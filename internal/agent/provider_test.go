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
	acpProviderCfg.Qoder = config.ACPConfig{SpawnCommand: []string{"no-such-qodercli-binary-xyz"}}

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

func TestConfigureACPProviders(t *testing.T) {
	prev := acpProviderCfg
	defer func() { acpProviderCfg = prev }()

	ConfigureACPProviders(ACPProviderConfig{Qoder: config.ACPConfig{AgentType: "codex", SpawnCommand: []string{"codex", "--acp"}}})
	if acpProviderCfg.Qoder.AgentType != "codex" {
		t.Fatalf("AgentType = %q, want codex", acpProviderCfg.Qoder.AgentType)
	}
	if len(acpProviderCfg.Qoder.SpawnCommand) != 2 || acpProviderCfg.Qoder.SpawnCommand[0] != "codex" {
		t.Fatalf("SpawnCommand not set: %v", acpProviderCfg.Qoder.SpawnCommand)
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
}
