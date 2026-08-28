package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"pieqi/internal/auth"
	"pieqi/internal/config"

	"github.com/gin-gonic/gin"
)

func fakeCFForAPITest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	url := "https://api-test.trycloudflare.com"
	if runtime.GOOS == "windows" {
		p := filepath.Join(dir, "cf.bat")
		_ = os.WriteFile(p, []byte("@echo off\r\necho INF url="+url+"\r\nping -n 30 127.0.0.1 > nul\r\n"), 0755)
		return p
	}
	p := filepath.Join(dir, "cf.sh")
	_ = os.WriteFile(p, []byte("#!/bin/sh\necho 'INF url="+url+"'\nsleep 30\n"), 0755)
	return p
}

func newServerWithTunnel(t *testing.T) (*Server, *auth.TunnelManager) {
	t.Helper()
	svc := newAuthSvcForTest(t)
	tm := auth.NewTunnelManager(auth.TunnelConfig{
		BinaryPath: fakeCFForAPITest(t),
		LocalURL:   "http://localhost:3000",
		Tokens:     svc.Tokens,
	})
	srv := &Server{auth: svc, tunnel: tm, cfg: &config.Config{}}
	return srv, tm
}

func TestTunnelStart_PC_Rejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv, _ := newServerWithTunnel(t)
	g := gin.New()
	g.POST("/api/tunnel/start", srv.auth.TunnelOpGateMiddleware(), srv.tunnelStart)
	req, _ := http.NewRequest("POST", "/api/tunnel/start", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 Chrome")
	req.RemoteAddr = "4.4.4.4:1"
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("PC tunnel start → 403, got %d", w.Code)
	}
}

func TestTunnelStart_LarkMobile_OK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv, _ := newServerWithTunnel(t)
	g := gin.New()
	g.POST("/api/tunnel/start", srv.auth.TunnelOpGateMiddleware(), srv.tunnelStart)
	body := `{"ttl":"15m"}`
	req, _ := http.NewRequest("POST", "/api/tunnel/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Lark/12.3 (iPhone)")
	req.RemoteAddr = "4.4.4.4:1"
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("lark mobile tunnel start → 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		TunnelURL    string `json:"tunnel_url"`
		LarkDeepLink string `json:"lark_deep_link"`
		Token        string `json:"token"`
		ExpiresAt    string `json:"expires_at"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if !strings.Contains(resp.TunnelURL, "trycloudflare.com") {
		t.Errorf("tunnel_url = %q", resp.TunnelURL)
	}
	if !strings.HasPrefix(resp.LarkDeepLink, "lark://open?url=") {
		t.Errorf("lark link = %q", resp.LarkDeepLink)
	}
	if resp.Token == "" {
		t.Error("token must be returned")
	}
	// Cleanup
	_ = srv.tunnel.Stop(req.Context())
}

func TestTunnelStop_OK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv, _ := newServerWithTunnel(t)
	_, _ = srv.tunnel.Start(context.Background(), time.Minute) // start directly for setup
	g := gin.New()
	g.POST("/api/tunnel/stop", srv.auth.TunnelOpGateMiddleware(), srv.tunnelStop)
	req, _ := http.NewRequest("POST", "/api/tunnel/stop", nil)
	req.Header.Set("User-Agent", "Lark/12.3 (iPhone)")
	req.RemoteAddr = "4.4.4.4:1"
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("stop → 200, got %d", w.Code)
	}
	if srv.tunnel.IsActive() {
		t.Fatal("tunnel should be stopped")
	}
}

func TestTunnelReset_OK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv, _ := newServerWithTunnel(t)
	orig, _ := srv.tunnel.Start(context.Background(), time.Minute)
	g := gin.New()
	g.POST("/api/tunnel/reset", srv.auth.TunnelOpGateMiddleware(), srv.tunnelReset)
	req, _ := http.NewRequest("POST", "/api/tunnel/reset", nil)
	req.Header.Set("User-Agent", "Lark/12.3 (iPhone)")
	req.RemoteAddr = "4.4.4.4:1"
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("reset → 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct{ Token string `json:"token"` }
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Token == orig.Token {
		t.Fatal("reset must issue a new token")
	}
	_ = srv.tunnel.Stop(req.Context())
}

func TestTunnelStatus_OK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv, _ := newServerWithTunnel(t)
	_, _ = srv.tunnel.Start(context.Background(), time.Minute)
	g := gin.New()
	g.GET("/api/tunnel/status", srv.tunnelStatus)
	req, _ := http.NewRequest("GET", "/api/tunnel/status", nil)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status → 200, got %d", w.Code)
	}
	var resp struct {
		Active    bool   `json:"active"`
		TunnelURL string `json:"tunnel_url"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp.Active {
		t.Fatal("should be active")
	}
	if !strings.Contains(resp.TunnelURL, "token=***") {
		t.Errorf("status url must mask token: %q", resp.TunnelURL)
	}
	_ = srv.tunnel.Stop(req.Context())
}
