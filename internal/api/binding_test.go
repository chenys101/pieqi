package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"pieqi/internal/auth"
	"pieqi/internal/config"

	"github.com/gin-gonic/gin"
)

func newAuthSvcForTest(t *testing.T) *auth.Service {
	t.Helper()
	bindings, err := auth.NewBindingStore(filepath.Join(t.TempDir(), "b.json"))
	if err != nil {
		t.Fatalf("binding store: %v", err)
	}
	return &auth.Service{
		Debug:    auth.NewDebugSwitch(false),
		Bindings: bindings,
		Tokens:   auth.NewTokenStore(),
		Limiter:  auth.NewIPLimiter(5, 10*time.Minute),
	}
}

func TestBind_Success_Internal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := &Server{auth: newAuthSvcForTest(t), cfg: &config.Config{}}
	g := gin.New()
	g.POST("/api/auth/bind", srv.auth.BindOpGateMiddleware(), srv.bind)

	body, _ := json.Marshal(map[string]string{
		"openid": "ou_admin", "user_id": "u1", "nickname": "Boss",
	})
	req, _ := http.NewRequest("POST", "/api/auth/bind", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.168.1.10:1234" // internal
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("bind status=%d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["openid"] != "ou_admin" {
		t.Fatalf("resp = %+v", resp)
	}
	// Verify persisted
	b, ok := srv.auth.Bindings.Get()
	if !ok || b.OpenID != "ou_admin" {
		t.Fatalf("binding not persisted: %+v", b)
	}
}

func TestBind_RejectsExternal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := &Server{auth: newAuthSvcForTest(t), cfg: &config.Config{}}
	g := gin.New()
	g.POST("/api/auth/bind", srv.auth.BindOpGateMiddleware(), srv.bind)
	body, _ := json.Marshal(map[string]string{"openid": "ou_x"})
	req, _ := http.NewRequest("POST", "/api/auth/bind", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "8.8.8.8:1234" // external
	req.Header.Set("X-Feishu-Openid", "ou_x")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("external bind → 403, got %d", w.Code)
	}
}

func TestUnbind_Success_Internal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := &Server{auth: newAuthSvcForTest(t), cfg: &config.Config{}}
	_, _ = srv.auth.Bindings.Bind(auth.Binding{OpenID: "ou_x"})
	g := gin.New()
	g.DELETE("/api/auth/bind", srv.auth.BindOpGateMiddleware(), srv.unbind)
	req, _ := http.NewRequest("DELETE", "/api/auth/bind", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("unbind status=%d", w.Code)
	}
	if srv.auth.Bindings.IsBound() {
		t.Fatal("should be unbound")
	}
}

func TestAuthStatus_ReportsBinding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := &Server{auth: newAuthSvcForTest(t), cfg: &config.Config{}}
	_, _ = srv.auth.Bindings.Bind(auth.Binding{OpenID: "ou_admin", Nickname: "Boss"})
	g := gin.New()
	g.GET("/api/auth/status", srv.authStatus)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/auth/status", nil)
	req.RemoteAddr = "192.168.1.5:1234" // internal — so authStatus reveals the bound OpenID
	g.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	var resp struct {
		Bound    bool   `json:"bound"`
		OpenID   string `json:"openid"`
		Nickname string `json:"nickname"`
		Debug    bool   `json:"debug"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp.Bound || resp.OpenID != "ou_admin" {
		t.Fatalf("status resp = %+v", resp)
	}
	if resp.Debug {
		t.Fatal("debug should be false")
	}
}

func TestAuthStatus_ExternalHidesOpenID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := &Server{auth: newAuthSvcForTest(t), cfg: &config.Config{}}
	_, _ = srv.auth.Bindings.Bind(auth.Binding{OpenID: "ou_secret", Nickname: "Boss"})
	g := gin.New()
	g.GET("/api/auth/status", srv.authStatus)
	req, _ := http.NewRequest("GET", "/api/auth/status", nil)
	req.RemoteAddr = "8.8.8.8:1234" // external
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	var resp struct {
		Bound  bool   `json:"bound"`
		OpenID string `json:"openid"`
		Debug  bool   `json:"debug"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp.Bound {
		t.Fatal("bound should be true")
	}
	if resp.OpenID != "" {
		t.Fatalf("external request must not receive bound OpenID, got %q", resp.OpenID)
	}
}

func TestBind_MissingOpenID_400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := &Server{auth: newAuthSvcForTest(t), cfg: &config.Config{}}
	g := gin.New()
	g.POST("/api/auth/bind", srv.auth.BindOpGateMiddleware(), srv.bind)
	body, _ := json.Marshal(map[string]string{"nickname": "no-id"})
	req, _ := http.NewRequest("POST", "/api/auth/bind", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing openid → 400, got %d", w.Code)
	}
}
