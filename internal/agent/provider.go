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
