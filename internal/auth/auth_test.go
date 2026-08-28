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

func TestClientIP_XFF(t *testing.T) {
	// Cloudflare 隧道流量经 CF 反代，真实 IP 在 X-Forwarded-For 首段
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 10.0.0.1")
	req.RemoteAddr = "10.0.0.1:1234"
	if got := ClientIP(req); got != "1.2.3.4" {
		t.Fatalf("ClientIP = %q, want 1.2.3.4", got)
	}
}

func TestClientIP_RemoteAddrFallback(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "8.8.8.8:5555"
	if got := ClientIP(req); got != "8.8.8.8" {
		t.Fatalf("ClientIP = %q, want 8.8.8.8", got)
	}
}
