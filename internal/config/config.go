package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	Pieqi    PieqiConfig    `mapstructure:"pieqi"`
	Auth     AuthConfig     `mapstructure:"auth"`
	Agents   AgentsConfig   `mapstructure:"agents"`

	// Deprecations Load 时检测到的旧字段迁移提示（pieqi.acp.* → agents.*）。
	// 仅由 Load 填充，无 YAML 反序列化来源；main.go 据此逐条打告警日志。
	Deprecations []string `mapstructure:"-"`
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
	Enabled         bool   `mapstructure:"enabled"`
	AppID           string `mapstructure:"app_id"`
	AppSecret       string `mapstructure:"app_secret"`
	VerifyToken     string `mapstructure:"verify_token"`
	EncryptKey      string `mapstructure:"encrypt_key"`
	EventMode       string `mapstructure:"event_mode"`       // "webhook"(默认)| "longconn"
	CredentialsFile string `mapstructure:"credentials_file"` // 一键接入凭据落盘路径;空 = ~/.pieqi/lark_credentials.json
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
	HookTimeout             time.Duration `mapstructure:"hook_timeout"`               // hook 等决策上限，Phase 0 验证后定
	HookTools               []string      `mapstructure:"hook_tools"`                 // PreToolUse 拦截的工具名，默认 Bash/Write/Edit/NotebookEdit
	MaxConcurrentPerProject int           `mapstructure:"max_concurrent_per_project"` // 每项目并发上限，默认 4
	BaseBranch              string        `mapstructure:"base_branch"`                // worktree 基准分支，默认 "main"
	ACP                     ACPConfig     `mapstructure:"acp"`                        // ACP 协议配置（Phase 2 引入；use_acp=false 时走 Phase 1 PrintAgent 路径）
}

// ACPConfig ACP 协议（Agent Client Protocol）相关配置。
// use_acp=true 时 TaskRunner 的 agent 驱动职责交给 AgentAdapter（ACPAgent）；
// false（默认）保持 Phase 1 的 claude -p + stream-json 路径不变。
type ACPConfig struct {
	UseACP       bool          `mapstructure:"use_acp"`           // ACP 总开关；默认 false（M1 不切默认，避免影响 Phase 1）
	AgentType    string        `mapstructure:"agent_type"`        // claude-code / qodercli / codex ...；默认 "claude-code"
	SpawnCommand []string      `mapstructure:"acp_spawn_command"` // spawn 命令分词，如 [npx,-y,@agentclientprotocol/claude-agent-acp@latest]；空 = 按 agent_type 取默认
	InitTimeout  time.Duration `mapstructure:"init_timeout"`      // initialize/newSession 握手超时
	IdleTimeout  time.Duration `mapstructure:"idle_timeout"`      // ACP 会话空闲回收阈值：轮间保活，超过该时长无对话则优雅关闭（避免孤儿进程）；<=0 禁用回收
}

// AgentsConfig 多 Agent 配置（multi-agent.md §9，修订版 §9/§11 P5）。
// 与 pieqi.acp.* 旧字段并存：新增节、不改旧语义；旧字段迁移（agents.claude/qoder
// 接管 acp.use_acp 等，含弃用告警）在 P5 单独做。默认 transport 见下方 defaults。
type AgentsConfig struct {
	Claude AgentClaudeConfig `mapstructure:"claude"`
	Qoder  AgentQoderConfig  `mapstructure:"qoder"`
}

// AgentClaudeConfig claude agent 配置（multi-agent.md §4.2）。
// transport: "sdk-bridge"（默认）| "print"。sdk-bridge 时 bridge 为主力、print 为回退。
type AgentClaudeConfig struct {
	Transport string             `mapstructure:"transport"`
	Bridge    ClaudeBridgeConfig `mapstructure:"bridge"`
	Print     AgentPrintConfig   `mapstructure:"print"`
}

