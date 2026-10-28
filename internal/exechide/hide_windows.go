//go:build windows

package exechide

import (
	"os/exec"
	"syscall"
)

// CREATE_NO_WINDOW：控制台子系统进程不弹黑窗。
// 与 DETACHED_PROCESS（自重启用）不同 —— 这里只藏窗口，仍可接管 stdin/stdout 管道。
const createNoWindow = 0x08000000

// Command 给后台子进程打上"不弹控制台窗口"。
//
// 详情页 / 标题补齐 / 模型清单探测都会 spawn claude、qodercli、dsh.cmd、node；
// Windows 上不设这一项就会连闪多扇 cmd。自重启走 DETACHED_PROCESS，**不要**对本函数
// 与 CreationFlags 混用（见 core/selfrestart_windows.go）。
//
// 注意：CREATE_NO_WINDOW 下没有附着控制台，部分壳（Windows PowerShell 5.1 / 未显式
// 设编码的 pwsh）会按 OEM 代码页写 stdout。生产 CLI（claude / node）通常自带 UTF-8；
// 测试里若用 pwsh 冒充，须在 -Command 里显式设 [Console]::OutputEncoding。
func Command(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}
