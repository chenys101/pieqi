package auth

import (
	"bufio"
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
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
}

// NewTunnelManager constructs a manager. Does NOT start anything.
func NewTunnelManager(cfg TunnelConfig) *TunnelManager {
	return &TunnelManager{cfg: cfg, Tokens: cfg.Tokens}
}

// urlRegex matches the trycloudflare URL printed by cloudflared to stdout.
var urlRegex = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

// Start launches (or restarts) the cloudflared tunnel and issues a fresh
// TTL token. If a tunnel is already running, it is stopped first.
func (m *TunnelManager) Start(ctx context.Context, ttl time.Duration) (TunnelResult, error) {
	ts := m.Tokens
	if ts == nil {
		return TunnelResult{}, fmt.Errorf("token store not configured")
	}
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
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return TunnelResult{}, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return TunnelResult{}, fmt.Errorf("start cloudflared: %w", err)
	}

	// Wait for the trycloudflare URL on stdout (real cf prints within ~2s).
	cfURL := ""
	deadline := time.Now().Add(15 * time.Second)
	scan := bufio.NewScanner(stdout)
	for time.Now().Before(deadline) {
		if !scan.Scan() {
			if err := scan.Err(); err != nil {
				cancel()
				_ = cmd.Wait()
				return TunnelResult{}, fmt.Errorf("read cf stdout: %w", err)
			}
			// EOF before URL: process died
			cancel()
			_ = cmd.Wait()
			return TunnelResult{}, fmt.Errorf("cloudflared exited before printing URL")
		}
		line := scan.Text()
		if match := urlRegex.FindString(line); match != "" {
			cfURL = match
			break
		}
	}
	if cfURL == "" {
		cancel()
		_ = cmd.Wait()
		return TunnelResult{}, fmt.Errorf("cloudflared did not print trycloudflare URL within 15s")
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
