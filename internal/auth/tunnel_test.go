package auth

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeCloudflaredScript writes a tiny shell/bat script that prints a
// trycloudflare URL to stdout and stays alive until killed — same
// observable behavior as real cloudflared quick-tunnel mode.
func fakeCloudflaredScript(t *testing.T, url string) string {
	t.Helper()
	dir := t.TempDir()
	var path string
	var content string
	if runtime.GOOS == "windows" {
		path = filepath.Join(dir, "cloudflared.bat")
		content = "@echo off\r\necho INF url=" + url + "\r\nping -n 30 127.0.0.1 > nul\r\n"
	} else {
		path = filepath.Join(dir, "cloudflared.sh")
		content = "#!/bin/sh\necho 'INF url=" + url + "'\nsleep 30\n"
	}
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatalf("write fake cf: %v", err)
	}
	return path
}

func TestTunnel_StartParsesURL(t *testing.T) {
	binary := fakeCloudflaredScript(t, "https://abc-xyz.trycloudflare.com")
	m := NewTunnelManager(TunnelConfig{
		BinaryPath: binary,
		LocalURL:   "http://localhost:3000",
		Tokens:     NewTokenStore(),
		Logger:     nil,
	})
	res, err := m.Start(context.Background(), 15*time.Minute)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !strings.Contains(res.TunnelURL, "abc-xyz.trycloudflare.com") {
		t.Fatalf("tunnel url = %q, want trycloudflare URL", res.TunnelURL)
	}
	if res.Token == "" {
		t.Fatal("token must be issued on start")
	}
	if !m.IsActive() {
		t.Fatal("should be active after Start")
	}
	// Token is wired into the URL so the front-end can hand the full link
	// to Lark
	if !strings.Contains(res.TunnelURL, "token=") {
		t.Fatalf("tunnel url must include ?token=, got %q", res.TunnelURL)
	}
	// Cleanup
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if m.IsActive() {
		t.Fatal("should not be active after Stop")
	}
}

func TestTunnel_StopInvalidatesToken(t *testing.T) {
	binary := fakeCloudflaredScript(t, "https://t1.trycloudflare.com")
	m := NewTunnelManager(TunnelConfig{
		BinaryPath: binary, LocalURL: "http://localhost:3000",
		Tokens: NewTokenStore(),
	})
	res, _ := m.Start(context.Background(), time.Hour)
	if !m.Tokens.Validate(res.Token) {
		t.Fatal("token valid right after start")
	}
	_ = m.Stop(context.Background())
	if m.Tokens.Validate(res.Token) {
		t.Fatal("Stop must invalidate the tunnel token")
	}
}

func TestTunnel_StartTwiceResetsToken(t *testing.T) {
	binary := fakeCloudflaredScript(t, "https://t2.trycloudflare.com")
	m := NewTunnelManager(TunnelConfig{
		BinaryPath: binary, LocalURL: "http://localhost:3000",
		Tokens: NewTokenStore(),
	})
	first, _ := m.Start(context.Background(), time.Minute)
	second, _ := m.Start(context.Background(), time.Minute)
	if first.Token == second.Token {
		t.Fatal("second start must issue a fresh token")
	}
	if m.Tokens.Validate(first.Token) {
		t.Fatal("first token must be invalidated when a new tunnel starts")
	}
	_ = m.Stop(context.Background())
}

func TestTunnel_ResetIssuesNewToken(t *testing.T) {
	binary := fakeCloudflaredScript(t, "https://t3.trycloudflare.com")
	m := NewTunnelManager(TunnelConfig{
		BinaryPath: binary, LocalURL: "http://localhost:3000",
		Tokens: NewTokenStore(),
	})
	orig, _ := m.Start(context.Background(), time.Minute)
	newTok, err := m.ResetToken(context.Background())
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if newTok == orig.Token {
		t.Fatal("reset must issue a different token")
	}
	if m.Tokens.Validate(orig.Token) {
		t.Fatal("old token must die on reset")
	}
	if !m.Tokens.Validate(newTok) {
		t.Fatal("new token must be valid")
	}
	_ = m.Stop(context.Background())
}

func TestTunnel_StatusReflectsState(t *testing.T) {
	binary := fakeCloudflaredScript(t, "https://t4.trycloudflare.com")
	m := NewTunnelManager(TunnelConfig{
		BinaryPath: binary, LocalURL: "http://localhost:3000",
		Tokens: NewTokenStore(),
	})
	if st := m.Status(); st.Active {
		t.Fatal("should be inactive initially")
	}
	res, _ := m.Start(context.Background(), 15*time.Minute)
	st := m.Status()
	if !st.Active {
		t.Fatal("status should report active")
	}
	// Status is the safe (public) snapshot: the raw token must be masked
	// so a polling front-end cannot read it. The Task 10 handler test
	// also expects "token=***" in the status URL.
	if !strings.Contains(st.TunnelURL, "token=***") {
		t.Fatalf("status url must mask the token, got %q (start url %q)", st.TunnelURL, res.TunnelURL)
	}
	if !st.ExpiresAt.IsZero() && st.ExpiresAt.Before(time.Now()) {
		t.Fatal("expiry should be in the future")
	}
	_ = m.Stop(context.Background())
}

func TestTunnel_LarkDeepLinkFormat(t *testing.T) {
	binary := fakeCloudflaredScript(t, "https://t5.trycloudflare.com")
	m := NewTunnelManager(TunnelConfig{
		BinaryPath: binary, LocalURL: "http://localhost:3000",
		Tokens: NewTokenStore(),
	})
	res, _ := m.Start(context.Background(), time.Minute)
	if !strings.HasPrefix(res.LarkDeepLink, "lark://open?url=") {
		t.Fatalf("lark link = %q, want lark://open?url= prefix", res.LarkDeepLink)
	}
	if !strings.Contains(res.LarkDeepLink, "token=") {
		t.Fatal("lark link must embed the token")
	}
	_ = m.Stop(context.Background())
}
