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
	BotsDir                 string        `mapstructure:"bots_dir"`                   // IM 机器人绑定记录目录；空 = ~/.pieqi/bots
	// SelfUpdateDir 是自重启的**交付落点**：agent 把编译好的新二进制放到
	// <dir>/pieqi.new[.exe]，再调 POST /api/admin/restart，服务会用它替换自身并重启。
	//
	// 必须是 **agent 进程写得到的目录**（通常就是项目工作区）。不要把服务自己的
	// 运行目录（~/.pieqi/bin）填进来：那正是 agent 因沙箱写不到的地方，填了就白搭。
	// 空 = 关闭该功能（端点返回 409 并提示未配置）。
	SelfUpdateDir string `mapstructure:"self_update_dir"`
	ACP           ACPConfig `mapstructure:"acp"` // ACP 协议配置（Phase 2 引入；use_acp=false 时走 Phase 1 PrintAgent 路径）
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
	Dsh    AgentDshConfig    `mapstructure:"dsh"`
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
	AgentType string `mapstructure:"agent_type"`
	// SpawnCommand spawn 命令分词；空 = 按 AgentType 取默认（裸名 "qodercli"）。
	//
	// 首元素的解析顺序（见 agent.resolveSpawnName）：显式路径原样使用 →
	// exec.LookPath（系统 PATH）→ 各 CLI 常见安装落点回退
	// （qodercli：~/.qoder/bin/qodercli/<name>、$QODER_HOME/bin/...）。
	//
	// 之所以要回退：Windows 的 PATH 是**进程启动那一刻的环境快照**，安装器只写
	// 用户级 PATH，而宿主（IDE/计划任务/常驻服务）常早于安装启动 → 服务进程继承
	// 不到，裸名直接 `executable file not found in %PATH%`（症状：claude 正常、
	// 只有 qoder 全挂）。故**裸名即可**，无需在配置里写死机器相关的绝对路径；
	// 确实要写死时也支持（显式路径不会被改写）。
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

// AgentDshConfig dsh（DeepSeek Harness）agent 配置。
// transport: "acp"（dsh --profile acp）。**默认留空 = 不注册**：它要求本机装好 dsh 并在
// 启动环境提供 DEEPSEEK_API_KEY，缺任一项都只会让任务失败，不如不進选择器。
type AgentDshConfig struct {
	Transport string       `mapstructure:"transport"`
	ACP       DshACPConfig `mapstructure:"acp"`
}

// DshACPConfig dsh 的 ACP 配置（字段与 QoderACPConfig 对应）。
// 模型不在这里配：dsh 的模型由 ACP 的 session/set_config_option 在会话内选，
// CLI 层没有 --model 之类的位置（spawn_command 里塞 `-m` 对它无效）。
type DshACPConfig struct {
	AgentType    string   `mapstructure:"agent_type"`
	SpawnCommand []string `mapstructure:"spawn_command"`
	// InitTimeout 覆盖 initialize/newSession 超时。dsh 首次启动要组装配方、挂载
	// 插件（本机 Windows 实测冷启动 >90s，热启动 1s 内回 initialize），30s 默认会
	// 把第一次会话直接判死，故 defaults 放宽到 2m。
	InitTimeout time.Duration `mapstructure:"init_timeout"`
	IdleTimeout time.Duration `mapstructure:"idle_timeout"`
}

// ACPConfig 把 dsh 节转成旧 ACPConfig 结构（喂给 agent.ConfigureACPProviders）。
func (d AgentDshConfig) ACPConfig() ACPConfig {
	return ACPConfig{
		AgentType:    d.ACP.AgentType,
		SpawnCommand: d.ACP.SpawnCommand,
		InitTimeout:  d.ACP.InitTimeout,
		IdleTimeout:  d.ACP.IdleTimeout,
	}
}

// AuthConfig 飞书身份绑定 + Cloudflared 隧道安全系统配置。
// 最高优先级是 DebugSkipAllAuth：true 时所有鉴权全部跳过（仅本地开发用）。
type AuthConfig struct {
	DebugSkipAllAuth  bool                 `mapstructure:"debug_skip_all_auth"` // 默认 false；true 全量放行（仅开发）
	FeishuBindingFile string               `mapstructure:"feishu_binding_file"` // 绑定账号持久化路径
	Cloudflared       CloudflaredConfig    `mapstructure:"cloudflared"`
	Access            CloudflareAccessConf `mapstructure:"cloudflare_access"`
	RateLimit         RateLimitConfig      `mapstructure:"ratelimit"`
}

