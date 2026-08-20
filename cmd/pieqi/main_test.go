package main

import (
	"os"
	"path/filepath"
	"testing"

	"pieqi/internal/config"
)

// TestLoadLarkChannelConfig_OverridesConfig 验证凭据配置文件存在时，
// 其 app_id/app_secret/verify_token/encrypt_key/event_mode 覆盖 config 默认值。
// 这是扫码一键接入或手工配置落盘后的接管路径：保存 → 下次启动自动加载。
func TestLoadLarkChannelConfig_OverridesConfig(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "lark_credentials.json")
	credBody := `{
		"app_id":"cli_from_scan",
		"app_secret":"sec_from_scan",
		"verify_token":"vt_from_scan",
		"encrypt_key":"ek_from_scan",
		"event_mode":"webhook"
	}`
	if err := os.WriteFile(credPath, []byte(credBody), 0600); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	cfg.Channels.Lark.AppID = "from_yaml"
	cfg.Channels.Lark.AppSecret = "yaml_secret"
	cfg.Channels.Lark.VerifyToken = "yaml_vt"
	cfg.Channels.Lark.EncryptKey = "yaml_ek"
	cfg.Channels.Lark.EventMode = "longconn"
	cfg.Channels.Lark.CredentialsFile = credPath

	loadLarkChannelConfig(cfg)

	if cfg.Channels.Lark.AppID != "cli_from_scan" {
		t.Fatalf("AppID = %q, want cli_from_scan", cfg.Channels.Lark.AppID)
	}
	if cfg.Channels.Lark.AppSecret != "sec_from_scan" {
		t.Fatalf("AppSecret = %q, want sec_from_scan", cfg.Channels.Lark.AppSecret)
	}
	if cfg.Channels.Lark.VerifyToken != "vt_from_scan" {
		t.Fatalf("VerifyToken = %q, want vt_from_scan", cfg.Channels.Lark.VerifyToken)
	}
	if cfg.Channels.Lark.EncryptKey != "ek_from_scan" {
		t.Fatalf("EncryptKey = %q, want ek_from_scan", cfg.Channels.Lark.EncryptKey)
	}
	if cfg.Channels.Lark.EventMode != "webhook" {
		t.Fatalf("EventMode = %q, want webhook", cfg.Channels.Lark.EventMode)
	}
}

// TestLoadLarkChannelConfig_NoFileIsNoop 验证凭据文件不存在时无副作用。
func TestLoadLarkChannelConfig_NoFileIsNoop(t *testing.T) {
	cfg := &config.Config{}
	cfg.Channels.Lark.AppID = "from_yaml"
	cfg.Channels.Lark.CredentialsFile = filepath.Join(t.TempDir(), "missing.json")

	loadLarkChannelConfig(cfg)

	if cfg.Channels.Lark.AppID != "from_yaml" {
		t.Fatalf("AppID changed unexpectedly: %q", cfg.Channels.Lark.AppID)
	}
}

// TestLoadLarkChannelConfig_CorruptFileIsNoop 验证损坏文件不阻断启动。
func TestLoadLarkChannelConfig_CorruptFileIsNoop(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "lark_credentials.json")
	_ = os.WriteFile(credPath, []byte("{not json"), 0600)

	cfg := &config.Config{}
	cfg.Channels.Lark.AppID = "from_yaml"
	cfg.Channels.Lark.CredentialsFile = credPath

	loadLarkChannelConfig(cfg)

	if cfg.Channels.Lark.AppID != "from_yaml" {
		t.Fatalf("AppID changed on corrupt file: %q", cfg.Channels.Lark.AppID)
	}
}
