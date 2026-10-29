package agent

import (
	"time"

	"pieqi/internal/config"
)

// manifest.go 是「接入一个新 agent」的**唯一改动点**。
//
// 背景（2026-10-22 重构）：此前接入一个 agent 要在 5 处登记 —— config.AgentsConfig 加一节、
// 加 AgentXxxConfig 类型、加 XxxACPConfig 类型、defaultACPAgentType 加一行、
// ACPProviderConfigFromAgents 加一个分支，外加前端。其中绝大多数是**代码事实**，
// 写进 config.yaml 的取值永远是同一个常量（例如 agent_type: codebuddy、spawn_command: []），
// 用户写了等于把代码常量抄进配置文件，抄错反而制造故障。
//
// 现在的分层：
//
//	manifest（本文件）   —— 代码事实：名字、显示名、默认 spawn、能力、模型通道。
//	                        **永远不该出现在配置里**。
//	settings.json         —— 用户意图：开不开某个 agent（enabled）、模型偏好等。
//	                        **运行期可改、可热应用**，不随代码走。
//
// 于是接一个新 agent = 在 agentManifests 里加一条。config.yaml 里不再有 agents 段。
//
// 为什么放在 agent 包而不是 config 包：它描述的是「怎么把这个 agent 跑起来」，
// 属于传输层知识；config 包只应负责「从哪读、读到什么」，不该知道 codebuddy 的 npm 入口路径。
// 反过来也避免了 config ⇄ agent 的循环依赖。

// AgentManifest 一个 agent 的静态描述（代码事实）。
//
// 这里的每个字段都必须是**跨机器不变**的：只要同一个 agent 版本，任何机器上取值相同。
// 但凡"这台机器上装在哪、开不开、用哪个模型"——一律不属于 manifest。
type AgentManifest struct {
	// Name 业务名：Task.Agent / SessionConfig.Agent / session provider 注册名，
	// 三处必须是同一个串，否则选出来的 agent 打不开。
	Name string

	// DisplayName / Description 选择器展示用。
	DisplayName string
	Description string

	// Transport 展示用的传输层描述（不参与选路）。
	Transport string

	// Kind 传输族。决定走哪条 provider 注册路径：
	//   TransportClaudeBridge —— sdk-bridge / print（claude）
	//   TransportACP          —— ACP 协议（qoder / dsh / codebuddy / cursor / codex）
	Kind TransportKind

	// ACPType manifest 里声明的 agent_type（ACP 族才有意义）。
	//
	// 它就是以前 config 里那个 `agent_type:` 字段的正确取值 —— 因为是代码事实，
	// 所以搬到这里；用户无法也无需配置它。
	ACPType string

	// DefaultSpawn 「按这个 agent 类型该跑什么命令」的默认值（裸名，不含机器路径）。
	// 空 = 按 ACPType 兜底成 `<ACPType> --acp`（dsh 那种非 --acp 形态要显式给）。
	DefaultSpawn []string

	// 能力声明（判据，不是文案 —— 前端据此决定渲染与入口显隐）。
	ImagePrompt      bool
	GoalMode         string
	GoalStateVisible bool
	SessionAlways    bool
	ModelSelectable  bool
	ColdStartCold    bool

	// InitTimeout / IdleTimeout 默认超时。0 = 用全局默认。
	InitTimeout time.Duration
	IdleTimeout time.Duration

	// OptIn 是否默认关闭。
	//
	// true 表示「装了也未必能用」：需要额外凭据、或由用户显式选择才启用
	// （例如 dsh 要 DEEPSEEK_API_KEY、codebuddy 要登录）。这类 agent 在
	// settings.agents.enabled 里不出现时**不注册**，避免用户选了之后只得到一条失败。
	OptIn bool
}

// TransportKind 传输族。
type TransportKind string

const (
	TransportClaudeBridge TransportKind = "claude-bridge"
	TransportACP          TransportKind = "acp"
)

