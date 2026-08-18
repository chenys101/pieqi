package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/viper"
)

// DefaultDataRoot 返回运行时数据根目录：$PIEQI_HOME 优先，否则 ~/.pieqi。
// tasks/worktrees 等运行时数据统一存这里，不入仓库。取不到 home 时退回 "."。
func DefaultDataRoot() string {
	if h := os.Getenv("PIEQI_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".pieqi")
}

// Config 全局配置
type Config struct {
	Server   ServerConfig   `mapstructure:"server"`
	Channels ChannelsConfig `mapstructure:"channels"`
	API      APIConfig      `mapstructure:"api"`
	Pieqi      PieqiConfig      `mapstructure:"pieqi"`
	Auth       AuthConfig       `mapstructure:"auth"`
}

// ServerConfig HTTP 服务配置
type ServerConfig struct {
	Port int    `mapstructure:"port"`
	Mode string `mapstructure:"mode"`
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

// PieqiConfig Pieqi 后端总开关与行为参数
type PieqiConfig struct {
	Enabled                 bool          `mapstructure:"enabled"`
	WorktreeBase            string        `mapstructure:"worktree_base"`   // worktree 根目录
	SkillsDirs              []string      `mapstructure:"skills_dirs"`     // 空 = 默认 ~/.claude/skills
	PermissionMode          string        `mapstructure:"permission_mode"` // 默认 "bypassPermissions"，hook 真正拦截
	CleanupWorktrees        bool          `mapstructure:"cleanup_worktrees"`
	HookTimeout             time.Duration `mapstructure:"hook_timeout"`   // hook 等决策上限，Phase 0 验证后定
	HookTools               []string      `mapstructure:"hook_tools"`     // PreToolUse 拦截的工具名，默认 Bash/Write/Edit/NotebookEdit
	MaxConcurrentPerProject int           `mapstructure:"max_concurrent_per_project"` // 每项目并发上限，默认 4
	BaseBranch              string        `mapstructure:"base_branch"`               // worktree 基准分支，默认 "main"
	ACP                     ACPConfig     `mapstructure:"acp"`                       // ACP 协议配置（Phase 2 引入；use_acp=false 时走 Phase 1 PrintAgent 路径）
}

// ACPConfig ACP 协议（Agent Client Protocol）相关配置。
// use_acp=true 时 TaskRunner 的 agent 驱动职责交给 AgentAdapter（ACPAgent）；
// false（默认）保持 Phase 1 的 claude -p + stream-json 路径不变。
type ACPConfig struct {
	UseACP       bool          `mapstructure:"use_acp"`        // ACP 总开关；默认 false（M1 不切默认，避免影响 Phase 1）
	AgentType    string        `mapstructure:"agent_type"`     // claude-code / qodercli / codex ...；默认 "claude-code"
	SpawnCommand []string      `mapstructure:"acp_spawn_command"` // spawn 命令分词，如 [npx,-y,@agentclientprotocol/claude-agent-acp@latest]；空 = 按 agent_type 取默认
	InitTimeout  time.Duration `mapstructure:"init_timeout"`   // initialize/newSession 握手超时
}

// AuthConfig 飞书身份绑定 + Cloudflared 隧道安全系统配置。
// 最高优先级是 DebugSkipAllAuth：true 时所有鉴权全部跳过（仅本地开发用）。
type AuthConfig struct {
	DebugSkipAllAuth    bool              `mapstructure:"debug_skip_all_auth"`     // 默认 false；true 全量放行（仅开发）
	FeishuBindingFile   string            `mapstructure:"feishu_binding_file"`    // 绑定账号持久化路径
	Cloudflared         CloudflaredConfig `mapstructure:"cloudflared"`
	RateLimit           RateLimitConfig   `mapstructure:"ratelimit"`
}

// CloudflaredConfig Cloudflared 临时隧道配置。
type CloudflaredConfig struct {
	BinaryPath string        `mapstructure:"binary_path"`  // cloudflared 可执行路径；默认 "cloudflared"（PATH 查找）
	DefaultTTL time.Duration `mapstructure:"default_ttl"`  // 默认 15m；可选 15m/1h/4h
}

// RateLimitConfig 外网 Token 暴力破解限流。
type RateLimitConfig struct {
	MaxFailuresPerMin int           `mapstructure:"max_failures_per_min"`  // 默认 5
	BlacklistDuration time.Duration `mapstructure:"blacklist_duration"`    // 默认 10m
}

// Load 从文件和环境变量加载配置
func Load(configPath string) (*Config, error) {
	v := viper.New()

	v.SetConfigFile(configPath)
	v.SetConfigType("yaml")

	// 环境变量覆盖（如 PIEQI_SERVER_PORT=3000）
	v.SetEnvPrefix("PIEQI")
	v.AutomaticEnv()

	// 默认值
	v.SetDefault("server.port", 3000)
	v.SetDefault("server.mode", "debug")
	v.SetDefault("api.enabled", true)
	v.SetDefault("pieqi.enabled", false)
	v.SetDefault("pieqi.permission_mode", "bypassPermissions")
	v.SetDefault("pieqi.cleanup_worktrees", true)
	v.SetDefault("pieqi.hook_timeout", "30m")
	v.SetDefault("pieqi.hook_tools", []string{"Bash", "Write", "Edit", "NotebookEdit"})
	v.SetDefault("pieqi.max_concurrent_per_project", 4)
	v.SetDefault("pieqi.worktree_base", "") // 空 = DefaultDataRoot()/worktrees（main 侧解析）
	v.SetDefault("pieqi.base_branch", "main")
	v.SetDefault("pieqi.acp.use_acp", false)
	v.SetDefault("pieqi.acp.agent_type", "claude-code")
	v.SetDefault("pieqi.acp.init_timeout", "30s")
	v.SetDefault("auth.debug_skip_all_auth", false)
	v.SetDefault("auth.feishu_binding_file", filepath.Join(DefaultDataRoot(), "feishu_binding.json"))
	v.SetDefault("auth.cloudflared.binary_path", "cloudflared")
	v.SetDefault("auth.cloudflared.default_ttl", "15m")
	v.SetDefault("auth.ratelimit.max_failures_per_min", 5)
	v.SetDefault("auth.ratelimit.blacklist_duration", "10m")

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	return &cfg, nil
}
