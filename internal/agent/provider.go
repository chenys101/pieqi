// Package agent: provider.go 注册 ACP 系 agent（qoder / dsh 等）的 AgentSession 工厂。
//
// 与 claude 包（bridge 传输）不同，ACP 系 agent 复用现有 ACPAgent（acp.go）+ sessionAdapter
// （session.go）桥接成 AgentSession：业务层 agent.Open(Agent:"qoder") 即可驱动，
// 底层仍是 qodercli --acp；agent.Open(Agent:"dsh") 底层是 dsh --profile acp。
// 这是 multi-agent.md §11 P4（qoder 迁到同接口）的工厂层落地点，加新 ACP agent 只需在
// acpAgentSpecs 与 config.AgentsConfig 各登记一次。
package agent

import (
	"context"

	"pieqi/internal/config"

	"go.uber.org/zap"
)

// ACPProviderConfig ACP 系 agent provider 的配置。
// Agents 的 key 是业务 agent 名（"qoder" / "dsh"，即 AgentQoder/AgentDsh），
// 与 session provider 的注册名、Task.Agent 的取值同一个串。
type ACPProviderConfig struct {
	Agents map[string]config.ACPConfig
	Logger *zap.Logger
}

var acpProviderCfg = ACPProviderConfig{}

// ConfigureACPProviders 设置 ACP 系 agent 的 provider 配置（app 启动时调用；可多次覆盖）。
// Agents 逐个合并（同名覆盖）；Logger 非空时替换。
func ConfigureACPProviders(c ACPProviderConfig) {
	for name, cfg := range c.Agents {
		if acpProviderCfg.Agents == nil {
			acpProviderCfg.Agents = make(map[string]config.ACPConfig)
		}
		acpProviderCfg.Agents[name] = cfg
	}
	if c.Logger != nil {
		acpProviderCfg.Logger = c.Logger
	}
}

// defaultACPAgentType 业务 agent 名 → 配置里 agent_type 空缺时的兜底。
// 兜底必须按 agent 区分：共用一个兜底会让「选了 dsh 但没配 agent_type」的会话静默起 qodercli。
// 这个表同时是 ACP 系 agent 的注册清单：加一个新 ACP agent 就是在这里加一行 +
// config.AgentsConfig 加一节 + ACPProviderConfigFromAgents 加一个分支。
var defaultACPAgentType = map[string]string{
	AgentQoder:     "qodercli",
	AgentDsh:       "dsh",
	AgentCodebuddy: "codebuddy",
}

// acpConfigFor 取某业务名的 ACP 配置，agent_type 空缺时套上面的兜底。
func acpConfigFor(name string) config.ACPConfig {
	cfg := acpProviderCfg.Agents[name]
	if cfg.AgentType == "" {
		cfg.AgentType = defaultACPAgentType[name]
	}
	return cfg
}

// ACPProviderConfigFromAgents 把 config.AgentsConfig 里各 ACP 系 agent 节转成 provider 配置
// （main.go 接线用）。只收 transport=="acp" 且 agent_type 非空的节；一个都没有时返回零值。
func ACPProviderConfigFromAgents(a config.AgentsConfig) ACPProviderConfig {
	out := ACPProviderConfig{}
	put := func(name string, cfg config.ACPConfig) {
		if cfg.AgentType == "" {
			return
		}
		if out.Agents == nil {
			out.Agents = make(map[string]config.ACPConfig)
		}
		out.Agents[name] = cfg
	}
	if a.Qoder.Transport == "acp" {
		put(AgentQoder, a.Qoder.ACPConfig())
	}
	if a.Dsh.Transport == "acp" {
		put(AgentDsh, a.Dsh.ACPConfig())
	}
	if a.Codebuddy.Transport == "acp" {
		put(AgentCodebuddy, a.Codebuddy.ACPConfig())
	}
	return out
}

// init 按 defaultACPAgentType 注册 ACP 系 agent 的 AgentSession 工厂：
// agent.Open(Agent:"qoder"/"dsh") 即可。
func init() {
	for name := range defaultACPAgentType {
		name := name
		RegisterSessionProvider(name, func(ctx context.Context, p OpenParams) (AgentSession, error) {
			return openACPSession(ctx, acpConfigFor(name), p)
		})
	}
}

