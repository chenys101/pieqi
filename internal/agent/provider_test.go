package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"pieqi/internal/config"
)

func TestOpenQoderProviderRouting(t *testing.T) {
	prev := acpProviderCfg
	defer func() { acpProviderCfg = prev }()

	// 用不存在的 spawn 命令：Open("qoder") 应路由到 ACP provider 并报 spawn 错（而非 ErrUnknownAgent）。
	acpProviderCfg.Agents = map[string]config.ACPConfig{
		AgentQoder: {SpawnCommand: []string{"no-such-qodercli-binary-xyz"}},
	}

	_, err := Open(context.Background(), OpenParams{Agent: "qoder", Cwd: t.TempDir()})
	if err == nil {
		t.Fatal("expected spawn error from qoder provider")
	}
	if errors.Is(err, ErrUnknownAgent) {
		t.Fatal("Open(qoder) should route to ACP provider, got ErrUnknownAgent")
	}
	if !strings.Contains(err.Error(), "no-such-qodercli-binary-xyz") {
		t.Fatalf("error should mention spawn command, got: %v", err)
	}
}

// TestOpenDshProviderRouting 同 qoder：注册名必须能路由到 ACP provider，
// 否则前端选了 dsh 只会拿到 ErrUnknownAgent。
func TestOpenDshProviderRouting(t *testing.T) {
	prev := acpProviderCfg
	defer func() { acpProviderCfg = prev }()

	acpProviderCfg.Agents = map[string]config.ACPConfig{
		AgentDsh: {AgentType: "dsh", SpawnCommand: []string{"no-such-dsh-binary-xyz", "--profile", "acp"}},
	}

	_, err := Open(context.Background(), OpenParams{Agent: "dsh", Cwd: t.TempDir()})
	if err == nil {
		t.Fatal("expected spawn error from dsh provider")
	}
	if errors.Is(err, ErrUnknownAgent) {
		t.Fatal("Open(dsh) should route to ACP provider, got ErrUnknownAgent")
	}
	if !strings.Contains(err.Error(), "no-such-dsh-binary-xyz") {
		t.Fatalf("error should mention spawn command, got: %v", err)
	}
}

// TestACPConfigForPerAgentDefault 钉住「兜底按 agent 区分」：provider 表里没有这个名字时，
// qoder 落到 qodercli、dsh 落到 dsh。曾经共用的一个兜底会让选了 dsh 的会话静默起 qodercli。
func TestACPConfigForPerAgentDefault(t *testing.T) {
	prev := acpProviderCfg
	defer func() { acpProviderCfg = prev }()

	acpProviderCfg = ACPProviderConfig{}
	if got := acpConfigFor(AgentQoder).AgentType; got != "qodercli" {
		t.Fatalf("qoder 兜底 agent_type=%q want qodercli", got)
	}
	if got := acpConfigFor(AgentDsh).AgentType; got != "dsh" {
		t.Fatalf("dsh 兜底 agent_type=%q want dsh（不能串到 qodercli）", got)
	}

	// 显式配置优先于兜底。
	acpProviderCfg.Agents = map[string]config.ACPConfig{AgentDsh: {AgentType: "dsh-acp"}}
	if got := acpConfigFor(AgentDsh).AgentType; got != "dsh-acp" {
		t.Fatalf("显式 agent_type 被兜底覆盖: %q", got)
	}
}

func TestConfigureACPProviders(t *testing.T) {
	prev := acpProviderCfg
	defer func() { acpProviderCfg = prev }()

	ConfigureACPProviders(ACPProviderConfig{Agents: map[string]config.ACPConfig{
		AgentQoder: {AgentType: "codex", SpawnCommand: []string{"codex", "--acp"}},
	}})
	cfg := acpProviderCfg.Agents[AgentQoder]
	if cfg.AgentType != "codex" {
		t.Fatalf("AgentType = %q, want codex", cfg.AgentType)
	}
	if len(cfg.SpawnCommand) != 2 || cfg.SpawnCommand[0] != "codex" {
		t.Fatalf("SpawnCommand not set: %v", cfg.SpawnCommand)
	}

	// 逐个合并：配了 dsh 不能把已登记的 qoder 顶掉。
	ConfigureACPProviders(ACPProviderConfig{Agents: map[string]config.ACPConfig{
		AgentDsh: {AgentType: "dsh"},
	}})
	if acpProviderCfg.Agents[AgentQoder].AgentType != "codex" {
		t.Fatalf("合并后 qoder 丢失: %+v", acpProviderCfg.Agents)
	}
	if acpProviderCfg.Agents[AgentDsh].AgentType != "dsh" {
		t.Fatalf("dsh 未登记: %+v", acpProviderCfg.Agents)
	}
}

