package auth

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
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

	// OnHeal 在自动健康检查发现 trycloudflare 域名被 Cloudflare 回收、
	// 并成功重启隧道（换全新域名 + 新 token）后回调。参数为新隧道结果
	// （含新 URL / lark 深链 / token / 到期时间）。nil = 静默。
	// TunnelManager 保持飞书无关 —— 接线方（main.go）负责把新链接推给用户。
	OnHeal func(TunnelResult)

	// OnHealErr 在自动自愈失败（旧隧道已死、新 cloudflared 起不来）时回调，
	// 此时隧道已完全下线，需告知用户手动重启。nil = 静默。
	OnHealErr func(error)
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

	// --- 自动自愈（域名回收检测）---
	// healthLoop 是进程生命周期的单例 goroutine（StartHealthCheck 幂等启动），
	// 每 tick 探测当前活跃隧道的 hostname；连续失败达到阈值后自动重启换新域名。
	healthStop chan struct{}            // 关闭即停止巡检（StopHealthCheck）
	healthDone chan struct{}            // 巡检 goroutine 退出信号（测试收尾用）
	healthOnce sync.Once                // 保证只启动一个巡检 goroutine
	stopOnce   sync.Once                // 保证 healthStop 只 close 一次
	healthCheck func(rawURL string) bool // 注入式探测；nil = 默认 HTTP 探测 hostAlive
	ttl        time.Duration            // 最近一次 Start 的 TTL，自愈重启时沿用（用户设定的 15m/1h/4h 意图）
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
	m.ttl = ttl // 自愈重启沿用本次 TTL（仅 Start 设置；RenewToken 的 ttl 是增量，不覆盖）
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
	m.url = rebuildURLWithToken(m.url, fresh)
	return fresh, nil
}

// RenewToken extends the running tunnel's current token TTL. Two cases:
//   - token 仍有效 → 延长有效期（token 值不变，已分发链接继续可用）；
//   - token 已过期/失效但 cloudflared 进程仍在 → 同一隧道上签发全新 token
//     （域名不变，仅 URL 里的 token 参数轮换）。旧 token 已死，轮换零损失，
//     免去"过期后必须重启隧道、丢掉现有域名"的折腾。
//
// 返回 TunnelResult（与 Start 同构），前端渲染同款链接/QR/token/到期块。
// 仅当无活跃隧道时返回错误。
func (m *TunnelManager) RenewToken(ctx context.Context, ttl time.Duration) (TunnelResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd == nil {
		return TunnelResult{}, fmt.Errorf("no active tunnel")
	}
	ts := m.Tokens
	if ts == nil {
		return TunnelResult{}, fmt.Errorf("token store not configured")
	}
	if m.token != "" && ts.Renew(m.token, ttl) {
		expires := m.expires.Add(ttl)
		m.expires = expires
		return TunnelResult{
			TunnelURL:    m.url,
			LarkDeepLink: "lark://open?url=" + m.url,
			Token:        m.token,
			ExpiresAt:    expires,
		}, nil
	}
	fresh, err := ts.IssueForNewTunnel(ttl)
	if err != nil {
		return TunnelResult{}, fmt.Errorf("issue token: %w", err)
	}
	m.token = fresh
	m.url = rebuildURLWithToken(m.url, fresh)
	expires := time.Now().Add(ttl)
	m.expires = expires
	return TunnelResult{
		TunnelURL:    m.url,
		LarkDeepLink: "lark://open?url=" + m.url,
		Token:        fresh,
		ExpiresAt:    expires,
	}, nil
}

// rebuildURLWithToken 把 URL 里的 token 查询参数替换为 fresh。隧道域名不变，
// 仅 token 参数轮换；保留 token 之后的其余参数（如有）。
func rebuildURLWithToken(url, fresh string) string {
	if i := strings.Index(url, "token="); i >= 0 {
		base := url[:i]
		if amp := strings.Index(url[i:], "&"); amp >= 0 {
			base = base + url[i+amp:] // preserve any subsequent params
		}
		return base + "token=" + fresh
	}
	return url + "?token=" + fresh
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

// FullURL returns the current tunnel URL with the RAW token embedded
// (https://xxx.trycloudflare.com?token=yyy). Unlike Status(), nothing is
// masked — so it must only ever feed already-authenticated callers
// (e.g. the preview attach link builder in the API layer). Empty when no
// tunnel is active.
func (m *TunnelManager) FullURL() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd == nil {
		return ""
	}
	return m.url
}

// StartHealthCheck 启动后台巡检 goroutine（幂等：整个进程生命周期只启一个）。
// interval <= 0 或 failures < 1 时不启动。巡检逻辑见 healthLoop。
func (m *TunnelManager) StartHealthCheck(interval time.Duration, failures int) {
	if interval <= 0 || failures < 1 {
		return
	}
	m.healthOnce.Do(func() {
		m.healthStop = make(chan struct{})
		m.healthDone = make(chan struct{})
		go m.healthLoop(interval, failures)
	})
}

