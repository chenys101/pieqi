// Package larkreg 封装"扫码一键创建飞书应用"流程,基于 OAuth 2.0
// Device Authorization Grant (RFC 8628)。底层用飞书官方 Go SDK 的
// registration.RegisterApp(见 registration.go)。
//
// 凭据(app_id/app_secret)落盘到 ~/.pieqi/lark_credentials.json,
// main.go 启动时优先加载该文件覆盖 config.yaml 的默认值。
package larkreg

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// credentialsFile 落盘 JSON 的结构。
type credentialsFile struct {
	AppID     string `json:"app_id"`
	AppSecret string `json:"app_secret"`
}

// SaveCredentials 原子写入凭据文件(0600 权限,先写 .tmp 再 rename)。
// 与 pieqi/internal/auth/binding.go 的 persistUnlocked 同模式。
func SaveCredentials(path, appID, appSecret string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("mkdir credentials dir: %w", err)
	}
	data, err := json.MarshalIndent(credentialsFile{AppID: appID, AppSecret: appSecret}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal credentials: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write credentials tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename credentials: %w", err)
	}
	return nil
}

// LoadCredentials 读取凭据文件。文件不存在或损坏时返回 ok=false
// (不返回 error,因为这是合法的"未接入过"状态)。
func LoadCredentials(path string) (appID, appSecret string, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", false
	}
	var c credentialsFile
	if err := json.Unmarshal(data, &c); err != nil {
		return "", "", false
	}
	if c.AppID == "" || c.AppSecret == "" {
		return "", "", false
	}
	return c.AppID, c.AppSecret, true
}
