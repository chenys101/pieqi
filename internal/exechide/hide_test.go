package exechide

import (
	"os/exec"
	"runtime"
	"testing"
)

func TestCommand_NilSafe(t *testing.T) {
	Command(nil) // 不应 panic
}

func TestCommand_SetsAttrOnWindows(t *testing.T) {
	cmd := exec.Command("echo", "x")
	Command(cmd)
	if runtime.GOOS != "windows" {
		if cmd.SysProcAttr != nil {
			t.Fatalf("non-windows SysProcAttr=%v, want nil", cmd.SysProcAttr)
		}
		return
	}
	if cmd.SysProcAttr == nil {
		t.Fatal("windows: SysProcAttr 应为非 nil")
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Error("windows: HideWindow 应为 true")
	}
	if cmd.SysProcAttr.CreationFlags&0x08000000 == 0 {
		t.Errorf("windows: CreationFlags 应含 CREATE_NO_WINDOW, got %#x", cmd.SysProcAttr.CreationFlags)
	}
}
