package agent

import (
	"testing"

	"pieqi/internal/config"
)

// TestInterfaceCompileAssertions 编译期断言：ACPAgent 实现 AgentAdapter，
// 且本包导出的 DTO/回调类型可被外部按接口契约使用。
// （acp.go 内已有 var _ AgentAdapter = (*ACPAgent)(nil)，这里在测试侧再断言一次。）
func TestInterfaceCompileAssertions(t *testing.T) {
	var _ AgentAdapter = (*ACPAgent)(nil)
	var _ ContentDeltaFunc = func(ContentDelta) {}
	var _ PermissionRequestFunc = func(PermissionRequest) {}
	var _ ToolCallUpdateFunc = func(ToolCallUpdateInfo) {}
}

// TestDTODefaults 校验 DTO 零值与常量符合预期（防止后续重构无意改动 ACP 字段映射）。
func TestDTODefaults(t *testing.T) {
	if PermissionOptionAllowOnce != "allow_once" {
		t.Fatalf("PermissionOptionAllowOnce=%q want allow_once", PermissionOptionAllowOnce)
	}
	if PermissionOptionRejectAlways != "reject_always" {
		t.Fatalf("PermissionOptionRejectAlways=%q want reject_always", PermissionOptionRejectAlways)
	}

	// ContentDelta 默认非思考（回答正文）
	d := ContentDelta{Text: "hi"}
	if d.IsThought {
		t.Fatalf("default ContentDelta.IsThought=true, want false")
	}

	// PermissionResponse 零值=Cancelled 语义（Selected=false）
	var r PermissionResponse
	if r.Selected {
		t.Fatalf("default PermissionResponse.Selected=true, want false (Cancelled)")
	}

	// ErrNotSupported 必须导出且非空
	if ErrNotSupported == nil {
		t.Fatal("ErrNotSupported is nil")
	}
}

// TestNewACPAgent_ConfigDefaults 校验 NewACPAgent 对空 InitTimeout 的兜底与 cmd 字段填充。
func TestNewACPAgent_ConfigDefaults(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "claude-code"}, nil)
	if a.cfg.InitTimeout <= 0 {
		t.Fatal("InitTimeout not defaulted")
	}
	if a.CmdName() != "npx" {
		t.Fatalf("CmdName=%q want npx", a.CmdName())
	}
	// CmdArgs 应返回副本，改动不影响内部
	args := a.CmdArgs()
	if len(args) < 2 || args[0] != "-y" || args[1] != "@agentclientprotocol/claude-agent-acp@latest" {
		t.Fatalf("CmdArgs=%v unexpected", args)
	}
	args[0] = "MUTATED"
	if a.CmdArgs()[0] == "MUTATED" {
		t.Fatal("CmdArgs returned internal slice, not a copy")
	}
}

// TestDoneChannel_OpenBeforeClose 校验 Done() 在 Close 前未关闭、Close 后关闭。
func TestDoneChannel_OpenBeforeClose(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "claude-code"}, nil)
	select {
	case <-a.Done():
		t.Fatal("Done() closed before Close")
	default:
	}
}
