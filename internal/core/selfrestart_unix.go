//go:build !windows

package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// spawnDetached 启动新实例并使其脱离当前进程（Unix 侧）。
//
// 与 Windows 版的对称点：都要保证新实例**不随旧实例一起死**。
// Unix 上靠 Setsid —— 新进程自成会话首领，不接收旧进程终端的挂断信号。
func (s *SelfRestart) spawnDetached() error {
	cmd := exec.Command(s.execPath)
	cmd.Dir = filepath.Dir(s.execPath)
	cmd.Env = os.Environ()
	cmd.Stdin = nil

	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err == nil {
		cmd.Stdout = devNull
		cmd.Stderr = devNull
		defer devNull.Close()
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