// ClaudeBridgeConfig claude-sdk-bridge 配置（multi-agent.md §5.4）。
type ClaudeBridgeConfig struct {
	BaseURL   string `mapstructure:"base_url"`   // 桥监听地址；默认 http://127.0.0.1:18790
	Token     string `mapstructure:"token"`      // 桥鉴权 token；空 = 不鉴权（仅本地私有端口）
	AutoStart bool   `mapstructure:"auto_start"` // 探活失败时自动 spawn node src/index.js；默认 true
	Dir       string `mapstructure:"dir"`        // 桥源码目录（含 src/index.js）；空 = 自动探测（exe/cwd 邻接 services/claude-sdk-bridge）
}

// AgentPrintConfig claude print 回退配置（multi-agent.md §7）。
type AgentPrintConfig struct {
	Command        string `mapstructure:"command"`
	PermissionMode string `mapstructure:"permission_mode"`
	Model          string `mapstructure:"model"`
	SysPrompt      string `mapstructure:"sys_prompt"`
}

// AgentQoderConfig qoder agent 配置（multi-agent.md §11 P4）。
// transport: "acp"（默认，qodercli --acp）。
type AgentQoderConfig struct {
	Transport string         `mapstructure:"transport"`
	ACP       QoderACPConfig `mapstructure:"acp"`
}

// QoderACPConfig qoder 的 ACP 配置（与 ACPConfig 字段对应，但 mapstructure 键不带 acp_ 前缀）。
type QoderACPConfig struct {
	AgentType    string        `mapstructure:"agent_type"`
	SpawnCommand []string      `mapstructure:"spawn_command"`
	InitTimeout  time.Duration `mapstructure:"init_timeout"`
	IdleTimeout  time.Duration `mapstructure:"idle_timeout"`
}

// ACPConfig 把 qoder 节转成旧 ACPConfig 结构（喂给 agent.ConfigureACPProviders / AgentManager）。
func (q AgentQoderConfig) ACPConfig() ACPConfig {
	return ACPConfig{
		AgentType:    q.ACP.AgentType,
		SpawnCommand: q.ACP.SpawnCommand,
		InitTimeout:  q.ACP.InitTimeout,
		IdleTimeout:  q.ACP.IdleTimeout,
	}
}

// AuthConfig 飞书身份绑定 + Cloudflared 隧道安全系统配置。
// 最高优先级是 DebugSkipAllAuth：true 时所有鉴权全部跳过（仅本地开发用）。
type AuthConfig struct {
	DebugSkipAllAuth  bool              `mapstructure:"debug_skip_all_auth"` // 默认 false；true 全量放行（仅开发）
	FeishuBindingFile string            `mapstructure:"feishu_binding_file"` // 绑定账号持久化路径
	Cloudflared       CloudflaredConfig `mapstructure:"cloudflared"`
	RateLimit         RateLimitConfig   `mapstructure:"ratelimit"`
}

// CloudflaredConfig Cloudflared 临时隧道配置。
type CloudflaredConfig struct {
	BinaryPath string        `mapstructure:"binary_path"` // cloudflared 可执行路径；默认 "cloudflared"（PATH 查找）
	DefaultTTL time.Duration `mapstructure:"default_ttl"` // 默认 15m；可选 15m/1h/4h
}