// CloudflareAccessConf 是把外网访问前置给 Cloudflare Access 的配置（可选）。
//
// 动机：固定域名上线后，域名证书（Let's Encrypt）必然进证书透明日志，等于
// 可被公开枚举，"靠域名没人知道"这层遮蔽不再成立；而面板能在本机执行 agent
// 命令。Access 把访问控制变成「按人授权、可随时撤销、URL 不带凭据」。
//
// 源站侧必须**自行验签** Cf-Access-Jwt-Assertion，否则伪造同名头即可绕过
// （见 internal/auth/access.go）。
type CloudflareAccessConf struct {
	// Enabled 开启后用 Access JWT 作为外网主凭据。
	Enabled bool `mapstructure:"enabled"`
	// TeamDomain 团队域，如 https://myteam.cloudflareaccess.com（可省略 scheme）。
	TeamDomain string `mapstructure:"team_domain"`
	// Audience Access 应用（Self-hosted / Fixed hostname）的 AUD tag。
	// 可用 PIEQI_AUTH_CLOUDFLARE_ACCESS_AUDIENCE 环境变量覆盖，避免入库。
	Audience string `mapstructure:"audience"`
	// JWKSURL 公钥地址；留空 = <TeamDomain>/cdn-cgi/access/certs。
	JWKSURL string `mapstructure:"jwks_url"`
	// TokenFallback 是否保留「外链 ?token=」作为 Access 之外的第二道兜底。
	// 默认 true：Access 配好并验证通过后再关（走 Access-only），避免把自己
	// 锁在门外。置 false 即 TokenDisabled。
	TokenFallback *bool `mapstructure:"token_fallback"`
}

// TokenFallbackEnabled 解析 TokenFallback，未配置时默认 true（保留兜底）。
func (c CloudflareAccessConf) TokenFallbackEnabled() bool {
	if c.TokenFallback == nil {
		return true
	}
	return *c.TokenFallback
}

// CloudflaredConfig Cloudflared 隧道配置。
type CloudflaredConfig struct {
	BinaryPath string        `mapstructure:"binary_path"` // cloudflared 可执行路径；默认 "cloudflared"（PATH 查找）
	DefaultTTL time.Duration `mapstructure:"default_ttl"` // 默认 15m；可选 15m/1h/4h

	// Mode 隧道模式："quick"（默认，trycloudflare 随机域名，Cloudflare 会周期性
	// 回收）| "named"（固定域名：Zero Trust 建 named tunnel，域名永不回收）。
	Mode string `mapstructure:"mode"` // 默认 quick
	// TunnelToken named 模式的 cloudflared tunnel token（base64 串）。quick 模式忽略。
	//
	// ⚠️ 不要把这个值提交进仓库：config.yaml 是 git 跟踪文件。推荐留空，
	// 改用 PIEQI_AUTH_CLOUDFLARED_TUNNEL_TOKEN 环境变量，或写入 TunnelTokenFile
	// 指向的仓外文件（默认 ~/.pieqi/cloudflared_token）。解析顺序：
	// 环境变量 > 本字段 > TunnelTokenFile。
	TunnelToken string `mapstructure:"tunnel_token"`
	// TunnelTokenFile 存放隧道 token 的仓外文件路径；默认
	// <dataRoot>/cloudflared_token（~/.pieqi/cloudflared_token）。空 = 用默认路径。
	// 读取时会 TrimSpace，允许文件里带尾随换行。
	TunnelTokenFile string `mapstructure:"tunnel_token_file"`
	// PublicHostname named 模式对外域名，如 304456.xyz。quick 模式忽略。
	PublicHostname string `mapstructure:"public_hostname"`

	// 自动自愈（域名回收检测）：Cloudflare 会周期性回收 trycloudflare 快速
	// 隧道域名（公网 DNS 变 NXDOMAIN），即使 cloudflared 进程仍存活。TunnelManager
	// 按 HealthCheckInterval 周期探测 hostname，连续 HealthCheckFailures 次失败
	// 即判定死亡，自动重启隧道换新域名并推送新链接。
	HealthCheckInterval time.Duration `mapstructure:"health_check_interval"` // 默认 5m；<=0 关闭巡检
	HealthCheckFailures int           `mapstructure:"health_check_failures"` // 默认 3；连续失败阈值
}

// ResolveTunnelToken 解析 named 模式的隧道凭据，返回 (token, 来源描述, error)。
// 顺序：配置值（含 PIEQI_AUTH_CLOUDFLARED_TUNNEL_TOKEN 环境变量覆盖）
// > TunnelTokenFile（默认 ~/.pieqi/cloudflared_token，仓外文件）。
// 两边都没有 → 返回空串且不报错，由调用方按"缺 token"处理（错误信息才好指向配置项）。
// 来源描述只用于日志，**绝不含 token 本身**。
func (c CloudflaredConfig) ResolveTunnelToken() (string, string, error) {
	if t := strings.TrimSpace(c.TunnelToken); t != "" {
		return t, "config/env", nil
	}
	if c.TunnelTokenFile == "" {
		return "", "", nil
	}
	b, err := os.ReadFile(c.TunnelTokenFile)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", nil // 文件不存在 = 未配置，不算错误
		}
		return "", "", fmt.Errorf("read tunnel token file %s: %w", c.TunnelTokenFile, err)
	}
	t := strings.TrimSpace(string(b))
	if t == "" {
		return "", "", nil
	}
	return t, "file:" + c.TunnelTokenFile, nil
}

