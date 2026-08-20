package auth

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// TunnelConfig wires the tunnel manager to its dependencies.
type TunnelConfig struct {
	BinaryPath string      // cloudflared executable (or fake script for tests)
	LocalURL   string      // upstream local URL cloudflared points at, e.g. http://localhost:3000
	Tokens     *TokenStore // token store; start/reset mutate this
	Logger     *zap.Logger // nil = silent

	// PIDFile 可选：cloudflared 子进程 PID 的落盘路径。用于跨重启清理
	// 孤儿进程 —— 服务被强杀（defer Stop 不执行）时 cloudflared 会残留，
	// 下次 Start 时按此文件杀掉上一次的残留，避免多份隧道堆积。
	// 空 = 不启用 PID 文件清理。
	PIDFile string
}

// TunnelResult is returned from Start/Reset — sent to the front-end as-is.
type TunnelResult struct {
	TunnelURL    string    // https://xxx.trycloudflare.com?token=yyy
	LarkDeepLink string    // lark://open?url=<tunnelURL with ?token= embedded raw>
	Token        string    // raw token (only ever returned via the API; never logged)
	ExpiresAt    time.Time // TTL expiry
}

// TunnelStatus is the safe GET response (no raw token leak on status).
type TunnelStatus struct {
	Active       bool      `json:"active"`
	TunnelURL    string    `json:"tunnel_url,omitempty"`
	LarkDeepLink string    `json:"lark_deep_link,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
}

// TunnelManager spawns and supervises a single cloudflared subprocess in
// quick-tunnel mode. Per PRD §4.5 it:
//   - launches cloudflared --url <LocalURL>
//   - scrapes the trycloudflare URL from stdout
//   - issues a fresh tunnel token (invalidating all old ones)
//   - on Stop / process exit, kills the subprocess and clears tokens
//
// Exactly one tunnel at a time. Start while already active first stops
// the previous one (per PRD §4.4 "重新开启新隧道" trigger).
//
// Reaping discipline: cmd.Wait() is invoked exactly once per spawned Cmd,
// by the background watch goroutine. Callers that tear the process down
// (Stop, or a Start that replaces an active tunnel) only Kill() it; they
// must NOT also call Wait() — Go's exec.Cmd.Wait deadlocks on a second
// call when exec.CommandContext is used (the internal ctxResult channel
// is sent only once).
type TunnelManager struct {
	cfg TunnelConfig

	mu      sync.Mutex
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	url     string
	token   string
	expires time.Time

	// Tokens exposes the underlying token store so middleware can validate
	// against the same instance. It is the same pointer as cfg.Tokens,
	// surfaced as a public field for direct access (e.g. m.Tokens.Validate).
	Tokens *TokenStore

	// killFunc 杀掉一个 pid。默认实现真杀；测试注入 fake 记录调用。
	killFunc func(pid int) error
}

// NewTunnelManager constructs a manager. Does NOT start anything.
func NewTunnelManager(cfg TunnelConfig) *TunnelManager {
	return &TunnelManager{
		cfg:    cfg,
		Tokens: cfg.Tokens,
		killFunc: func(pid int) error {
			proc, err := os.FindProcess(pid)
			if err != nil {
				return err
			}
			return proc.Kill()
		},
	}
}

// urlRegex matches the trycloudflare URL printed by cloudflared.
var urlRegex = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

// cleanupOrphans 杀掉上一次实例残留的 cloudflared 进程（PID 文件机制）。
// 服务被强杀时 defer Stop 不执行，cloudflared 会变孤儿残留；每次 Start
// 前清理，避免多份隧道进程堆积。PID 已退出（Kill 报错）时静默忽略。
// 若 PID 文件指向当前实例正在管理的活跃进程则跳过（交由 stopLocked 处理）。
func (m *TunnelManager) cleanupOrphans() {
	if m.cfg.PIDFile == "" || m.killFunc == nil {
		return
	}
	m.mu.Lock()
	activePID := 0
	if m.cmd != nil {
		activePID = m.cmd.Process.Pid
	}
	m.mu.Unlock()

	data, err := os.ReadFile(m.cfg.PIDFile)
	if err != nil {
		return // 无记录 = 无残留
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return
	}
	if pid == activePID {
		return // 当前实例活跃隧道，stopLocked 负责停它
	}
	if m.cfg.Logger != nil {
		m.cfg.Logger.Info("cleaning orphan cloudflared", zap.Int("pid", pid))
	}
	if err := m.killFunc(pid); err != nil {
		if m.cfg.Logger != nil {
			m.cfg.Logger.Debug("orphan cloudflared already gone", zap.Int("pid", pid), zap.Error(err))
		}
	}
	_ = os.Remove(m.cfg.PIDFile)
}

// writePIDFile 原子写入当前 cloudflared 子进程 PID。
func (m *TunnelManager) writePIDFile(pid int) {
	if m.cfg.PIDFile == "" {
		return
	}
	tmp := m.cfg.PIDFile + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(pid)), 0600); err != nil {
		if m.cfg.Logger != nil {
			m.cfg.Logger.Warn("write pid file", zap.Error(err))
		}
		return
	}
	_ = os.Rename(tmp, m.cfg.PIDFile)
}

// removePIDFile 删除 PID 文件（正常 Stop / 进程意外退出时）。
func (m *TunnelManager) removePIDFile() {
	if m.cfg.PIDFile == "" {
		return
	}
	_ = os.Remove(m.cfg.PIDFile)
}

// Start launches (or restarts) the cloudflared tunnel and issues a fresh
// TTL token. If a tunnel is already running, it is stopped first.
func (m *TunnelManager) Start(ctx context.Context, ttl time.Duration) (TunnelResult, error) {
	ts := m.Tokens
	if ts == nil {
		return TunnelResult{}, fmt.Errorf("token store not configured")
	}
	// 清理上次实例强杀后残留的孤儿 cloudflared（PID 文件机制）。
	// 必须在 stopLocked 之前：stopLocked 会删除 PID 文件，先清孤儿再停
	// 当前实例（cleanupOrphans 会跳过当前活跃进程，不误杀）。
	m.cleanupOrphans()

	// Stop any existing tunnel first (PRD §4.4 trigger: 重新开启新隧道).
	// stopLocked assumes the mutex is held, so take it here (Stop takes
	// it itself, but this Start-path call does not go through Stop).
	m.mu.Lock()
	_ = m.stopLocked(context.Background())
	m.mu.Unlock()

	tok, err := ts.IssueForNewTunnel(ttl)
	if err != nil {
		return TunnelResult{}, fmt.Errorf("issue token: %w", err)
	}

	subCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(subCtx, m.cfg.BinaryPath, "tunnel", "--url", m.cfg.LocalURL)
	// cloudflared 把日志（含 trycloudflare URL）打到 stderr 而非 stdout，
	// 因此两条管道都要扫。每条管道一个 goroutine，命中 URL 发 urlCh；
	// 主协程用 select+timer 等结果 —— 阻塞式 Scan 会让 15s deadline 失效。
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return TunnelResult{}, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return TunnelResult{}, fmt.Errorf("stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return TunnelResult{}, fmt.Errorf("start cloudflared: %w", err)
	}

	urlCh := make(chan string, 2)
	doneCh := make(chan struct{}, 2)
	scanPipe := func(r io.Reader) {
		scan := bufio.NewScanner(r)
		for scan.Scan() {
			if m := urlRegex.FindString(scan.Text()); m != "" {
				urlCh <- m
				return
			}
		}
		doneCh <- struct{}{}
	}
	go scanPipe(stdout)
	go scanPipe(stderr)

	// 等 trycloudflare URL（cloudflared 约 2s 内打印）。只有当两条管道都
	// EOF 才判定"进程退出"——stdout 常常立即 EOF（空），必须继续等 stderr。
	cfURL := ""
	doneCount := 0
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	for cfURL == "" {
		select {
		case cfURL = <-urlCh:
		case <-doneCh:
			doneCount++
			if doneCount == 2 {
				cancel()
				_ = cmd.Wait()
				return TunnelResult{}, fmt.Errorf("cloudflared exited before printing URL")
			}
		case <-timer.C:
			cancel()
			_ = cmd.Wait()
			return TunnelResult{}, fmt.Errorf("cloudflared did not print trycloudflare URL within 15s")
		}
	}

	// Wire the token into the URL and build the Lark deep link. The token
	// is embedded as a literal ?token= query param (not percent-encoded)
	// so the front-end can hand the link straight to Lark and the Pieqi
	// server's ?token= extractor (PRD §4.6) reads it without re-decoding.
	full := cfURL
	q := url.Values{}
	q.Set("token", tok)
	if strings.Contains(full, "?") {
		full = full + "&" + q.Encode()
	} else {
		full = full + "?" + q.Encode()
	}
	lark := "lark://open?url=" + full

	// Stash state + supervise the process so its death clears tokens.
	expires := time.Now().Add(ttl)
	m.mu.Lock()
	m.cmd = cmd
	m.cancel = cancel
	m.url = full
	m.token = tok
	m.expires = expires
	m.mu.Unlock()
	m.writePIDFile(cmd.Process.Pid)

	// Background watcher reaps the process and, on an *unexpected* exit,
	// clears all tokens (PRD §4.4 trigger: Cloudflared 进程意外退出).
	go m.watch(cmd, cancel)

	if m.cfg.Logger != nil {
		m.cfg.Logger.Info("tunnel started", zap.String("tunnel_url_no_token", cfURL), zap.Time("expires_at", expires))
	}
	return TunnelResult{
		TunnelURL:    full,
		LarkDeepLink: lark,
		Token:        tok,
		ExpiresAt:    expires,
	}, nil
}

// watch blocks until the cloudflared subprocess exits, then reaps it
// (the single cmd.Wait() for this Cmd) and — only if THIS tunnel is
// still the active one — clears all tokens. If the tunnel has been
// replaced by a newer Start or torn down by Stop, watch does nothing
// to the token store (the active tunnel, or Stop, owns the tokens).
func (m *TunnelManager) watch(cmd *exec.Cmd, cancel context.CancelFunc) {
	_ = cmd.Wait() // sole reaper for this Cmd (see TunnelManager doc)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd != cmd {
		// Replaced or explicitly stopped: do not clobber the active
		// tunnel's token. Stop/Start already invalidated what needed
		// invalidating.
		return
	}
	m.cmd = nil
	m.cancel = nil
	m.url = ""
	m.token = ""
	m.expires = time.Time{}
	m.removePIDFile()
	if ts := m.Tokens; ts != nil {
		ts.InvalidateAll()
	}
	if m.cfg.Logger != nil {
		m.cfg.Logger.Warn("tunnel process exited; tokens cleared")
	}
}

// Stop kills the running tunnel and clears all tokens.
func (m *TunnelManager) Stop(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopLocked(ctx)
}

// stopLocked assumes the mutex is held. It Kill()s the subprocess but
// does NOT Wait() for it — the background watch goroutine is the sole
// reaper (calling Wait() here too deadlocks under exec.CommandContext).
func (m *TunnelManager) stopLocked(ctx context.Context) error {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if m.cmd != nil {
		_ = m.cmd.Process.Kill()
		m.cmd = nil
	}
	m.url = ""
	m.token = ""
	m.expires = time.Time{}
	m.removePIDFile()
	if ts := m.Tokens; ts != nil {
		ts.InvalidateAll()
	}
	return nil
}

// ResetToken issues a new token for the running tunnel, invalidating the
// old one. The tunnel subprocess itself stays up — only the token rotates.
// Returns an error if no tunnel is active.
func (m *TunnelManager) ResetToken(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd == nil {
		return "", fmt.Errorf("no active tunnel")
	}
	ts := m.Tokens
	if ts == nil {
		return "", fmt.Errorf("token store not configured")
	}
	// Drop every prior token, then mint one fresh. The remaining TTL is
	// preserved so a reset mid-life does not extend the tunnel's lifetime.
	ts.InvalidateAll()
	remaining := time.Until(m.expires)
	if remaining <= 0 {
		remaining = 15 * time.Minute
	}
	fresh, err := ts.Issue(remaining)
	if err != nil {
		return "", err
	}
	m.token = fresh
	// Rebuild the cached URL with the new token (the trycloudflare hostname
	// does not change on reset — only the query param rotates).
	if i := strings.Index(m.url, "token="); i >= 0 {
		base := m.url[:i]
		if amp := strings.Index(m.url[i:], "&"); amp >= 0 {
			base = base + m.url[i+amp:] // preserve any subsequent params
		}
		m.url = base + "token=" + fresh
	} else {
		m.url = m.url + "?token=" + fresh
	}
	return fresh, nil
}

// IsActive reports whether a tunnel is currently running.
func (m *TunnelManager) IsActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cmd != nil
}

// Status returns a safe snapshot (no raw token in JSON output — the
// token is replaced with "***" so a polling front-end cannot read it).
func (m *TunnelManager) Status() TunnelStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd == nil {
		return TunnelStatus{Active: false}
	}
	safeURL := m.url
	if i := strings.Index(safeURL, "token="); i >= 0 {
		safeURL = safeURL[:i] + "token=***"
	}
	return TunnelStatus{
		Active:       true,
		TunnelURL:    safeURL,
		LarkDeepLink: "", // not exposed on status; only on start/reset
		ExpiresAt:    m.expires,
	}
}

// wipeTokensOnDebugOff clears all tunnel tokens when the debug switch
// transitions from ON to OFF. PRD §4.4 lists this as an invalidation
// trigger. main.go is responsible for calling this at the toggle point
// (the DebugSwitch itself is just an atomic flag and doesn't know about
// the token store).
func wipeTokensOnDebugOff(dbg *DebugSwitch, ts *TokenStore) {
	if ts == nil {
		return
	}
	ts.InvalidateAll()
}
