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

// TestLarkReg_Status_Unregistered 验证未接入时返回 registered=false。
func TestLarkReg_Status_Unregistered(t *testing.T) {
	srv := newLarkRegTestServer(t, &fakeReg{}) // credPath 指向不存在的文件

	req := httptest.NewRequest("GET", "/api/larkreg/status", nil)
	req.RemoteAddr = "192.168.1.1:1234"
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status → %d, want 200. body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Registered bool   `json:"registered"`
		AppID      string `json:"app_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Registered {
		t.Fatalf("unregistered server should report registered=false, got %+v", resp)
	}
	if resp.AppID != "" {
		t.Fatalf("unregistered server should have empty app_id, got %q", resp.AppID)
	}
}

// TestLarkReg_Status_Registered 验证已落盘凭据时返回 registered=true + app_id。
func TestLarkReg_Status_Registered(t *testing.T) {
	credPath := filepath.Join(t.TempDir(), "lark_credentials.json")
	if err := larkreg.SaveCredentials(credPath, "cli_registered", "sec_secret"); err != nil {
		t.Fatalf("save credentials: %v", err)
	}
	srv := newLarkRegTestServerWithCredPath(t, credPath, &fakeReg{})

	req := httptest.NewRequest("GET", "/api/larkreg/status", nil)
	req.RemoteAddr = "192.168.1.1:1234"
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	var resp struct {
		Registered bool   `json:"registered"`
		AppID      string `json:"app_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp.Registered {
		t.Fatalf("registered server should report registered=true, got %+v", resp)
	}
	if resp.AppID != "cli_registered" {
		t.Fatalf("app_id = %q, want cli_registered", resp.AppID)
	}
	if strings.Contains(w.Body.String(), "sec_secret") {
		t.Fatal("status must never leak app_secret")
	}
}

// TestLarkReg_Config_GET_Masked 验证 GET /api/larkreg/config 返回脱敏视图，
// 绝不泄漏 app_secret/verify_token/encrypt_key。
func TestLarkReg_Config_GET_Masked(t *testing.T) {
	credPath := filepath.Join(t.TempDir(), "lark_credentials.json")
	if err := larkreg.SaveConfig(credPath, larkreg.ChannelConfig{
		AppID: "cli_cfg", AppSecret: "sec_cfg",
		VerifyToken: "vt_cfg", EncryptKey: "ek_cfg", EventMode: "webhook",
	}); err != nil {
		t.Fatal(err)
	}
	srv := newLarkRegTestServerWithCredPath(t, credPath, &fakeReg{})

	req := httptest.NewRequest("GET", "/api/larkreg/config", nil)
	req.RemoteAddr = "192.168.1.1:1234"
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("config → %d, want 200. body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		AppID          string `json:"app_id"`
		EventMode      string `json:"event_mode"`
		Registered     bool   `json:"registered"`
		VerifyTokenSet bool   `json:"verify_token_set"`
		EncryptKeySet  bool   `json:"encrypt_key_set"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.AppID != "cli_cfg" {
		t.Fatalf("app_id = %q, want cli_cfg", resp.AppID)
	}
	if resp.EventMode != "webhook" {
		t.Fatalf("event_mode = %q, want webhook", resp.EventMode)
	}
	if !resp.Registered || !resp.VerifyTokenSet || !resp.EncryptKeySet {
		t.Fatalf("masked view incomplete: %+v", resp)
	}
	body := w.Body.String()
	for _, secret := range []string{"sec_cfg", "vt_cfg", "ek_cfg"} {
		if strings.Contains(body, secret) {
			t.Fatalf("config must never leak %q, body=%s", secret, body)
		}
	}
}

// TestLarkReg_Config_POST_SavesAndApplies 验证 POST /api/larkreg/config
// 落盘 + 调用 applier，返回 applied + restart_required。
func TestLarkReg_Config_POST_SavesAndApplies(t *testing.T) {
	credPath := filepath.Join(t.TempDir(), "lark_credentials.json")
	srv := newLarkRegTestServerWithCredPath(t, credPath, &fakeReg{})

	var applied larkreg.ChannelConfig
	srv.srv.SetLarkConfigApplier(func(cfg larkreg.ChannelConfig) (bool, error) {
		applied = cfg
		return false, nil
	})

	body := `{"app_id":"cli_manual","app_secret":"sec_manual","event_mode":"longconn"}`
	req := httptest.NewRequest("POST", "/api/larkreg/config", strings.NewReader(body))
	req.RemoteAddr = "192.168.1.1:1234"
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("config POST → %d, want 200. body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Applied         bool   `json:"applied"`
		RestartRequired bool   `json:"restart_required"`
		Message         string `json:"message"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp.Applied || resp.RestartRequired {
		t.Fatalf("resp = %+v, want applied=true restart_required=false", resp)
	}
	if applied.AppID != "cli_manual" || applied.AppSecret != "sec_manual" {
		t.Fatalf("applier got %+v, want cli_manual/sec_manual", applied)
	}
	// 落盘验证
	onDisk, ok := larkreg.LoadConfig(credPath)
	if !ok || onDisk.AppID != "cli_manual" || onDisk.EventMode != "longconn" {
		t.Fatalf("on-disk config = %+v (ok=%v)", onDisk, ok)
	}
}

// TestLarkReg_Config_POST_MergeKeepsBlankSecret 验证合并语义：空字段回退
// 保留现有已存值，避免每次重输 secret。
func TestLarkReg_Config_POST_MergeKeepsBlankSecret(t *testing.T) {
	credPath := filepath.Join(t.TempDir(), "lark_credentials.json")
	if err := larkreg.SaveConfig(credPath, larkreg.ChannelConfig{
		AppID: "cli_old", AppSecret: "sec_old",
		VerifyToken: "vt_old", EncryptKey: "ek_old", EventMode: "webhook",
	}); err != nil {
		t.Fatal(err)
	}
	srv := newLarkRegTestServerWithCredPath(t, credPath, &fakeReg{})
	srv.srv.SetLarkConfigApplier(func(larkreg.ChannelConfig) (bool, error) { return false, nil })

	// 只更新 app_id，secret/其他字段留空
	body := `{"app_id":"cli_new","event_mode":""}`
	req := httptest.NewRequest("POST", "/api/larkreg/config", strings.NewReader(body))
	req.RemoteAddr = "192.168.1.1:1234"
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("config POST → %d, want 200. body=%s", w.Code, w.Body.String())
	}
	onDisk, ok := larkreg.LoadConfig(credPath)
	if !ok {
		t.Fatal("LoadConfig failed")
	}
	if onDisk.AppID != "cli_new" {
		t.Fatalf("app_id = %q, want cli_new", onDisk.AppID)
	}
	if onDisk.AppSecret != "sec_old" {
		t.Fatalf("secret wiped: %q, want sec_old (merge semantics)", onDisk.AppSecret)
	}
	if onDisk.VerifyToken != "vt_old" || onDisk.EncryptKey != "ek_old" || onDisk.EventMode != "webhook" {
		t.Fatalf("other fields not preserved: %+v", onDisk)
	}
}

