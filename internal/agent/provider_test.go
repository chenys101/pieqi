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
