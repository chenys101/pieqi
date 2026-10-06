//go:build windows

package core

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// spawnDetached 启动新实例，并让它**脱离**当前进程树。
//
// 关键点（Windows）：
//   - DETACHED_PROCESS：新进程不继承控制台。旧实例退出时不会把它带走，
//     也不会因为它持有同一个控制台而在关停时互相干扰。
//   - CREATE_NEW_PROCESS_GROUP：与旧实例分到不同进程组，Ctrl 事件不会串扰。
//
// 刻意**不**用 cmd /c start：那会多一层壳，壳退出时句柄语义更绕，
// 且在新进程起来之前无法确认启动失败。直接 exec 自己更干净。
func (s *SelfRestart) spawnDetached() error {
	cmd := exec.Command(s.execPath)
	// 新实例在**原目录**启动，从而以相对路径找到同一个 config.yaml。
	cmd.Dir = filepath.Dir(s.execPath)
	cmd.Env = os.Environ()
	cmd.Stdin = nil

	// 日志接管：新实例自己会按 PIEQI_CONFIG/默认路径写日志。
	// stdout/stderr 给到空设备，避免它继承我们的句柄导致日志重复或 write-after-close。
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err == nil {
		cmd.Stdout = devNull
		cmd.Stderr = devNull
		defer devNull.Close()
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x00000008, // DETACHED_PROCESS
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	// 不 Wait：新进程必须独立存活。释放句柄即可（Windows 上不 Wait 会留僵尸句柄，
	// 故显式 Release）。
	return cmd.Process.Release()
}

// canDial 探测端口是否已有监听者（新实例是否真的起来了）。
func canDial(addr string) bool {
	// addr 形如 ":3000" —— 补上本地回环主机再拨。
	host := "127.0.0.1"
	if len(addr) > 0 && addr[0] != ':' {
		host = ""
	}
	conn, err := net.DialTimeout("tcp", host+addr, 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
