package auth

import (
	"net/http/httptest"
	"testing"
)

func TestIsInternalIP(t *testing.T) {
	cases := []struct{ ip string; want bool }{
		{"127.0.0.1", true},
		{"::1", true},
		{"10.0.0.5", true},
		{"192.168.1.100", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"172.32.0.1", false},     // outside private range
		{"8.8.8.8", false},
		{"1.2.3.4", false},
		{"", false},
		{"not-an-ip", false},
	}
	for _, c := range cases {
		if got := IsInternalIP(c.ip); got != c.want {
			t.Errorf("IsInternalIP(%q) = %v, want %v", c.ip, got, c.want)
		}
	}
}

func TestDebugSwitch_BypassAll(t *testing.T) {
	dbg := NewDebugSwitch(true)
	if !dbg.BypassAll() {
		t.Fatal("debug enabled should bypass all")
	}
	dbg.Set(false)
	if dbg.BypassAll() {
		t.Fatal("debug disabled should not bypass")
	}
}

// TestClientIP_XFF 隧道流量（对端=本机 cloudflared，回环）才采信转发头。
// 旧实现取 XFF 首跳，而 CF 会把客户端预置值保留在前、真实 IP 追加在后，
// 于是首跳是攻击者可控段 —— 本用例锁死「取最后一跳」这个修复契约。
func TestClientIP_XFF(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 10.0.0.1")
	req.RemoteAddr = "127.0.0.1:1234"
	if got := ClientIP(req); got != "10.0.0.1" {
		t.Fatalf("ClientIP = %q, want 10.0.0.1（取末跳；首跳是客户端可预置的）", got)
	}
}

func TestClientIP_RemoteAddrFallback(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "8.8.8.8:5555"
	if got := ClientIP(req); got != "8.8.8.8" {
		t.Fatalf("ClientIP = %q, want 8.8.8.8", got)
	}
}

// TestClientIP_UntrustedPeerIgnoresForwardingHeaders 是本次安全修复的核心回归：
// 非回环对端携带任何转发头都不得改变判定。
func TestClientIP_UntrustedPeerIgnoresForwardingHeaders(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		headers    map[string]string
		want       string
	}{
		{
			name:       "外网对端伪造 XFF 首跳为回环",
			remoteAddr: "203.0.113.9:5555",
			headers:    map[string]string{"X-Forwarded-For": "127.0.0.1"},
			want:       "203.0.113.9",
		},
		{
			name:       "外网对端伪造 XFF 首跳为私网",
			remoteAddr: "203.0.113.9:5555",
			headers:    map[string]string{"X-Forwarded-For": "192.168.1.1, 203.0.113.9"},
			want:       "203.0.113.9",
		},
		{
			name:       "外网对端伪造 CF-Connecting-IP 为回环",
			remoteAddr: "203.0.113.9:5555",
			headers:    map[string]string{"CF-Connecting-IP": "127.0.0.1"},
			want:       "203.0.113.9",
		},
		{
			name:       "局域网对端伪造 CF-Connecting-IP",
			remoteAddr: "192.168.1.50:5555",
			headers:    map[string]string{"CF-Connecting-IP": "10.0.0.1"},
			want:       "192.168.1.50",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = c.remoteAddr
			for k, v := range c.headers {
				req.Header.Set(k, v)
			}
			if got := ClientIP(req); got != c.want {
				t.Fatalf("ClientIP = %q, want %q（对端不可信时以对端地址为准）", got, c.want)
			}
		})
	}
}

// TestIsInternalRequest_SpoofedXFFNotInternal 端到端语义：外网请求伪造内网
// 转发头后，绝不能被判为内网。这是线上被利用的那条路径。
func TestIsInternalRequest_SpoofedXFFNotInternal(t *testing.T) {
	for _, spoof := range []string{"127.0.0.1", "10.0.0.1", "192.168.1.1", "172.16.0.1"} {
		req := httptest.NewRequest("GET", "/api/tasks", nil)
		req.RemoteAddr = "203.0.113.9:5555"
		req.Header.Set("X-Forwarded-For", spoof)
		if IsInternalRequest(req) {
			t.Fatalf("伪造 X-Forwarded-For=%q 被判为内网：鉴权被绕过", spoof)
		}
	}
}

// TestClientIP_TrustedForwarderPrefersCFConnectingIP 隧道流量：CF-Connecting-IP
// 由 Cloudflare 覆盖写入，优先于 XFF。
func TestClientIP_TrustedForwarderPrefersCFConnectingIP(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "[::1]:1234" // IPv6 回环对端
	req.Header.Set("CF-Connecting-IP", "198.51.100.7")
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := ClientIP(req); got != "198.51.100.7" {
		t.Fatalf("ClientIP = %q, want 198.51.100.7", got)
	}
}

// TestClientIP_TrustedForwarderSkipsInvalidHops 末跳无效时向前回溯到最近的有效 IP。
func TestClientIP_TrustedForwarderSkipsInvalidHops(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "1.2.3.4, unknown, ")
	if got := ClientIP(req); got != "1.2.3.4" {
		t.Fatalf("ClientIP = %q, want 1.2.3.4", got)
	}
}

// TestClientIP_LocalProcessNoHeaders 本机进程直连 API（无转发头）→ 回环对端即答案，
// 仍被判为内网，内网放行语义不变。
func TestClientIP_LocalProcessNoHeaders(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	if got := ClientIP(req); got != "127.0.0.1" {
		t.Fatalf("ClientIP = %q, want 127.0.0.1", got)
	}
	if !IsInternalRequest(req) {
		t.Fatal("本机进程直连应判为内网")
	}
}