// TestACPProviderConfigFromAgents 钉住「哪些配置会注册 provider」——与 AvailableAgents 同一判据。
func TestACPProviderConfigFromAgents(t *testing.T) {
	got := ACPProviderConfigFromAgents(config.AgentsConfig{
		Qoder: config.AgentQoderConfig{Transport: "acp", ACP: config.QoderACPConfig{AgentType: "qodercli"}},
		Dsh:   config.AgentDshConfig{Transport: "acp", ACP: config.DshACPConfig{AgentType: "dsh"}},
	})
	if len(got.Agents) != 2 {
		t.Fatalf("agents=%+v, want qoder + dsh", got.Agents)
	}

	// transport 非 acp / agent_type 空缺都不入表。
	got = ACPProviderConfigFromAgents(config.AgentsConfig{
		Qoder: config.AgentQoderConfig{Transport: "acp", ACP: config.QoderACPConfig{AgentType: "qodercli"}},
		Dsh:   config.AgentDshConfig{Transport: "", ACP: config.DshACPConfig{AgentType: "dsh"}},
	})
	if _, ok := got.Agents[AgentDsh]; ok {
		t.Fatalf("dsh transport 空却入了表: %+v", got.Agents)
	}
	if len(got.Agents) != 1 {
		t.Fatalf("agents=%+v, want 仅 qoder", got.Agents)
	}

	// 两个都没配 → 零值（main.go 据此不注册、也不进 session 驱动路径）。
	if got := ACPProviderConfigFromAgents(config.AgentsConfig{}); got.Agents != nil {
		t.Fatalf("未配置时应为零值, got %+v", got.Agents)
	}
}

// TestAvailableAgents 目录本身即「新任务页能选什么」的唯一事实源：
// 顺序（默认 agent = 首位）与可用性判据都必须钉住。
func TestAvailableAgents(t *testing.T) {
	t.Run("qoder 未配置时只有 claude", func(t *testing.T) {
		got := AvailableAgents(config.AgentsConfig{
			Claude: config.AgentClaudeConfig{Transport: "sdk-bridge"},
		})
		if len(got) != 1 || got[0].Name != AgentClaude {
			t.Fatalf("agents=%+v, want 仅 claude", got)
		}
	})

	t.Run("claude 永远排首位（默认 agent = 首位 = Claude Code）", func(t *testing.T) {
		got := AvailableAgents(config.AgentsConfig{
			Claude: config.AgentClaudeConfig{Transport: "sdk-bridge"},
			Qoder: config.AgentQoderConfig{
				Transport: "acp",
				ACP:       config.QoderACPConfig{AgentType: "qodercli"},
			},
		})
		if len(got) != 2 {
			t.Fatalf("agents=%+v, want 2 个", got)
		}
		if got[0].Name != AgentClaude {
			t.Fatalf("首位 = %q, want %q（默认 agent 取首位）", got[0].Name, AgentClaude)
		}
		if got[1].Name != AgentQoder {
			t.Fatalf("次位 = %q, want %q", got[1].Name, AgentQoder)
		}
		if got[0].DisplayName != "Claude Code" {
			t.Fatalf("claude DisplayName = %q", got[0].DisplayName)
		}
	})

	t.Run("qoder transport 非 acp 时不列", func(t *testing.T) {
		got := AvailableAgents(config.AgentsConfig{
			Claude: config.AgentClaudeConfig{Transport: "sdk-bridge"},
			Qoder:  config.AgentQoderConfig{Transport: "off", ACP: config.QoderACPConfig{AgentType: "qodercli"}},
		})
		if len(got) != 1 || got[0].Name != AgentClaude {
			t.Fatalf("agents=%+v, want 仅 claude", got)
		}
	})

	t.Run("claude print 模式下 transport 描述随之变化", func(t *testing.T) {
		got := AvailableAgents(config.AgentsConfig{
			Claude: config.AgentClaudeConfig{Transport: "print"},
		})
		if got[0].Transport != "claude -p" {
			t.Fatalf("transport=%q, want claude -p", got[0].Transport)
		}
	})

	dshConfig := config.AgentDshConfig{
		Transport: "acp",
		ACP:       config.DshACPConfig{AgentType: "dsh"},
	}

	t.Run("dsh transport=acp 才列，且排在 qoder 之后", func(t *testing.T) {
		got := AvailableAgents(config.AgentsConfig{
			Claude: config.AgentClaudeConfig{Transport: "sdk-bridge"},
			Qoder:  config.AgentQoderConfig{Transport: "acp", ACP: config.QoderACPConfig{AgentType: "qodercli"}},
			Dsh:    dshConfig,
		})
		if len(got) != 3 {
			t.Fatalf("agents=%+v, want 3 个", got)
		}
		if got[2].Name != AgentDsh || got[2].DisplayName != "DeepSeek Harness" {
			t.Fatalf("末位 = %+v, want dsh / DeepSeek Harness", got[2])
		}
		if got[2].Transport != "ACP" {
			t.Fatalf("dsh transport = %q, want ACP", got[2].Transport)
		}
	})

	t.Run("dsh 默认（transport 留空）不注册", func(t *testing.T) {
		got := AvailableAgents(config.AgentsConfig{
			Claude: config.AgentClaudeConfig{Transport: "sdk-bridge"},
			Qoder:  config.AgentQoderConfig{Transport: "acp", ACP: config.QoderACPConfig{AgentType: "qodercli"}},
			Dsh:    config.AgentDshConfig{ACP: config.DshACPConfig{AgentType: "dsh"}},
		})
		for _, a := range got {
			if a.Name == AgentDsh {
				t.Fatalf("transport 留空却列出了 dsh: %+v", got)
			}
		}
	})

	t.Run("只配 dsh 不配 qoder 也成立（dsh 不需要 qoder 陪跑）", func(t *testing.T) {
		got := AvailableAgents(config.AgentsConfig{
			Claude: config.AgentClaudeConfig{Transport: "sdk-bridge"},
			Dsh:    dshConfig,
		})
		if len(got) != 2 || got[1].Name != AgentDsh {
			t.Fatalf("agents=%+v, want claude + dsh", got)
		}
	})
}