// agentManifests 全部已知 agent 的静态描述。**顺序即选择器展示顺序。**
//
// ⚠️ 新增 agent 只改这里。不要在 config 里再加 agents 段、不要加 AgentXxxConfig 类型、
// 不要改默认模型通道 —— 那些都从这里派生。
var agentManifests = []AgentManifest{
	{
		Name:        AgentClaude,
		DisplayName: "Claude Code",
		Description: "Anthropic 官方 Agent，经 SDK 常驻桥驱动（不可用时自动回退 claude -p）",
		Transport:   "SDK Bridge",
		Kind:        TransportClaudeBridge,
		// claude 的图片通路**只在桥路径存在**（print 回退是纯文本 prompt 通道）。
		// 所以这条是"条件能力"，由 AvailableAgents 按实际 transport 再裁一次。
		ImagePrompt:      true,
		GoalMode:         GoalModeCommand,
		GoalStateVisible: true,
		SessionAlways:    false, // claude 走 PreToolUse hook，没有 approve_session
		ModelSelectable:  false, // 桥不下发模型清单
		ColdStartCold:    false,
	},
	{
		Name:        AgentQoder,
		DisplayName: "Qoder CLI",
		Description: "Qoder 官方 CLI（ACP 协议），可选用限时免费的 Qwen3.8-Flash 模型",
		Transport:   "ACP",
		Kind:        TransportACP,
		ACPType:     "qodercli",
		// qodercli **有**完整的 goal，但 ACP 只开放 4 个斜杠命令，goal 不在其中；
		// 它的 goal 走 SDK control request，不在 ACP 路径上 ⇒ 两条路都不通，留空。
		// 实测（字面 /goal 与自然语言各一组，共 62 次 tool_call）goal 调用 0 次 ——
		// 谎报能力会让用户盲打 /goal。
		GoalMode:         "",
		GoalStateVisible: false,
		ImagePrompt:      true,
		SessionAlways:    true,
		ModelSelectable:  true,
		ColdStartCold:    false,
		InitTimeout:      30 * time.Second,
		IdleTimeout:      15 * time.Minute,
	},
	{
		Name:        AgentDsh,
		DisplayName: "DeepSeek Harness",
		Description: "DeepSeek 官方 agent harness（dsh --profile acp）。模型路由取自 dsh 侧 acp profile 的 pin，出厂 pin 需 DEEPSEEK_API_KEY",
		Transport:   "ACP",
		Kind:        TransportACP,
		ACPType:     "dsh",
		// dsh 的 ACP 是一个 profile，不是 --acp 开关。
		DefaultSpawn: []string{"dsh", "--profile", "acp"},
		// dsh 的 goal 是**模型可见的工具**（create_goal/get_goal/update_goal），
		// 已实测收到 tool_call: create_goal → phase=active。
		GoalMode:         GoalModeTool,
		GoalStateVisible: true,
		ImagePrompt:      true,
		SessionAlways:    true,
		ModelSelectable:  true,
		// 冷启动实测 >90s（首次要组装配方、挂载插件），所以 init_timeout 必须放宽。
		ColdStartCold: true,
		InitTimeout:   2 * time.Minute,
		IdleTimeout:   15 * time.Minute,
		OptIn:         true, // 需要 DEEPSEEK_API_KEY，否则选了也只会失败
	},
	{
		Name:        AgentCodebuddy,
		DisplayName: "CodeBuddy CLI",
		Description: "腾讯 CodeBuddy CLI（ACP 协议）。需先在 CLI 内登录（iOA / Google·GitHub / 微信 / 企业域名）或提供 CODEBUDDY_API_KEY",
		Transport:   "ACP",
		Kind:        TransportACP,
		ACPType:     "codebuddy",
		// CodeBuddy 是闭源 CLI，goal 没有任何实测证据 ⇒ 不谎报。
		GoalMode:         "",
		GoalStateVisible: false,
		// initialize 实测（v2.159.0）声明 promptCapabilities.image=true。
		ImagePrompt:     true,
		SessionAlways:   true,
		ModelSelectable: true, // session/new 标准 configOptions 下发 20 个模型
		ColdStartCold:   false,
		InitTimeout:     30 * time.Second,
		IdleTimeout:     15 * time.Minute,
		OptIn:           true, // 需要先登录或提供 CODEBUDDY_API_KEY
	},
	{
		Name:        AgentCursor,
		DisplayName: "Cursor Agent",
		Description: "Cursor Agent CLI（ACP：agent acp）。需本机执行 agent login，或设置 CURSOR_API_KEY",
		Transport:   "ACP",
		Kind:        TransportACP,
		// ACPType 即二进制裸名；Cursor 的 ACP 入口是子命令 `acp` 而非 `--acp`，
		// 所以 DefaultSpawn 必须显式给出（见 docs/notes/cursor-acp-p0.md）。
		ACPType:      "agent",
		DefaultSpawn: []string{"agent", "acp"},
		GoalMode:         "",
		GoalStateVisible: false,
		ImagePrompt:      true, // initialize promptCapabilities.image=true
		SessionAlways:    true, // loadSession=true
		ModelSelectable:  true, // session/new 下发标准 configOptions
		ColdStartCold:    false,
		InitTimeout:      30 * time.Second,
		IdleTimeout:      15 * time.Minute,
		OptIn:            true, // 需要登录或 CURSOR_API_KEY
	},
}

// manifestByName 按业务名查 manifest。
func manifestByName(name string) (AgentManifest, bool) {
	for _, m := range agentManifests {
		if m.Name == name {
			return m, true
		}
	}
	return AgentManifest{}, false
}

// AllManifests 返回全部已知 agent 的静态描述（副本，调用方可随意持有）。
func AllManifests() []AgentManifest {
	out := make([]AgentManifest, len(agentManifests))
	copy(out, agentManifests)
	return out
}

// DefaultSpawnCommand 按 manifest 求默认 spawn 命令分词。
//
// 显式给出 DefaultSpawn 时用它（dsh 的 --profile acp）；否则按 ACPType 兜底成
// `<ACPType> --acp` —— 这覆盖了绝大多数"原生 ACP CLI"的形态。
func (m AgentManifest) DefaultSpawnCommand() []string {
	if len(m.DefaultSpawn) > 0 {
		out := make([]string, len(m.DefaultSpawn))
		copy(out, m.DefaultSpawn)
		return out
	}
	if m.ACPType == "" {
		return nil
	}
	return []string{m.ACPType, "--acp"}
}

// ACPConfig 由 manifest 求该 agent 的 ACP 配置（transport 与 spawn 全走代码事实）。
//
// 用户仍可在 settings.json 里覆盖 spawn_command（例如把 dsh 指到独立安装位置），
// 那属于"这台机器上装在哪"的运行期知识，归 settings，不归 manifest。
func (m AgentManifest) ACPConfig(override config.ACPConfig) config.ACPConfig {
	out := config.ACPConfig{
		AgentType:   m.ACPType,
		InitTimeout: m.InitTimeout,
		IdleTimeout: m.IdleTimeout,
	}
	// 先把 manifest 的默认值铺上，再用用户覆盖项盖掉非零字段。
	out.SpawnCommand = m.DefaultSpawnCommand()
	if override.SpawnCommand != nil {
		out.SpawnCommand = override.SpawnCommand
	}
	if override.InitTimeout > 0 {
		out.InitTimeout = override.InitTimeout
	}
	if override.IdleTimeout > 0 {
		out.IdleTimeout = override.IdleTimeout
	}
	return out
}