// AgentInfo 一个**可被任务选择**的 agent 的描述（新任务页选择器用）。
//
// Name 是 Task.Agent / SessionConfig.Agent 的取值，也是 session provider 的注册名——
// 三处必须是同一个字符串，否则选出来的 agent 打不开。
type AgentInfo struct {
	Name        string `json:"name"`         // 业务名：claude / qoder
	DisplayName string `json:"display_name"` // 人类可读名（选择器展示）
	Description string `json:"description,omitempty"`
	// Transport 传输层的人类可读描述（展示用，不参与选路）。
	Transport string `json:"transport,omitempty"`
	// Capabilities 该 agent 具备的能力标签（展示用）。
	Capabilities []string `json:"capabilities,omitempty"`
	// ImagePrompt 该 agent 的传输**是否具备图片入站通路**（新建任务页据此显示加图入口）。
	//
	// 与上面两个字段的关键区别：这是**判据**，不是展示文案 —— 前端拿它做显隐决策，
	// 所以它必须反映真实能力，且不能是给人看的字符串（改文案会静默改变行为）。
	//
	// 语义边界：true = "这条路能把图送出去"，**不**保证"对端模型一定看得见图"。
	// 那取决于模型自身的视觉能力，只有真正发送时才由对端报错暴露。所以前端
	// 显示入口 ≠ 保证能发；残余不确定性由后端的明确报错兜底（绝不静默丢图）。
	//
	// 为什么放在目录里而不是让前端按 agent 名硬编码：硬编码的名单必然会与
	// 服务端配置漂移（例如把 claude 的 transport 配成 print 后，桥的图片通路
	// 就不存在了，而前端仍会显示入口）。目录是服务端算出来的唯一事实源。
	ImagePrompt bool `json:"image_prompt"`

	// ---- 结构化能力（2026-10-18 新增）----
	//
	// 为什么不再靠 Capabilities 那串文案：三个 agent 的 Capabilities 此前**完全相同**
	// （都是「流式输出 / 工具审批 / 续问 / 取消」），等于没给任何信息 ——
	// 用户在 Agents 页看到三张一模一样的卡，换了个名字而已。
	//
	// 下面的字段是**判据**，不是文案（与 ImagePrompt 同一条纪律）：前端拿它决定
	// 怎么渲染、要不要把某个入口灰掉；改文案不会静默改变行为。

	// GoalMode goal 能力的形态（见 docs/agents-goal-and-capabilities.md）：
	//   "tool"    —— 模型可见的工具（create_goal/update_goal），ACP 实测可通
	//   "command" —— 斜杠命令 + Stop hook（claude），命令生效但状态靠事件
	//   ""        —— 该通道不开放 goal
	GoalMode string `json:"goal_mode,omitempty"`

	// GoalStateVisible goal 状态在 pieqi 里**能不能看见**。
	//
	// 与 GoalMode 分开的理由：claude 的 goal 命令可用、状态却曾经被桥丢弃
	// （active_goal 落进 default: break）。这两件事坏在不同地方，用户能做的
	// 补救也不同 —— 合一个字段会把"命令能设但看不到进度"表述成"不支持"。
	GoalStateVisible bool `json:"goal_state_visible"`

	// SessionAlways 是否支持「本会话内同类操作免审」（按 ToolKind 记账）。
	//
	// claude 走 PreToolUse hook，`Resolve` 不认 approve_session；ACP 系（qoder/dsh）
	// 走 RequestPermission，支持。前端据此决定卡片上要不要出现「同类免审」。
	SessionAlways bool `json:"session_always"`

	// ModelSelectable 该 agent 是否会下发可选模型清单。
	//
	// 注意这仍是**能力声明**：清单本身按需拉（GET /api/agents/{name}/models），
	// 因为取清单要起一次 agent 进程。这里只说明"值不值得去拉"。
	ModelSelectable bool `json:"model_selectable"`

	// ColdStartCold 冷启动是否明显慢（>30s）。目前 dsh 实测 >90s。
	//
	// 用途不是"显示一个数字"，而是**提醒用户别在错误的时机选它**：
	// 新任务页选 dsh 时要预期"点了创建要等一分多钟才动"。
	ColdStartCold bool `json:"cold_start_cold"`
}

