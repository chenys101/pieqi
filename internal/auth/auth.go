// Package auth implements Pieqi's layered external access control:
//   - Global debug switch (highest priority, bypasses everything)
//   - Internal/external IP classification
//   - Feishu single-account identity binding (file-persisted)
//   - In-memory TTL tunnel tokens (deepseek-harness-desktop model)
//   - Cloudflared subprocess tunnel management
//   - IP rate limiting for token brute force
//   - Structured audit logging
//
// Permission matrix (debug=false):
//   internal IP        → all access, no token, no identity
//   external IP        → must have valid tunnel token AND matching X-Feishu-Openid
//   tunnel ops         → additionally must come from Feishu mobile webview UA
//   bind/unbind        → internal IP only
package auth

import (
	"net"
	"net/http"
	"strings"
	"sync/atomic"
)

// DebugSwitch is the global "skip all auth" flag. Highest priority: when
// Enabled, every check short-circuits to allow. Mutable at runtime via
// Set; reads are atomic.
type DebugSwitch struct {
	enabled atomic.Bool
}

// NewDebugSwitch creates a switch with the given initial state.
func NewDebugSwitch(enabled bool) *DebugSwitch {
	d := &DebugSwitch{}
	d.enabled.Store(enabled)
	return d
}

// Enabled reports the current debug state.
func (d *DebugSwitch) Enabled() bool { return d.enabled.Load() }

// Set toggles the debug state. Switching from true→false also invalidates
// all tunnel tokens (handled by TunnelManager watching this; see tunnel.go).
func (d *DebugSwitch) Set(v bool) { d.enabled.Store(v) }

// BypassAll reports whether all auth checks should be skipped.
func (d *DebugSwitch) BypassAll() bool { return d.enabled.Load() }

// IsInternalIP reports whether ip is in a private / loopback range.
// Internal: 127.0.0.0/8, ::1, 10.0.0.0/8, 192.168.0.0/16, 172.16.0.0/12.
// Empty or unparseable strings are treated as external (deny-by-default).
func IsInternalIP(ip string) bool {
	if ip == "" {
		return false
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	if parsed.IsLoopback() {
		return true
	}
	if ip4 := parsed.To4(); ip4 != nil {
		switch {
		case ip4[0] == 10:
			return true
		case ip4[0] == 192 && ip4[1] == 168:
			return true
		case ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31:
			return true
		}
	}
	return false
}

// ClientIP extracts the real client IP from a request. Honors
// X-Forwarded-For first hop (Cloudflare tunnel traffic), then falls back
// to RemoteAddr. Returns "" if neither parses.
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.IndexByte(xff, ','); idx >= 0 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr // may be empty or already host-only
	}
	return host
}

// IsInternalRequest reports whether the request originates from an internal IP.
func IsInternalRequest(r *http.Request) bool {
	return IsInternalIP(ClientIP(r))
}
