// Package larkreg 封装"扫码一键创建飞书应用"流程,基于 OAuth 2.0
// Device Authorization Grant (RFC 8628)。底层用飞书官方 Go SDK 的
// registration.RegisterApp(见 registration.go)。
//
// 配置(app_id/app_secret/verify_token/encrypt_key/event_mode)落盘到
// ~/.pieqi/lark_credentials.json,main.go 启动时优先加载该文件覆盖
// config.yaml 的默认值。手工配置与扫码一键创建都写这个文件。
package larkreg

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ChannelConfig 飞书渠道的运行时配置（凭据 + 接入方式）。
// 既可由 config.yaml 提供，也可落盘到 credentials 文件覆盖。
// app_secret 等敏感字段绝不随 API 响应外泄，仅用于落盘/加载。
type ChannelConfig struct {
	AppID       string `json:"app_id"`
	AppSecret   string `json:"app_secret"`
	VerifyToken string `json:"verify_token"`
	EncryptKey  string `json:"encrypt_key"`
	EventMode   string `json:"event_mode"` // "longconn"(默认) | "webhook"
}

// SaveConfig 原子写入配置文件(0600 权限,先写 .tmp 再 rename)。
// 与 pieqi/internal/auth/binding.go 的 persistUnlocked 同模式。
func SaveConfig(path string, cfg ChannelConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write config tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename config: %w", err)
	}
	return nil
}

// LoadConfig 读取配置文件。文件不存在或损坏时返回 ok=false
// (不返回 error,因为这是合法的"未接入过"状态)。
func LoadConfig(path string) (cfg ChannelConfig, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ChannelConfig{}, false
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ChannelConfig{}, false
	}
	if cfg.AppID == "" || cfg.AppSecret == "" {
		return ChannelConfig{}, false
	}
	return cfg, true
}

// SaveCredentials 保存 app_id/app_secret（兼容旧调用方，如扫码落盘）。
// 采用"读旧档合并"：先加载现有配置，再覆盖 app 字段，避免清空
// 已配置的 verify_token/encrypt_key/event_mode。
func SaveCredentials(path, appID, appSecret string) error {
	cfg, _ := LoadConfig(path) // 文件不存在/损坏 = 全新配置
	cfg.AppID = appID
	cfg.AppSecret = appSecret
	return SaveConfig(path, cfg)
}

// LoadCredentials 读取 app_id/app_secret（兼容旧调用方）。
func LoadCredentials(path string) (appID, appSecret string, ok bool) {
	cfg, ok := LoadConfig(path)
	if !ok {
		return "", "", false
	}
	return cfg.AppID, cfg.AppSecret, true
}
