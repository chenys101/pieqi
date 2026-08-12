package config

import (
	"fmt"
	"time"

	"github.com/spf13/viper"
)

// Config 全局配置
type Config struct {
	Server   ServerConfig   `mapstructure:"server"`
	Claude   ClaudeConfig   `mapstructure:"claude"`
	Session  SessionConfig  `mapstructure:"session"`
	Channels ChannelsConfig `mapstructure:"channels"`
	API      APIConfig      `mapstructure:"api"`
	Din      DinConfig      `mapstructure:"din"`
}

// ServerConfig HTTP 服务配置
type ServerConfig struct {
	Port int    `mapstructure:"port"`
	Mode string `mapstructure:"mode"`
}

// ClaudeConfig Claude 子进程配置
type ClaudeConfig struct {
	WorkDir string        `mapstructure:"work_dir"`
	Model   string        `mapstructure:"model"`
	Effort  string        `mapstructure:"effort"`
	Timeout time.Duration `mapstructure:"timeout"`
}

// SessionConfig 会话配置
type SessionConfig struct {
	TTL     time.Duration `mapstructure:"ttl"`
	DataDir string        `mapstructure:"data_dir"`
}

// ChannelsConfig 渠道开关
type ChannelsConfig struct {
	Lark   LarkConfig   `mapstructure:"lark"`
	WeCom  WeComConfig  `mapstructure:"wecom"`
	WeChat WeChatConfig `mapstructure:"wechat"`
}

// LarkConfig 飞书配置
type LarkConfig struct {
	Enabled     bool   `mapstructure:"enabled"`
	AppID       string `mapstructure:"app_id"`
	AppSecret   string `mapstructure:"app_secret"`
	VerifyToken string `mapstructure:"verify_token"`
	EncryptKey  string `mapstructure:"encrypt_key"`
}

// WeComConfig 企业微信配置
type WeComConfig struct {
	Enabled bool `mapstructure:"enabled"`
}

// WeChatConfig 个人微信配置（iLink）
type WeChatConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	BaseURL string `mapstructure:"base_url"`
}

// APIConfig 监控/干预 HTTP API（PWA + CLI/Electron 入口）
type APIConfig struct {
	Enabled     bool     `mapstructure:"enabled"`
	Token       string   `mapstructure:"token"` // 空 = 不鉴权（仅本地）
	CORSOrigins []string `mapstructure:"cors_origins"`
}

// DinConfig Din Agent 后端总开关与行为参数
type DinConfig struct {
	Enabled             bool          `mapstructure:"enabled"`
	WorktreeBase        string        `mapstructure:"worktree_base"`   // worktree 根目录
	SkillsDirs          []string      `mapstructure:"skills_dirs"`     // 空 = 默认 ~/.claude/skills
	PermissionMode      string        `mapstructure:"permission_mode"` // 默认 "bypassPermissions"，hook 真正拦截
	CleanupWorktrees    bool          `mapstructure:"cleanup_worktrees"`
	LegacyIMPath        bool          `mapstructure:"legacy_im_path"` // 迁移期保留旧 ApprovalGate 路径
	HookTimeout         time.Duration `mapstructure:"hook_timeout"`   // hook 等决策上限，Phase 0 验证后定
	HookTools           []string      `mapstructure:"hook_tools"`     // PreToolUse 拦截的工具名，默认 Bash/Write/Edit/NotebookEdit
	MaxConcurrentPerProject int       `mapstructure:"max_concurrent_per_project"` // 每项目并发上限，默认 4
	BaseBranch          string        `mapstructure:"base_branch"`    // worktree 基准分支，默认 "main"
}

// Load 从文件和环境变量加载配置
func Load(configPath string) (*Config, error) {
	v := viper.New()

	v.SetConfigFile(configPath)
	v.SetConfigType("yaml")

	// 环境变量覆盖（如 BRIDGE_SERVER_PORT=3000）
	v.SetEnvPrefix("BRIDGE")
	v.AutomaticEnv()

	// 默认值
	v.SetDefault("server.port", 3000)
	v.SetDefault("server.mode", "debug")
	v.SetDefault("claude.model", "sonnet")
	v.SetDefault("claude.effort", "high")
	v.SetDefault("claude.timeout", "300s")
	v.SetDefault("session.ttl", "30m")
	v.SetDefault("session.data_dir", "./data/sessions")
	v.SetDefault("api.enabled", true)
	v.SetDefault("din.enabled", false)
	v.SetDefault("din.permission_mode", "bypassPermissions")
	v.SetDefault("din.cleanup_worktrees", true)
	v.SetDefault("din.legacy_im_path", false)
	v.SetDefault("din.hook_timeout", "30m")
	v.SetDefault("din.hook_tools", []string{"Bash", "Write", "Edit", "NotebookEdit"})
	v.SetDefault("din.max_concurrent_per_project", 4)
	v.SetDefault("din.worktree_base", "./data/worktrees")
	v.SetDefault("din.base_branch", "main")

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	return &cfg, nil
}
