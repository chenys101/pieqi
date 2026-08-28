// proc.go 管理 claude-sdk-bridge 进程（multi-agent.md §5.4 auto_start）。
// 探活不可达时自动 spawn `node src/index.js`，等健康后复用；关停时优雅终止子进程。
//
// 与 factory.go 的分工：Proc 只负责"桥进程在不在"（spawn/探活/停），不碰会话协议；
// openSession 拿到的是 HTTP 客户端，桥崩了由客户端 Health/事件流失败兜底回退 print。
package claude

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"

	"pieqi/internal/agent/claude/bridge"
	"pieqi/internal/config"

	"go.uber.org/zap"
)

// ProcConfig 桥进程管理配置。
type ProcConfig struct {
	BaseURL     string        // 桥地址（含端口）；探活与 spawn 端口取自它
	Token       string        // 桥鉴权 token；spawn 时注入 BRIDGE_TOKEN
	Dir         string        // 桥源码目录（含 src/index.js）；空 = 自动探测
	NodeCmd     string        // node 可执行；空 = "node"
	IdleTimeout time.Duration // 桥 idle 回收阈值；<=0 用桥默认（30m）
	LogPath     string        // 桥 stdout/stderr 落盘；空 = ~/.pieqi/bridge.log
	Logger      *zap.Logger
}

// Proc 托管一个 claude-sdk-bridge 子进程。
// 幂等：EnsureRunning 多次调用共享同一子进程；子进程退出后 watcher 自动清状态，
// 下次 EnsureRunning 会重新 spawn。
type Proc struct {
	cfg     ProcConfig
	baseURL string

	mu      sync.Mutex
	cmd     *exec.Cmd  // 非 nil = 已 spawn 且未退出（watcher 退出时清空）
	startMu sync.Mutex // 串行化 EnsureRunning 的探活+spawn 流程
}

// ProcStartTimeout spawn 后等健康的最大时长。
const ProcStartTimeout = 15 * time.Second

// NewProc 创建桥进程管理器。BaseURL 空时用默认桥地址。
func NewProc(cfg ProcConfig) *Proc {
	if cfg.BaseURL == "" {
		cfg.BaseURL = bridge.DefaultBaseURL
	}
	if cfg.NodeCmd == "" {
		cfg.NodeCmd = "node"
	}
	if cfg.Logger == nil {
		cfg.Logger = zap.NewNop()
	}
	return &Proc{cfg: cfg, baseURL: cfg.BaseURL}
}

// EnsureRunning 确保桥在运行：
//  1. 已托管且进程存活 → 复用；
//  2. 探活通过（外部/已有桥）→ 直接复用，不 spawn；
//  3. 否则 spawn `node src/index.js` 并等健康；失败返回错误（调用方决定回退 print）。
//
// 幂等、并发安全。
func (p *Proc) EnsureRunning(ctx context.Context) error {
	p.startMu.Lock()
	defer p.startMu.Unlock()

	p.mu.Lock()
	alive := p.cmd != nil
	p.mu.Unlock()
	if alive {
		return nil
	}

	// 短超时探活：已有桥在跑就直接复用。
	if err := p.healthShort(ctx); err == nil {
		return nil
	}

	dir := p.resolveDir()
	if dir == "" {
		return errors.New("claude: bridge dir not found (set agents.claude.bridge.dir or PIEQI_BRIDGE_DIR)")
	}

	// 日志落盘（供排障），stdout/stderr 同一文件。
	logPath := p.cfg.LogPath
	if logPath == "" {
		logPath = filepath.Join(config.DefaultDataRoot(), "bridge.log")
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		return fmt.Errorf("claude: bridge log dir: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("claude: bridge log: %w", err)
	}

	cmd := exec.Command(p.cfg.NodeCmd, "src/index.js")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"BRIDGE_PORT="+p.portStr(),
		"BRIDGE_HOST=127.0.0.1",
		"BRIDGE_TOKEN="+p.cfg.Token,
	)
	if p.cfg.IdleTimeout > 0 {
		cmd.Env = append(cmd.Env, fmt.Sprintf("BRIDGE_IDLE_TIMEOUT_MS=%d", p.cfg.IdleTimeout.Milliseconds()))
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("claude: bridge spawn: %w", err)
	}
	p.mu.Lock()
	p.cmd = cmd
	p.mu.Unlock()

	// watcher：进程退出时收尸并清状态（下次 EnsureRunning 会重新 spawn）。
	go func() {
		_ = cmd.Wait()
		_ = logFile.Close()
		p.mu.Lock()
		if p.cmd == cmd {
			p.cmd = nil
		}
		p.mu.Unlock()
	}()

	if err := p.waitHealth(ctx); err != nil {
		_ = cmd.Process.Kill()
		p.mu.Lock()
		if p.cmd == cmd {
			p.cmd = nil
		}
		p.mu.Unlock()
		return fmt.Errorf("claude: bridge start: %w", err)
	}
	p.cfg.Logger.Info("claude sdk-bridge auto-started",
		zap.String("base_url", p.baseURL), zap.String("dir", dir), zap.String("log", logPath))
	return nil
}

