package larkreg

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSaveAndLoadCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lark_credentials.json")
	if err := SaveCredentials(path, "cli_xxx", "sec_yyy"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	id, sec, ok := LoadCredentials(path)
	if !ok {
		t.Fatal("Load returned ok=false after Save")
	}
	if id != "cli_xxx" {
		t.Fatalf("AppID = %q, want cli_xxx", id)
	}
	if sec != "sec_yyy" {
		t.Fatalf("AppSecret = %q, want sec_yyy", sec)
	}
}

func TestLoadCredentials_NoFile(t *testing.T) {
	_, _, ok := LoadCredentials(filepath.Join(t.TempDir(), "missing.json"))
	if ok {
		t.Fatal("missing file should return ok=false")
	}
}

func TestLoadCredentials_Corrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	_ = os.WriteFile(path, []byte("{not json"), 0600)
	_, _, ok := LoadCredentials(path)
	if ok {
		t.Fatal("corrupt file should return ok=false")
	}
}

func TestSaveCredentials_Permissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不落实 Unix 权限位（0600 → 0666），仅 Unix 上断言")
	}
	path := filepath.Join(t.TempDir(), "lark_credentials.json")
	_ = SaveCredentials(path, "x", "y")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("file mode = %o, want 0600", info.Mode().Perm())
	}
}

func TestSaveLoadConfig_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lark_credentials.json")
	in := ChannelConfig{
		AppID:       "cli_cfg",
		AppSecret:   "sec_cfg",
		VerifyToken: "vt_cfg",
		EncryptKey:  "ek_cfg",
		EventMode:   "webhook",
	}
	if err := SaveConfig(path, in); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	out, ok := LoadConfig(path)
	if !ok {
		t.Fatal("LoadConfig returned ok=false after SaveConfig")
	}
	if out != in {
		t.Fatalf("round-trip mismatch:\n in=%+v\nout=%+v", in, out)
	}
}

// TestSaveCredentials_MergesKeepsOtherFields 验证 SaveCredentials（兼容旧调用）
// 只覆盖 app 字段，不清空已存在的 verify_token/encrypt_key/event_mode。
// 防止扫码流程覆盖手工配置的 webhook 字段。
func TestSaveCredentials_MergesKeepsOtherFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lark_credentials.json")
	if err := SaveConfig(path, ChannelConfig{
		AppID: "cli_a", AppSecret: "sec_a",
		VerifyToken: "vt_a", EncryptKey: "ek_a", EventMode: "webhook",
	}); err != nil {
		t.Fatal(err)
	}
	// 扫码落盘：只写 app_id/app_secret
	if err := SaveCredentials(path, "cli_b", "sec_b"); err != nil {
		t.Fatal(err)
	}
	cfg, ok := LoadConfig(path)
	if !ok {
		t.Fatal("LoadConfig failed")
	}
	if cfg.AppID != "cli_b" || cfg.AppSecret != "sec_b" {
		t.Fatalf("app fields not updated: %+v", cfg)
	}
	if cfg.VerifyToken != "vt_a" || cfg.EncryptKey != "ek_a" || cfg.EventMode != "webhook" {
		t.Fatalf("other fields wiped by SaveCredentials: %+v", cfg)
	}
}
