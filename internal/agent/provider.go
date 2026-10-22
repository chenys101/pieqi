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
	"sync"

	"pieqi/internal/config"

	"go.uber.org/zap"
)

// ACPProviderConfig ACP 系 agent provider 的配置。
// Agents 的 key 是业务 agent 名（"qoder" / "dsh"，即 AgentQoder/AgentDsh），
// 与 session provider 的注册名、Task.Agent 的取值同一个串。
type ACPProviderConfig struct {
	Agents map[string]config.ACPConfig
	Logger *zap.Logger

	// initialised 标记"这份 provider 表是否已经装配过"。
	// 用来区分**启动**与**运行期变更**：只有后者才该作废模型清单缓存
	// （启动时作废会把上一进程留下的磁盘缓存删掉，磁盘缓存就失去全部意义）。
	initialised bool
}

var (
	acpProviderMu  sync.RWMutex
	acpProviderCfg = ACPProviderConfig{}
)

// ConfigureACPProviders 设置 ACP 系 agent 的 provider 配置
// （启动时与运行期设置变更时调用）。
//
// Agents **整体替换**而不是逐项合并：settings.agents.enabled 的语义是"以列表为准"，
// 合并会让被关掉的 agent 继续残留在表里 —— 热应用后表现为"设置里关了，却还能跑"。
// Logger 非空时替换（避免设置变更只带 Agents 时把 logger 清空）。
func ConfigureACPProviders(c ACPProviderConfig) {
	acpProviderMu.Lock()
	old := acpProviderCfg.Agents
	initialised := acpProviderCfg.initialised
	acpProviderCfg.Agents = c.Agents
	acpProviderCfg.initialised = true
	if c.Logger != nil {
		acpProviderCfg.Logger = c.Logger
	}
	acpProviderMu.Unlock()

	// 探测身份变了的 agent 要丢掉模型清单缓存：清单是"用这份 spawn 配置起进程问出来的"，
	// spawn/agent_type 改了，旧答案就不再代表现状。不清掉的话，用户在设置页改完路径，
	// 界面还会拿旧清单显示一整天（内存档 24h / 磁盘档 7 天）。
	//
	// ⚠️ **首次调用是启动，不是变更**：那时 old 为空，若照"没有旧值就作废"处理，
	// 每次启动都会把上一进程存下的磁盘缓存删掉 —— 磁盘缓存唯一的用途（跨重启）当场作废，
	// 实测表现为"重启后首个请求仍要等 7s"（2026-10-22 踩过）。
	// 首次以本次配置把"初始状态"记入 old，后续调用才比较。
	// 必须在 acpProviderMu **之外**调：探测路径会在持自己的锁时反向调 acpConfigFor（RLock），
	// 同序持锁会锁序反转。
	if !initialised {
		return
	}
	for name, cfg := range c.Agents {
		if prev, ok := old[name]; ok && prev.SameProbeIdentity(cfg) {
			continue
		}
		InvalidateAgentModels(name)
	}
}

// acpLogger 返回当前 provider 的 logger（可能为 nil）。
func acpLogger() *zap.Logger {
	acpProviderMu.RLock()
	defer acpProviderMu.RUnlock()
	return acpProviderCfg.Logger
}

// defaultACPAgentType 业务 agent 名 → agent_type。
//
// 2026-10-22 起这张表**不再是手写的一份清单**，而是从 agentManifests 派生：
// 接入一个新 agent 只需在 manifest.go 加一条，这里、AvailableAgents、
// ACPProviderConfigFromAgents 会一起跟上。保留这个名字是为了兼容既有调用点。
var defaultACPAgentType = func() map[string]string {
	m := make(map[string]string, len(agentManifests))
	for _, spec := range agentManifests {
		if spec.Kind == TransportACP && spec.ACPType != "" {
			m[spec.Name] = spec.ACPType
		}
	}
	return m
}()

// acpConfigFor 取某业务名的 ACP 配置。
//
// 配置来源已改为「manifest 默认值 + settings 覆盖」（见 AgentRuntimeConfig），
// agent_type 恒来自代码事实，不再需要"空缺时套兜底"这一步。
func acpConfigFor(name string) config.ACPConfig {
	acpProviderMu.RLock()
	cfg, ok := acpProviderCfg.Agents[name]
	acpProviderMu.RUnlock()
	if ok {
		return cfg
	}
	// 未显式注册（例如 settings 里没开、但有人强行选了它）：仍按 manifest 兜底，
	// 让错误发生在 spawn 阶段（可读的"找不到可执行文件"）而不是这里返回空配置。
	if spec, ok := manifestByName(name); ok {
		return spec.ACPConfig(config.ACPConfig{})
	}
	return config.ACPConfig{}
}

// AgentRuntimeConfig 运行期的 agent 选择结果：哪些 agent 被启用，以及各自的覆盖项。
//
// 这是 settings.json 里 agents 段的 Go 形态。**它只承载用户意图**：
// 开不开、装在哪、用哪个模型。凡是 manifest 能表达的（agent_type、默认 spawn、
// 能力声明）都不出现在这里。
type AgentRuntimeConfig struct {
	// Enabled 被启用的业务名集合。为空时只启用 manifest 里非 OptIn 的（即 claude/qoder）。
	Enabled []string
	// Overrides 按业务名给出的运行期覆盖（spawn_command、transport 等机器相关知识）。
	//
	// Transport 只对 claude 有意义（sdk-bridge / print）：它是"这台机器上桥能不能跑"
	// 的运行期事实，所以归这里而不是 manifest。ACP 系的 transport 恒为 acp（代码事实）。
	Overrides map[string]config.ACPConfig
	// ClaudeTransport claude 的传输选择（"sdk-bridge" 或 "print"）；空 = 用默认 sdk-bridge。
	ClaudeTransport string
}

