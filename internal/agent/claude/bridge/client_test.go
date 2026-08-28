package bridge

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientCreateSessionError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"cwd required"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	_, err := c.CreateSession(context.Background(), CreateSessionRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "cwd required") {
		t.Fatalf("error = %q, want contains 'cwd required'", err)
	}
}

func TestClientPromptSessionClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"error":"session closed"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	err := c.Prompt(context.Background(), "sid", "hi", "r1")
	if err == nil || !strings.Contains(err.Error(), "session closed") {
		t.Fatalf("Prompt err = %v, want contains 'session closed'", err)
	}
}

func TestClientHealthDeadServer(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	url := "http://" + l.Addr().String()
	_ = l.Close() // 端口已关 → 连接拒绝

	c := NewClient(url)
	if err := c.Health(context.Background()); err == nil {
		t.Fatal("expected health error on dead server")
	}
}

func TestClientCloseSession(t *testing.T) {
	var deleted bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && r.URL.Path == "/v1/sessions/sid" {
			deleted = true
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"closed":true}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	if err := c.CloseSession(context.Background(), "sid"); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	if !deleted {
		t.Fatal("DELETE /v1/sessions/sid not called")
	}
}

// TestClientTokenHeader 校验带 token 的客户端给 /v1 请求附 Authorization: Bearer。
func TestClientTokenHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sekrit" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/v1/health":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"ok":true}`))
		case r.URL.Path == "/v1/sessions":
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"id":"s1"}`))
		case r.Method == http.MethodPost: // prompt/cancel/permissions → 202
			w.WriteHeader(http.StatusAccepted)
			w.Write([]byte(`{"ok":true}`))
		default: // DELETE close session → 200
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	c := NewClientWithToken(srv.URL, "sekrit")
	if err := c.Health(context.Background()); err != nil {
		t.Fatalf("Health: %v", err)
	}
	if _, err := c.CreateSession(context.Background(), CreateSessionRequest{Cwd: "/tmp"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := c.Prompt(context.Background(), "s1", "hi", "r1"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if err := c.CloseSession(context.Background(), "s1"); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
}

// TestClientNoTokenWithoutConfiguredToken 校验未配置 token 的客户端不附带 Authorization 头。
func TestClientNoTokenWithoutConfiguredToken(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	if err := NewClient(srv.URL).Health(context.Background()); err != nil {
		t.Fatalf("Health: %v", err)
	}
	if auth != "" {
		t.Fatalf("Authorization header = %q, want empty when no token", auth)
	}
}
