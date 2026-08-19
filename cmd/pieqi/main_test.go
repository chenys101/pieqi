package main

import (
	"os"
	"path/filepath"
	"testing"

	"pieqi/internal/config"
)

// TestLoadLarkCredentials_OverridesConfig 验证 ~/.pieqi/lark_credentials.json
// 存在时,其 app_id/app_secret 覆盖 config 里的默认值。这是 Device Flow
// 一键接入后的接管路径:扫码拿到凭据落盘 → 下次启动自动加载。
func TestLoadLarkCredentials_OverridesConfig(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "lark_credentials.json")
	credBody := `{"app_id":"cli_from_scan","app_secret":"sec_from_scan"}`
	if err := os.WriteFile(credPath, []byte(credBody), 0600); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	cfg.Channels.Lark.AppID = "from_yaml"
	cfg.Channels.Lark.AppSecret = "yaml_secret"
	cfg.Channels.Lark.CredentialsFile = credPath

	if err := loadLarkCredentials(cfg); err != nil {
		t.Fatalf("loadLarkCredentials: %v", err)
	}
	if cfg.Channels.Lark.AppID != "cli_from_scan" {
		t.Fatalf("AppID = %q, want cli_from_scan", cfg.Channels.Lark.AppID)
	}
	if cfg.Channels.Lark.AppSecret != "sec_from_scan" {
		t.Fatalf("AppSecret = %q, want sec_from_scan", cfg.Channels.Lark.AppSecret)
	}
}

// TestLoadLarkCredentials_NoFileIsNoop 验证凭据文件不存在时无副作用。
func TestLoadLarkCredentials_NoFileIsNoop(t *testing.T) {
	cfg := &config.Config{}
	cfg.Channels.Lark.AppID = "from_yaml"
	cfg.Channels.Lark.CredentialsFile = filepath.Join(t.TempDir(), "missing.json")
	if err := loadLarkCredentials(cfg); err != nil {
		t.Fatalf("missing file should be noop, got: %v", err)
	}
	if cfg.Channels.Lark.AppID != "from_yaml" {
		t.Fatalf("AppID changed unexpectedly: %q", cfg.Channels.Lark.AppID)
	}
}

// TestLoadLarkCredentials_CorruptFileIsNoop 验证损坏文件不阻断启动。
func TestLoadLarkCredentials_CorruptFileIsNoop(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "lark_credentials.json")
	_ = os.WriteFile(credPath, []byte("{not json"), 0600)

	cfg := &config.Config{}
	cfg.Channels.Lark.AppID = "from_yaml"
	cfg.Channels.Lark.CredentialsFile = credPath

	if err := loadLarkCredentials(cfg); err != nil {
		t.Fatalf("corrupt file should be noop, got: %v", err)
	}
	if cfg.Channels.Lark.AppID != "from_yaml" {
		t.Fatalf("AppID changed on corrupt file: %q", cfg.Channels.Lark.AppID)
	}
}