// Running 是否托管了一个桥子进程（且 watcher 尚未清状态）。
func (p *Proc) Running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cmd != nil
}

// Stop 停止托管中的桥子进程。未托管时 no-op。幂等。
// 非 Windows：先 SIGTERM（桥 SIGTERM 优雅关全部会话再退），超时再 KILL。
// Windows：无 POSIX 信号，直接 KILL。收尸由 watcher 完成（本方法不二次 Wait）。
func (p *Proc) Stop(ctx context.Context) error {
	p.mu.Lock()
	cmd := p.cmd
	p.mu.Unlock()
	if cmd == nil {
		return nil
	}
	if runtime.GOOS != "windows" {
		_ = cmd.Process.Signal(syscall.SIGTERM)
	} else {
		_ = cmd.Process.Kill()
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		p.mu.Lock()
		cleared := p.cmd == nil
		p.mu.Unlock()
		if cleared {
			return nil
		}
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return ctx.Err()
		case <-timer.C:
			_ = cmd.Process.Kill()
			timer.Reset(2 * time.Second)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// healthShort 单次短超时探活。
func (p *Proc) healthShort(ctx context.Context) error {
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return bridge.NewClientWithToken(p.baseURL, p.cfg.Token).Health(probeCtx)
}

// waitHealth 轮询探活直到健康或超时（ProcStartTimeout，受 ctx 约束）。
func (p *Proc) waitHealth(ctx context.Context) error {
	deadline := time.Now().Add(ProcStartTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	for time.Now().Before(deadline) {
		if err := p.healthShort(ctx); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return fmt.Errorf("bridge not healthy within %s", ProcStartTimeout)
}

// portStr 从 baseURL 提取端口（spawn 用 BRIDGE_PORT 保持一致）。
func (p *Proc) portStr() string {
	u, err := url.Parse(p.baseURL)
	if err != nil {
		return "18790"
	}
	if u.Port() != "" {
		return u.Port()
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

// resolveDir 定位桥源码目录（含 src/index.js）：显式配置 → 环境变量 → exe/cwd 邻接。
func (p *Proc) resolveDir() string {
	if p.cfg.Dir != "" {
		if dirExists(p.cfg.Dir) {
			return p.cfg.Dir
		}
		p.cfg.Logger.Warn("configured bridge dir missing", zap.String("dir", p.cfg.Dir))
		return ""
	}
	if d := os.Getenv("PIEQI_BRIDGE_DIR"); d != "" && dirExists(d) {
		return d
	}
	// 候选：exe 邻接（打包部署 services/ 同装）/ cwd 邻接（源码开发）
	if exe, err := os.Executable(); err == nil {
		if d := filepath.Join(filepath.Dir(exe), "services", "claude-sdk-bridge"); dirExists(d) {
			return d
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		if d := filepath.Join(cwd, "services", "claude-sdk-bridge"); dirExists(d) {
			return d
		}
	}
	return ""
}

func dirExists(d string) bool {
	fi, err := os.Stat(d)
	return err == nil && fi.IsDir()
}