// GoalMode 的取值。语义见 docs/agents-goal-and-capabilities.md。
const (
	// GoalModeTool goal 是**模型可见的工具**（create_goal / update_goal）。
	// 即便通道没有命令平面，模型读工具说明也会自己调 —— dsh 属于这一类。
	GoalModeTool = "tool"
	// GoalModeCommand goal 是**斜杠命令 + Stop hook**（Claude Code 的 /goal <condition>）。
	// 命令由 CLI 本地解析，不经模型回合。
	GoalModeCommand = "command"
)

// AgentClaude / AgentQoder / AgentDsh / AgentCodebuddy 可用 agent 的常量名（避免散落的裸串拼错）。
const (
	AgentClaude    = "claude"
	AgentQoder     = "qoder"
	AgentDsh       = "dsh"
	AgentCodebuddy = "codebuddy"
)

// AvailableAgents 按配置算出可被任务选择的 agent 列表（顺序即前端展示顺序，claude 在前）。
//
// 判据是「配置上跑得起来」，不做进程探活——探活要 spawn 子进程，代价与副作用都不该
// 花在渲染一个下拉框上。真跑不起来时任务会带明确错误失败（ensureACPSession 的 surface 路径）。
// ACP 系 agent 的可用性判据复用 ACPProviderConfigFromAgents（与 main.go 的接线判据同一个函数，
// 避免「选择器里有但没注册 provider」这类分叉）。
//   - claude：永远可用。transport=sdk-bridge 时走桥（失败自动回退 print），print 时直连 claude -p。
//   - qoder / dsh：transport=acp 且 agent_type 非空。
func AvailableAgents(a config.AgentsConfig) []AgentInfo {
	claudeTransport := "SDK Bridge"
	// claude 的图片通路**只在桥路径存在**：桥能把图转成 SDK 的 content blocks；
	// 而 print 回退（claude -p 的纯文本 prompt 通道）没有实现图片。
	// 所以显式配成 transport=print 时，这里必须是 false —— 否则前端会显示加图入口，
	// 用户选了图却在发送时失败（配置决定能力，不是 agent 名决定能力）。
	claudeImages := a.Claude.Transport != "print"
	if a.Claude.Transport == "print" {
		claudeTransport = "claude -p"
	}
	out := []AgentInfo{{
		Name:         AgentClaude,
		DisplayName:  "Claude Code",
		Description:  "Anthropic 官方 Agent，经 SDK 常驻桥驱动（不可用时自动回退 claude -p）",
		Transport:    claudeTransport,
		Capabilities: []string{"流式输出", "工具审批", "续问", "取消"},
		ImagePrompt:  claudeImages,
		// goal 是**斜杠命令**（`/goal <完成条件>`，CLI 本地解析）+ Stop hook 判定；
		// 没有 create_goal 这类模型可见的工具入口。
		// 状态可见性在 2026-10-18 之前是 false（桥把 active_goal 丢了），已修。
		GoalMode:         GoalModeCommand,
		GoalStateVisible: true,
		// 「同类免审」只有 ACP 路径有：Resolve 认 approve_session 的前提是
		// 对端发 RequestPermission；claude 走 PreToolUse hook，选项里没有这一项。
		SessionAlways:   false,
		ModelSelectable: false, // 桥不下发模型清单（用 agent 自己的默认）
		ColdStartCold:   false, // claude SDK 桥秒级起来
	}}
	configured := ACPProviderConfigFromAgents(a)
	if _, ok := configured.Agents[AgentQoder]; ok {
		out = append(out, AgentInfo{
			Name:         AgentQoder,
			DisplayName:  "Qoder CLI",
			Description:  "Qoder 官方 CLI（ACP 协议），可选用限时免费的 Qwen3.8-Flash 模型",
			Transport:    "ACP",
			Capabilities: []string{"流式输出", "工具审批", "续问", "取消"},
			// ACP 具备图片入站通路（ACPAgent 实现 RichPromptSender），
			// 但是否真收取决于对端握手声明 promptCapabilities.image —— 见 ImagePrompt 的说明。
			ImagePrompt: true,
			// qodercli **有**完整的 goal（工具 + /goal 命令 + auto-continuation，见
			// docs/agents-goal-and-capabilities.md），但 ACP 只开放 4 个斜杠命令
			// （/init /memory /about /help），goal 不在其中；而且它的 goal 是
			// SDK control request，不在 ACP 路径上。**两条路都走不通** ⇒ 留空。
			//
			// 这里刻意不写 "tool"：实测（字面 /goal 与自然语言各一组，共 62 次 tool_call）
			// goal 工具调用 **0 次**，模型也不会自己调 —— 谎报能力会让用户盲打 /goal。
			GoalMode:         "",
			GoalStateVisible: false,
			SessionAlways:    true, // ACP：proceed_always / approve_session 都支持
			ModelSelectable:  true, // qodercli 下发模型清单（Qwen3.8-Flash 等）
			ColdStartCold:    false,
		})
	}
	if _, ok := configured.Agents[AgentDsh]; ok {
		out = append(out, AgentInfo{
			Name:         AgentDsh,
			DisplayName:  "DeepSeek Harness",
			Description:  "DeepSeek 官方 agent harness（dsh --profile acp）。模型路由取自 dsh 侧 acp profile 的 pin，出厂 pin 需 DEEPSEEK_API_KEY",
			Transport:    "ACP",
			Capabilities: []string{"流式输出", "工具审批", "续问", "取消"},
			ImagePrompt:  true,
			// dsh 的 goal 是**模型可见的工具**（create_goal/get_goal/update_goal）：
			// 即便 ACP 没有命令平面，模型也会从工具说明里读出目标意图并自己调用 ——
			// 已实测收到 tool_call: create_goal → phase=active, activation=armed。
			GoalMode:         GoalModeTool,
			GoalStateVisible: true, // 走 tool_call 事件，时间线直接可见
			SessionAlways:    true,
			ModelSelectable:  true, // acp profile 的 pin 提供模型清单
			// dsh 冷启动实测 >90s（init_timeout 因此设为 2m）；
			// 这条要显示出来，否则用户不知道"点了创建要等一分多钟"。
			ColdStartCold: true,
		})
	}
	if _, ok := configured.Agents[AgentCodebuddy]; ok {
		out = append(out, AgentInfo{
			Name:         AgentCodebuddy,
			DisplayName:  "CodeBuddy CLI",
			Description:  "腾讯 CodeBuddy CLI（ACP 协议）。需先在 CLI 内登录（iOA / Google·GitHub / 微信 / 企业域名）或提供 CODEBUDDY_API_KEY",
			Transport:    "ACP",
			Capabilities: []string{"流式输出", "工具审批", "续问", "取消"},
			// initialize 实测（v2.159.0）声明 promptCapabilities.image=true，
			// 故 ACP 这条图片入站通路存在；是否真看得见图仍由对端模型决定。
			ImagePrompt: true,
			// goal 没有任何实测证据（CodeBuddy 是闭源 CLI），不谎报：
			// 报成 tool/command 会让用户盲打 /goal 后没反应。等实测再开。
			GoalMode:         "",
			GoalStateVisible: false,
			SessionAlways:    true, // ACP：proceed_always / approve_session 都支持
			// session/new 的标准 configOptions 里下发 id="model"（实测 20 个模型），
			// 与 qoder 同一条通道：清单走 configOptions、设模型走 set_config_option。
			ModelSelectable: true,
			ColdStartCold:   false, // Node CLI，initialize/session-new 都在秒级
		})
	}
	return out
}

// openACPSession 用现有 ACPAgent 建一个 ACP 会话，并桥接成 AgentSession。
// 注意：NewSession 会懒 spawn agent 进程（如 qodercli --acp / dsh --profile acp），失败返回错误。
// cfg.AgentType 由 acpConfigFor 保证非空，这里不再猜测具体厂商。
func openACPSession(ctx context.Context, cfg config.ACPConfig, p OpenParams) (AgentSession, error) {
	adapter := NewACPAgent(cfg, acpProviderCfg.Logger)
	sid, err := adapter.NewSession(ctx, SessionConfig{Cwd: p.Cwd, ResumeFrom: p.ResumeFrom, TaskID: p.TaskID, Model: p.Model})
	if err != nil {
		return nil, err
	}
	return NewSessionAdapter(adapter, sid, Caps{MultiTurnPersistent: true, Streaming: true}), nil
}