// ClaudeConfigTranspor 返回 claude 实际使用的 transport（空则默认 sdk-bridge）。
func (rt AgentRuntimeConfig) ClaudeTransportOrDefault() string {
	if rt.ClaudeTransport == "" {
		return "sdk-bridge"
	}
	return rt.ClaudeTransport
}

// ACPProviderConfigFromRuntime 由运行期选择结果算出该注册哪些 ACP provider。
//
// 取代旧的 ACPProviderConfigFromAgents：后者要求调用方传 config.AgentsConfig，
// 也就是让"注册什么"取决于配置文件里逐节手写的 transport —— 那是本末倒置
// （transport 是代码事实，写在配置里只多一个能写错的地方）。
func ACPProviderConfigFromRuntime(rt AgentRuntimeConfig) ACPProviderConfig {
	enabled := rt.enabledSet()
	out := ACPProviderConfig{}
	for _, spec := range agentManifests {
		if spec.Kind != TransportACP || spec.ACPType == "" {
			continue
		}
		if !enabled[spec.Name] {
			continue
		}
		if out.Agents == nil {
			out.Agents = make(map[string]config.ACPConfig)
		}
		out.Agents[spec.Name] = spec.ACPConfig(rt.Overrides[spec.Name])
	}
	return out
}

// enabledSet 计算最终启用的业务名集合。
//
// 规则（约定优于配置）：Enabled 为空 = 用默认值，即所有非 OptIn 的 agent。
// 显式列出时**以列表为准**（可以只开一个，也可以把默认开的关掉）。
func (rt AgentRuntimeConfig) enabledSet() map[string]bool {
	out := make(map[string]bool, len(agentManifests))
	if len(rt.Enabled) == 0 {
		for _, spec := range agentManifests {
			out[spec.Name] = !spec.OptIn
		}
		return out
	}
	want := make(map[string]bool, len(rt.Enabled))
	for _, n := range rt.Enabled {
		want[n] = true
	}
	for _, spec := range agentManifests {
		out[spec.Name] = want[spec.Name]
	}
	return out
}

// init 按 manifest 注册 ACP 系 agent 的 AgentSession 工厂：
// agent.Open(Agent:"qoder"/"dsh"/"codebuddy") 即可。
//
// 遍历 manifest 而不是手写清单：接入新 agent 只改 manifest.go 一处。
func init() {
	for _, spec := range agentManifests {
		if spec.Kind != TransportACP {
			continue
		}
		name := spec.Name
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
// 判据是「被启用」，不做进程探活——探活要 spawn 子进程，代价与副作用都不该
// 花在渲染一个下拉框上。真跑不起来时任务会带明确错误失败（ensureACPSession 的 surface 路径）。
//
// 2026-10-22 重构：目录不再由 config.AgentsConfig 逐节手写，而是**从 manifest 派生**。
// 这样"加一个 agent"不再需要在配置里登记（transport/agent_type 这些是代码事实，
// 写进配置只会多一个写错的地方）。启用与否来自 AgentRuntimeConfig。
func AvailableAgents(rt AgentRuntimeConfig) []AgentInfo {
	enabled := rt.enabledSet()
	configured := ACPProviderConfigFromRuntime(rt)

	out := make([]AgentInfo, 0, len(agentManifests))
	for _, spec := range agentManifests {
		if !enabled[spec.Name] {
			continue
		}
		info := AgentInfo{
			Name:             spec.Name,
			DisplayName:      spec.DisplayName,
			Description:      spec.Description,
			Transport:        spec.Transport,
			Capabilities:     []string{"流式输出", "工具审批", "续问", "取消"},
			ImagePrompt:      spec.ImagePrompt,
			GoalMode:         spec.GoalMode,
			GoalStateVisible: spec.GoalStateVisible,
			SessionAlways:    spec.SessionAlways,
			ModelSelectable:  spec.ModelSelectable,
			ColdStartCold:    spec.ColdStartCold,
		}
		// claude 的图像能力取决于**实际** transport：桥能把图转成 SDK content blocks，
		// 而 print 回退是纯文本 prompt 通道。配置决定能力，不是 agent 名决定能力 ——
		// 否则前端会显示加图入口，用户选了图却在发送时失败。
		//
		// 判据直接读覆盖项里的 transport，**不 import claude 包**：claude 包已经
		// import 了本包（见 claude/factory.go），反向依赖会成环。
		if spec.Kind == TransportClaudeBridge {
			if rt.ClaudeTransportOrDefault() == "print" {
				info.Transport = "claude -p"
				info.ImagePrompt = false
			}
		}
		// ACP 系再做一次一致性兜底：只有真的注册了 provider 才进目录，
		// 避免"选择器里有但打不开"。
		if spec.Kind == TransportACP {
			if _, ok := configured.Agents[spec.Name]; !ok {
				continue
			}
		}
		out = append(out, info)
	}
	return out
}

// openACPSession 用现有 ACPAgent 建一个 ACP 会话，并桥接成 AgentSession。
// 注意：NewSession 会懒 spawn agent 进程（如 qodercli --acp / dsh --profile acp），失败返回错误。
// cfg.AgentType 由 acpConfigFor 保证非空，这里不再猜测具体厂商。
func openACPSession(ctx context.Context, cfg config.ACPConfig, p OpenParams) (AgentSession, error) {
	adapter := NewACPAgent(cfg, acpLogger())
	sid, err := adapter.NewSession(ctx, SessionConfig{Cwd: p.Cwd, ResumeFrom: p.ResumeFrom, TaskID: p.TaskID, Model: p.Model})
	if err != nil {
		return nil, err
	}
	return NewSessionAdapter(adapter, sid, Caps{MultiTurnPersistent: true, Streaming: true}), nil
}