// IsNamed 报告是否选用 named（固定域名）隧道模式；其余值（含空）都按 quick 处理。
func (c CloudflaredConfig) IsNamed() bool {
	return strings.EqualFold(strings.TrimSpace(c.Mode), "named")
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

	// 环境变量覆盖（如 PIEQI_SERVER_PORT=3000）。
	// 嵌套 key 必须配 KeyReplacer：否则 viper 会去找 "PIEQI_AUTH.CLOUDFLARED.TUNNEL_TOKEN"
	// 这种含点的变量名（合法环境变量名不含点），覆盖永远不生效。
	v.SetEnvPrefix("PIEQI")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// 默认值
	v.SetDefault("server.port", 3000)
	v.SetDefault("server.mode", "debug")
	v.SetDefault("api.enabled", true)
	v.SetDefault("pieqi.enabled", false)
	v.SetDefault("pieqi.permission_mode", "bypassPermissions")
	// 注：pieqi.auto_approve_tools 已删除（见 Load() 的弃用告警）。免审名单的运行期真相
	// 是设置页的 auto_approve_l0 / auto_approve_l1（core.Settings.AutoApproveTools），
	// 由 main.go 直接读 SettingsStore —— 这个配置项此前**只被解析、无人消费**，
	// 且其默认值里的 delete 属 L3，与"L3 永不放行"的硬边界矛盾。
	v.SetDefault("pieqi.cleanup_worktrees", true)
	v.SetDefault("pieqi.hook_timeout", "30m")
	v.SetDefault("pieqi.hook_tools", []string{"Bash", "Write", "Edit", "NotebookEdit"})
	v.SetDefault("pieqi.max_concurrent_per_project", 4)
	v.SetDefault("pieqi.worktree_base", "") // 空 = DefaultDataRoot()/worktrees（main 侧解析）
	v.SetDefault("pieqi.base_branch", "main")
	v.SetDefault("pieqi.bots_dir", filepath.Join(DefaultDataRoot(), "bots"))
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
	// dsh 刻意不设 transport 默认值：空 transport = 不进 agent 选择器（需要用户显式开启，
	// 理由是它依赖本机 dsh 安装 + DEEPSEEK_API_KEY，见 AgentDshConfig）。
	v.SetDefault("agents.dsh.acp.agent_type", "dsh")
	v.SetDefault("agents.dsh.acp.init_timeout", "2m") // 首启动冷启动远慢于 qodercli，见 DshACPConfig.InitTimeout
	v.SetDefault("agents.dsh.acp.idle_timeout", "15m")
	v.SetDefault("auth.debug_skip_all_auth", false)
	v.SetDefault("auth.feishu_binding_file", filepath.Join(DefaultDataRoot(), "feishu_binding.json"))
	v.SetDefault("auth.cloudflared.binary_path", "cloudflared")
	v.SetDefault("auth.cloudflared.default_ttl", "15m")
	v.SetDefault("auth.cloudflared.mode", "quick")
	v.SetDefault("auth.cloudflared.tunnel_token", "")
	v.SetDefault("auth.cloudflared.tunnel_token_file", filepath.Join(DefaultDataRoot(), "cloudflared_token"))
	v.SetDefault("auth.cloudflared.public_hostname", "")
	v.SetDefault("auth.cloudflared.health_check_interval", "5m")
	v.SetDefault("auth.cloudflared.health_check_failures", 3)
	v.SetDefault("auth.ratelimit.max_failures_per_min", 5)
	v.SetDefault("auth.ratelimit.blacklist_duration", "10m")
	v.SetDefault("auth.cloudflare_access.enabled", false)
	v.SetDefault("auth.cloudflare_access.team_domain", "")
	v.SetDefault("auth.cloudflare_access.audience", "")
	v.SetDefault("auth.cloudflare_access.jwks_url", "")
	v.SetDefault("auth.cloudflare_access.token_fallback", true)
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

	// 空 tunnel_token_file 回退默认路径（同上：显式空值会覆盖 viper 默认值）
	if cfg.Auth.Cloudflared.TunnelTokenFile == "" {
		cfg.Auth.Cloudflared.TunnelTokenFile = filepath.Join(DefaultDataRoot(), "cloudflared_token")
	}

	// 空 bots_dir 回退默认路径（同上：显式空值会覆盖 viper 默认值）
	if cfg.Pieqi.BotsDir == "" {
		cfg.Pieqi.BotsDir = filepath.Join(DefaultDataRoot(), "bots")
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

	// 2) pieqi.auto_approve_tools 已删除语义：该字段从未被运行期消费（免审名单一直
	//    来自设置页的 auto_approve_l0/l1），默认值里的 delete 还属 L3、与硬边界矛盾。
	//    这里只告警不改行为——用户若指望它生效，必须知道去设置页改。
	if v.InConfig("pieqi.auto_approve_tools") {
		cfg.Deprecations = append(cfg.Deprecations,
			"pieqi.auto_approve_tools 已移除（它从未生效）：免审名单请在设置页用 auto_approve_l0 / auto_approve_l1 开关控制")
	}

	// 3) qoder 兼容回填：agents.qoder.acp 未显式配置（新节任一字段都不在配置文件里）
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
