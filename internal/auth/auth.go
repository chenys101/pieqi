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
//   external IP        → must present a valid tunnel token (single factor; the
//                        X-Feishu-Openid HTTP-layer check was dropped 2026-08 —
//                        Feishu identity now only gates IM tunnel commands)
//   tunnel ops         → additionally must come from Feishu mobile webview UA
//   bind/unbind        → internal IP only
//
// 「内网」判定 (IsInternalRequest) 建立在 ClientIP 之上，而 ClientIP 只在立即
// 对端为回环（本机 cloudflared 子进程）时才采信转发头 —— 见 ClientIP 注释，
// 这是防伪造内网判定的安全边界，不可放宽。
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

// isTrustedForwarder 判定「立即对端」是否是可信任的转发器 —— 即本机上的
// cloudflared 子进程（builder 侧 LocalURL 恒为 http://localhost:<port>，见
// cmd/pieqi/main.go）。回环地址（127.0.0.0/8、::1）即视为可信。
//
// ⚠️ 不变量：只有当 cloudflared 经回环连本机端口时，转发头才可信。若将来把
// LocalURL 改成非回环地址（如局域网 IP），本判定会失效并让隧道流量被误判成
// 「内网」而绕过全部鉴权 —— 改那里必须同步复核这里。
func isTrustedForwarder(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// remoteHost 取 RemoteAddr 的主机部分（无端口时原样返回）。
func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr // may be empty or already host-only
	}
	return host
}

// IPSource 解析客户端 IP，并返回**判定来源**，用于审计取证：
//   - "cf"    → CF-Connecting-IP（Cloudflare 边缘覆盖写入，客户端预置会被拦）；
//   - "xff"   → X-Forwarded-For 的末段（CF 把真实客户端 IP 追加在末尾）；
//   - "peer"  → 立即对端地址（对端不可信，或本机进程直连）；
//   - "none"  → 无法解析。
//
// 来源写进审计日志，是为了让「这次为什么被判成内网/外部」可回溯 —— 安全判定
// 依赖哪个头，必须能事后验证，而不是只看一个 IP 值。
func IPSource(r *http.Request) (ip, source string) {
	peer := remoteHost(r)
	if !isTrustedForwarder(peer) {
		if peer == "" {
			return "", "none"
		}
		return peer, "peer"
	}
	if cfip := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); cfip != "" {
		if net.ParseIP(cfip) != nil {
			return cfip, "cf"
		}
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		for i := len(parts) - 1; i >= 0; i-- {
			if p := strings.TrimSpace(parts[i]); net.ParseIP(p) != nil {
				return p, "xff"
			}
		}
	}
	if peer == "" {
		return "", "none"
	}
	return peer, "peer"
}

// ClientIP extracts the real client IP from a request.
//
// 纪律（2026-10-02 线上安全修复）：**转发头只在立即对端可信时才采信**。
// 旧实现无条件取 X-Forwarded-For 首跳，而 IsInternalRequest 建立在它之上，
// 于是任何人对外网域名加一个 `X-Forwarded-For: 127.0.0.1` 就被判定为内网，
// 完全绕过 ExternalAuthMiddleware / BindOpGateMiddleware（已线上实证：
// 无 token 的 GET /api/tasks 返回 200）。
//
// 关键在于：**到达回环端口的请求默认就是「内网」**（对端是 127.0.0.1），
// 唯一能让隧道流量被判成「外部」的，是一个客户端**无法控制**的转发头。
// 线上实测锁定（2026-10-02）：
//   - 客户端发 `X-Forwarded-For: 10.0.0.1, 1.2.3.4`，源站解析出的是真实公网
//     出口 116.22.3.237 而**不是** 1.2.3.4 → 说明被采信的那一跳由 CF 追加，
//     客户端预置段不可控；
//   - 客户端发 `X-Forwarded-For: 127.0.0.1`，源站解析出真实公网 IPv6；
//   - 伪造 `CF-Connecting-IP` 的请求被 Cloudflare 在边缘直接 403、压根不到源站。
//
// 解析顺序与 IPSource 一致（对端不可信 → 只认对端地址；否则 CF 头优先，
// 退化到 XFF 末跳）。返回 "" 表示无法解析（调用方按「外部」处理，deny-by-default）。
func ClientIP(r *http.Request) string {
	ip, _ := IPSource(r)
	return ip
}

// IsInternalRequest reports whether the request originates from an internal IP.
func IsInternalRequest(r *http.Request) bool {
	return IsInternalIP(ClientIP(r))
}