// StopHealthCheck 停止巡检 goroutine。幂等；仅在测试收尾调用（进程退出由
// os.Exit 直接终止，无需优雅停止，故不接到 main.go 的 SIGINT handler）。
func (m *TunnelManager) StopHealthCheck() {
	if m.healthStop == nil {
		return
	}
	m.stopOnce.Do(func() {
		close(m.healthStop)
		<-m.healthDone
	})
}

// healthLoop 周期性探测当前活跃隧道 hostname 的存活。Cloudflare 会周期性
// 回收 trycloudflare 快速隧道域名（公网 DNS 变 NXDOMAIN），即使 cloudflared
// 进程仍连着、状态仍报告 active —— 已分发的链接彻底失效。此时自动重启
// 隧道拿全新域名 + 新 token，并回调 OnHeal / OnHealErr 让接线方推送新链接。
//
// 判定纪律：连续 failures 次探测失败才确认死亡，避免 ISP/DNS 瞬时抖动误杀。
// "无活跃隧道"（含崩溃后 watch 已清空状态）一律跳过并归零 —— 本特性只覆盖
// 域名回收，不复活管理员主动关闭的隧道。
func (m *TunnelManager) healthLoop(interval time.Duration, failures int) {
	defer close(m.healthDone)
	check := m.healthCheck
	if check == nil {
		check = hostAlive
	}
	t := time.NewTicker(interval)
	defer t.Stop()

	streak := 0
	for {
		select {
		case <-m.healthStop:
			return
		case <-t.C:
			// 快照当前状态（锁内取值，锁外探测 + 重启）。
			m.mu.Lock()
			if m.cmd == nil || m.url == "" {
				m.mu.Unlock()
				streak = 0
				continue
			}
			rawURL := m.url
			ttl := m.ttl
			m.mu.Unlock()

			if check(rawURL) {
				streak = 0
				continue
			}
			streak++
			if streak < failures {
				continue
			}
			streak = 0 // 已触发一轮自愈，不再累计

			if host := logHostnameOnly(rawURL); host != "" && m.cfg.Logger != nil {
				m.cfg.Logger.Info("tunnel hostname unreachable; auto-healing", zap.String("host", host))
			}
			if ttl <= 0 {
				ttl = 15 * time.Minute
			}
			// 锁已释放：Start 内部自取锁（含 stopLocked），无重入风险；
			// 旧 watch goroutine 醒来会因 m.cmd != oldCmd 直接返回，不碰 token。
			res, err := m.Start(context.Background(), ttl)
			if err != nil {
				if m.cfg.Logger != nil {
					m.cfg.Logger.Warn("tunnel auto-heal restart failed; tunnel is down", zap.Error(err))
				}
				if m.cfg.OnHealErr != nil {
					m.cfg.OnHealErr(err)
				}
				continue
			}
			// 竞态缓解：若自愈重启期间管理员刚好「关隧道」，Start 里 stopLocked
			// 已把 cmd 置 nil，但新 spawn 仍会 stash 进去导致隧道"复活"；这里复查
			// IsActive，为假则跳过"已自愈"通知，避免误导管理员。
			if !m.IsActive() {
				continue
			}
			if m.cfg.Logger != nil {
				m.cfg.Logger.Info("tunnel auto-healed; new hostname issued", zap.String("tunnel_url_no_token", logHostnameOnly(res.TunnelURL)))
			}
			if m.cfg.OnHeal != nil {
				m.cfg.OnHeal(res)
			}
		}
	}
}

// logHostnameOnly 从带 token 的完整 URL 里取出纯 hostname，供日志使用
// （延续"token 绝不落日志"的纪律，见 TunnelResult.Token 注释）。
func logHostnameOnly(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Host
}

// hostAlive 默认探测：GET https://<host>/ 能否拿到任何 HTTP 响应。
// 关键：**任何状态码（含 401/403/302/502）都证明 hostname 在公网存活**
// —— 请求能到达 Cloudflare 边缘并被隧道转发回来；只有 DNS 解析失败 /
// 连接失败 / 超时算"死亡"。因此 token 过期导致的 401 不会误触发换域名，
// 域名存活检测与 token 存活检测（RenewToken 的职责）被正确解耦。
// 每次探测新建 Client + Transport{Proxy:nil}：忽略 HTTP(S)_PROXY 环境变量，
// 保证 NXDOMAIN 对本机 DNS 真实可观测，且不命中 keep-alive 的旧连接。
func hostAlive(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	probe := u.Scheme + "://" + u.Host + "/"
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probe, nil)
	if err != nil {
		return false
	}
	client := &http.Client{
		Timeout: 8 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse // 不跟随重定向，首响应即存活证据
		},
		Transport: &http.Transport{Proxy: nil},
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	_, _ = io.Copy(io.Discard, resp.Body) // 排干 body，避免 keep-alive 钉住陈旧连接
	_ = resp.Body.Close()
	return true
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