// TestLarkReg_Config_POST_RequiresAppID 验证缺少 app_id 返回 400。
func TestLarkReg_Config_POST_RequiresAppID(t *testing.T) {
	srv := newLarkRegTestServer(t, &fakeReg{})
	req := httptest.NewRequest("POST", "/api/larkreg/config",
		strings.NewReader(`{"app_secret":"sec_only"}`))
	req.RemoteAddr = "192.168.1.1:1234"
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("missing app_id → %d, want 400. body=%s", w.Code, w.Body.String())
	}
}

// TestLarkReg_Config_POST_ExternalBlocked 验证外网调 POST config 被拒绝(403)。
func TestLarkReg_Config_POST_ExternalBlocked(t *testing.T) {
	srv := newLarkRegTestServer(t, &fakeReg{})
	req := httptest.NewRequest("POST", "/api/larkreg/config",
		strings.NewReader(`{"app_id":"x","app_secret":"y"}`))
	req.RemoteAddr = "8.8.8.8:1234"
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("external config POST → %d, want 403", w.Code)
	}
}

// TestLarkReg_Poll_HotApplies 验证扫码成功且接线 applier 时热应用并返回
// "已生效"提示（而非"重启生效"）。
func TestLarkReg_Poll_HotApplies(t *testing.T) {
	credPath := filepath.Join(t.TempDir(), "lark_credentials.json")
	srv := newLarkRegTestServerWithCredPath(t, credPath, &fakeReg{
		onRun: func(opts larkreg.Options) (larkreg.Result, error) {
			opts.OnQRCode("https://qr.test/abc", 300)
			time.Sleep(10 * time.Millisecond)
			return larkreg.Result{AppID: "cli_hot", AppSecret: "sec_hot"}, nil
		},
	})
	var applied larkreg.ChannelConfig
	srv.srv.SetLarkConfigApplier(func(cfg larkreg.ChannelConfig) (bool, error) {
		applied = cfg
		return false, nil
	})

	// 启动 device flow
	req := httptest.NewRequest("POST", "/api/larkreg/start", strings.NewReader(`{}`))
	req.RemoteAddr = "192.168.1.1:1234"
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	// 轮询直到成功
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		req := httptest.NewRequest("GET", "/api/larkreg/poll", nil)
		req.RemoteAddr = "192.168.1.1:1234"
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)
		if w.Code == 200 {
			var resp map[string]string
			_ = json.Unmarshal(w.Body.Bytes(), &resp)
			if resp["app_id"] == "cli_hot" {
				if resp["hint"] != "已生效" {
					t.Fatalf("hint = %q, want 已生效 (hot applied)", resp["hint"])
				}
				if applied.AppID != "cli_hot" {
					t.Fatalf("applier not called with cli_hot, got %+v", applied)
				}
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("poll did not return credentials within 2s")
}
