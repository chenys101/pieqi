package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pieqi/internal/auth"
	"pieqi/internal/config"
	"pieqi/internal/larkreg"

	"github.com/gin-gonic/gin"
)

// TestLarkReg_Start_InternalOK 验证 /api/larkreg/start 仅内网可调,返回 QR URL。
// 用 fake runner 注入(避免真实扫码)。
func TestLarkReg_Start_InternalOK(t *testing.T) {
	srv := newLarkRegTestServer(t, &fakeReg{
		onRun: func(opts larkreg.Options) (larkreg.Result, error) {
			opts.OnQRCode("https://qr.test/abc", 300)
			return larkreg.Result{AppID: "cli_test", AppSecret: "sec_test"}, nil
		},
	})

	// 内网 IP
	req := httptest.NewRequest("POST", "/api/larkreg/start", strings.NewReader(`{}`))
	req.RemoteAddr = "192.168.1.1:1234"
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("internal start → %d, want 200. body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["qr_url"] != "https://qr.test/abc" {
		t.Fatalf("qr_url = %v", resp["qr_url"])
	}
}

// TestLarkReg_Start_ExternalBlocked 验证外网调 start 被 BindOpGate 拒绝(403)。
func TestLarkReg_Start_ExternalBlocked(t *testing.T) {
	srv := newLarkRegTestServer(t, &fakeReg{})

	req := httptest.NewRequest("POST", "/api/larkreg/start", strings.NewReader(`{}`))
	req.RemoteAddr = "8.8.8.8:1234"
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != 403 {
		t.Fatalf("external start → %d, want 403", w.Code)
	}
}

// TestLarkReg_Poll_ReturnsCredentialsAndPersists 验证 poll 在 device flow
// 完成后返回凭据并落盘(用 tmp 文件验证)。
func TestLarkReg_Poll_ReturnsCredentialsAndPersists(t *testing.T) {
	credPath := filepath.Join(t.TempDir(), "lark_credentials.json")
	srv := newLarkRegTestServerWithCredPath(t, credPath, &fakeReg{
		onRun: func(opts larkreg.Options) (larkreg.Result, error) {
			opts.OnQRCode("https://qr.test/abc", 300)
			// 模拟扫码完成
			time.Sleep(10 * time.Millisecond)
			return larkreg.Result{AppID: "cli_persist", AppSecret: "sec_persist"}, nil
		},
	})

	// 1. 启动 device flow
	req := httptest.NewRequest("POST", "/api/larkreg/start", strings.NewReader(`{}`))
	req.RemoteAddr = "192.168.1.1:1234"
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	// 2. 轮询直到拿到凭据(最多 2s)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		req := httptest.NewRequest("GET", "/api/larkreg/poll", nil)
		req.RemoteAddr = "192.168.1.1:1234"
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)
		if w.Code == 200 {
			var resp map[string]string
			_ = json.Unmarshal(w.Body.Bytes(), &resp)
			if resp["app_id"] == "cli_persist" {
				// 验证落盘
				data, err := os.ReadFile(credPath)
				if err != nil {
					t.Fatalf("credentials file not written: %v", err)
				}
				if !strings.Contains(string(data), "cli_persist") {
					t.Fatalf("credentials file content = %s", string(data))
				}
				return // 成功
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("poll did not return credentials within 2s")
}

// fakeReg 是 larkRegRunner 接口的 fake,绕开真实 SDK。
type fakeReg struct {
	onRun func(opts larkreg.Options) (larkreg.Result, error)
}

func (f *fakeReg) Run(ctx context.Context, opts larkreg.Options) (larkreg.Result, error) {
	if f.onRun != nil {
		return f.onRun(opts)
	}
	return larkreg.Result{}, nil
}

// newLarkRegTestServer 装一个最小可测的 Server + 路由,套上 BindOpGateMiddleware。
// 复用 PRD V1.0 的 auth 装配模式。
func newLarkRegTestServer(t *testing.T, reg *fakeReg) *larkRegTestEnv {
	return newLarkRegTestServerWithCredPath(t, filepath.Join(t.TempDir(), "lark_credentials.json"), reg)
}

func newLarkRegTestServerWithCredPath(t *testing.T, credPath string, reg *fakeReg) *larkRegTestEnv {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	cfg := &config.Config{}
	cfg.Channels.Lark.CredentialsFile = credPath

	bindings, _ := auth.NewBindingStore(filepath.Join(t.TempDir(), "b.json"))
	svc := &auth.Service{
		Debug:    auth.NewDebugSwitch(false),
		Bindings: bindings,
		Tokens:   auth.NewTokenStore(),
		Limiter:  auth.NewIPLimiter(5, 10*time.Minute),
	}
	srv := &Server{cfg: cfg, auth: svc}
	srv.SetLarkReg(reg, credPath)
	srv.Register(r)
	return &larkRegTestEnv{router: r, srv: srv}
}

type larkRegTestEnv struct {
	router *gin.Engine
	srv    *Server
}
