//go:build !windows

package exechide

import "os/exec"

// Command 非 Windows：控制台窗口不是问题，空操作。
func Command(cmd *exec.Cmd) {}