// RateLimitConfig 外网 Token 暴力破解限流。
type RateLimitConfig struct {
	MaxFailuresPerMin int           `mapstructure:"max_failures_per_min"` // 默认 5
	BlacklistDuration time.Duration `mapstructure:"blacklist_duration"`   // 默认 10m
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
	v.SetDefault("pieqi.acp.idle_timeout", "15m") // ACP 会话空闲回收阈值（轮间保活上限）
	v.SetDefault("agents.claude.transport", "sdk-bridge")
	v.SetDefault("agents.claude.bridge.base_url", "http://127.0.0.1:18790")
	v.SetDefault("agents.claude.bridge.auto_start", true)
	v.SetDefault("agents.claude.print.command", "claude")
	v.SetDefault("agents.claude.print.permission_mode", "bypassPermissions")
	v.SetDefault("agents.qoder.transport", "acp")
	v.SetDefault("agents.qoder.acp.agent_type", "qodercli")
	v.SetDefault("auth.debug_skip_all_auth", false)
	v.SetDefault("auth.feishu_binding_file", filepath.Join(DefaultDataRoot(), "feishu_binding.json"))
	v.SetDefault("auth.cloudflared.binary_path", "cloudflared")
	v.SetDefault("auth.cloudflared.default_ttl", "15m")
	v.SetDefault("auth.ratelimit.max_failures_per_min", 5)
	v.SetDefault("auth.ratelimit.blacklist_duration", "10m")
	v.SetDefault("channels.lark.event_mode", "webhook")
	v.SetDefault("channels.lark.credentials_file", filepath.Join(DefaultDataRoot(), "lark_credentials.json"))

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	// Normalize: an explicitly-empty feishu_binding_file (e.g. the
	// production-default config.yaml sets `feishu_binding_file: ""`) must
	// fall back to the default path. Viper's SetDefault is overridden by
	// any explicit value in the file, including the empty string, which
	// would otherwise make auth.NewBindingStore("") try to MkdirAll("").
	if cfg.Auth.FeishuBindingFile == "" {
		cfg.Auth.FeishuBindingFile = filepath.Join(DefaultDataRoot(), "feishu_binding.json")
	}

	// 空 credentials_file 回退默认路径(同 feishu_binding_file 模式)
	if cfg.Channels.Lark.CredentialsFile == "" {
		cfg.Channels.Lark.CredentialsFile = filepath.Join(DefaultDataRoot(), "lark_credentials.json")
	}

	// P5 迁移（multi-agent.md §9）：pieqi.acp.* → agents.*。
	// 旧字段仍被消费（TaskRunner ACP 路径 / AgentManager，main.go 直读），故只告警不删语义；
	// 真正的执行路径迁移（TaskRunner 切到 agent.Open）在 TaskRunner 接入阶段完成。
	//
	// 1) 显式配置的旧字段 → 弃用告警。
	//    注意用 v.InConfig（只认配置文件里的显式值）而非 v.IsSet——viper 把 SetDefault
	//    也算作"已设置"，IsSet 会把默认值误报为弃用。
	deprecatedACP := []string{
		"pieqi.acp.use_acp",
		"pieqi.acp.agent_type",
		"pieqi.acp.acp_spawn_command",
		"pieqi.acp.init_timeout",
		"pieqi.acp.idle_timeout",
	}
	for _, k := range deprecatedACP {
		if v.InConfig(k) {
			cfg.Deprecations = append(cfg.Deprecations,
				fmt.Sprintf("pieqi.acp.%s 已弃用，请迁移到 agents.claude / agents.qoder；当前旧语义仍生效",
					strings.TrimPrefix(k, "pieqi.acp.")))
		}
	}

	// 2) qoder 兼容回填：agents.qoder.acp 未显式配置（新节任一字段都不在配置文件里）
	//    且旧字段是 qoder 信号（agent_type=qodercli 或 acp_spawn_command 非空）时，
	//    从 pieqi.acp.* 回填，保证老配置迁到新节不丢 spawn 参数。
	newQoderACPSet := false
	for _, k := range []string{
		"agents.qoder.acp.agent_type",
		"agents.qoder.acp.spawn_command",
		"agents.qoder.acp.init_timeout",
		"agents.qoder.acp.idle_timeout",
	} {
		if v.InConfig(k) {
			newQoderACPSet = true
			break
		}
	}
	if !newQoderACPSet {
		legacy := cfg.Pieqi.ACP
		if legacy.AgentType == "qodercli" || len(legacy.SpawnCommand) > 0 {
			cfg.Agents.Qoder.ACP = QoderACPConfig{
				AgentType:    legacy.AgentType,
				SpawnCommand: legacy.SpawnCommand,
				InitTimeout:  legacy.InitTimeout,
				IdleTimeout:  legacy.IdleTimeout,
			}
			cfg.Deprecations = append(cfg.Deprecations,
				"pieqi.acp 的 qoder 配置已自动回填到 agents.qoder.acp（agent_type=qodercli / acp_spawn_command 非空）")
		}
	}

	return &cfg, nil
}
