// Package agent: provider.go 注册 ACP 系 agent（qoder 等）的 AgentSession 工厂。
//
// 与 claude 包（bridge 传输）不同，ACP 系 agent 复用现有 ACPAgent（acp.go）+ sessionAdapter
// （session.go）桥接成 AgentSession：业务层 agent.Open(Agent:"qoder") 即可驱动，
// 底层仍是 qodercli --acp。这是 multi-agent.md §11 P4（qoder 迁到同接口）的工厂层落地点。
package agent

import (
	"context"

	"pieqi/internal/config"

	"go.uber.org/zap"
)

// ACPProviderConfig ACP 系 agent provider 的配置。
type ACPProviderConfig struct {
	Qoder  config.ACPConfig // qodercli 的 ACP 配置（AgentType/SpawnCommand/InitTimeout 等）
	Logger *zap.Logger
}

var acpProviderCfg = ACPProviderConfig{}

// ConfigureACPProviders 设置 ACP 系 agent 的 provider 配置（app 启动时调用；可多次覆盖）。
// Qoder 非空时整体替换；Logger 非空时替换。
func ConfigureACPProviders(c ACPProviderConfig) {
	if c.Qoder.AgentType != "" || len(c.Qoder.SpawnCommand) > 0 {
		acpProviderCfg.Qoder = c.Qoder
	}
	if c.Logger != nil {
		acpProviderCfg.Logger = c.Logger
	}
}

// ACPProviderConfigFromAgents 把 config.AgentsConfig 的 qoder 节转成 provider 配置（main.go 接线用）。
// transport 非 "acp" 时返回零值（调用方不注册即可禁用该 agent）。
func ACPProviderConfigFromAgents(a config.AgentsConfig) ACPProviderConfig {
	if a.Qoder.Transport != "acp" {
		return ACPProviderConfig{}
	}
	return ACPProviderConfig{Qoder: a.Qoder.ACPConfig()}
}

// init 注册 "qoder" 的 AgentSession 工厂：agent.Open(Agent:"qoder") 即可。
func init() {
	RegisterSessionProvider("qoder", func(ctx context.Context, p OpenParams) (AgentSession, error) {
		return openACPSession(ctx, acpProviderCfg.Qoder, p)
	})
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

// AgentClaude / AgentQoder 可用 agent 的常量名（避免散落的裸串拼错）。
const (
	AgentClaude = "claude"
	AgentQoder  = "qoder"
)

// AvailableAgents 按配置算出可被任务选择的 agent 列表（顺序即前端展示顺序，claude 在前）。
//
// 判据是「配置上跑得起来」，不做进程探活——探活要 spawn 子进程，代价与副作用都不该
// 花在渲染一个下拉框上。真跑不起来时任务会带明确错误失败（ensureACPSession 的 surface 路径）。
//   - claude：永远可用。transport=sdk-bridge 时走桥（失败自动回退 print），print 时直连 claude -p。
//   - qoder：transport=acp 且 agent_type 非空（与 main.go 原有的「qoder 已配置」判据一致）。
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
	if a.Qoder.Transport == "acp" && a.Qoder.ACPConfig().AgentType != "" {
		out = append(out, AgentInfo{
			Name:         AgentQoder,
			DisplayName:  "Qoder CLI",
			Description:  "Qoder 官方 CLI（ACP 协议），可选用限时免费的 Qwen3.8-Flash 模型",
			Transport:    "ACP",
			Capabilities: []string{"流式输出", "工具审批", "续问", "取消"},
		})
	}
	return out
}

// openACPSession 用现有 ACPAgent 建一个 ACP 会话，并桥接成 AgentSession。
// 注意：NewSession 会懒 spawn agent 进程（qodercli --acp），失败返回错误。
func openACPSession(ctx context.Context, cfg config.ACPConfig, p OpenParams) (AgentSession, error) {
	if cfg.AgentType == "" {
		cfg.AgentType = "qodercli"
	}
	adapter := NewACPAgent(cfg, acpProviderCfg.Logger)
	sid, err := adapter.NewSession(ctx, SessionConfig{Cwd: p.Cwd, ResumeFrom: p.ResumeFrom})
	if err != nil {
		return nil, err
	}
	return NewSessionAdapter(adapter, sid, Caps{MultiTurnPersistent: true, Streaming: true}), nil
}
