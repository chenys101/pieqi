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
	AgentQoder: "qodercli",
	AgentDsh:   "dsh",
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
}

// AgentClaude / AgentQoder / AgentDsh 可用 agent 的常量名（避免散落的裸串拼错）。
const (
	AgentClaude = "claude"
	AgentQoder  = "qoder"
	AgentDsh    = "dsh"
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
	if a.Claude.Transport == "print" {
		claudeTransport = "claude -p"
	}
	out := []AgentInfo{{
		Name:         AgentClaude,
		DisplayName:  "Claude Code",
		Description:  "Anthropic 官方 Agent，经 SDK 常驻桥驱动（不可用时自动回退 claude -p）",
		Transport:    claudeTransport,
		Capabilities: []string{"流式输出", "工具审批", "续问", "取消"},
	}}
	configured := ACPProviderConfigFromAgents(a)
	if _, ok := configured.Agents[AgentQoder]; ok {
		out = append(out, AgentInfo{
			Name:         AgentQoder,
			DisplayName:  "Qoder CLI",
			Description:  "Qoder 官方 CLI（ACP 协议），可选用限时免费的 Qwen3.8-Flash 模型",
			Transport:    "ACP",
			Capabilities: []string{"流式输出", "工具审批", "续问", "取消"},
		})
	}
	if _, ok := configured.Agents[AgentDsh]; ok {
		out = append(out, AgentInfo{
			Name:         AgentDsh,
			DisplayName:  "DeepSeek Harness",
			Description:  "DeepSeek 官方 agent harness（dsh --profile acp）。模型路由取自 dsh 侧 acp profile 的 pin，出厂 pin 需 DEEPSEEK_API_KEY",
			Transport:    "ACP",
			Capabilities: []string{"流式输出", "工具审批", "续问", "取消"},
		})
	}
	return out
}

// openACPSession 用现有 ACPAgent 建一个 ACP 会话，并桥接成 AgentSession。
// 注意：NewSession 会懒 spawn agent 进程（如 qodercli --acp / dsh --profile acp），失败返回错误。
// cfg.AgentType 由 acpConfigFor 保证非空，这里不再猜测具体厂商。
func openACPSession(ctx context.Context, cfg config.ACPConfig, p OpenParams) (AgentSession, error) {
	adapter := NewACPAgent(cfg, acpProviderCfg.Logger)
	sid, err := adapter.NewSession(ctx, SessionConfig{Cwd: p.Cwd, ResumeFrom: p.ResumeFrom})
	if err != nil {
		return nil, err
	}
	return NewSessionAdapter(adapter, sid, Caps{MultiTurnPersistent: true, Streaming: true}), nil
}
