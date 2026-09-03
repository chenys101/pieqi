// preview.go PreviewManager：项目运行态反馈（p0-design.md §7）。
//
// 职责：Project Discovery（识别 vite/next/nuxt 与启动命令、绝不硬编码端口）
// → Lifecycle（spawn / 端口探测 / stop / 异常退出感知）→ Task 级清理。
// 代理（Preview Proxy）在 api/preview.go，本模块只管进程与状态。
//
// 安全边界（ADR-0007：不做浏览器自动化）：
//   - dev server 只绑 127.0.0.1，对外仅经 Proxy 暴露
//   - 子进程环境剔除隧道 token 相关变量，防 dev server 代码读到凭据
package core

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"pieqi/internal/model"

	"go.uber.org/zap"
)

// PreviewState Preview 生命周期状态（p0-design.md §6.2）。
const (
	PreviewUnavailable = "unavailable" // 未识别到可运行的 dev server 形态
	PreviewAvailable   = "available"   // discovery 通过，尚未启动
	PreviewStarting    = "starting"
	PreviewRunning     = "running"
	PreviewStopped     = "stopped"
	PreviewError       = "error"
)

// PreviewProfile Discovery 结果：如何启动这个项目的 dev server。
type PreviewProfile struct {
	Framework string   `json:"framework"` // vite | next | nuxt | node
	Command   []string `json:"command"`  // 完整命令（含参数，不含端口覆盖）
	Port      int      `json:"port"`     // 配置声明的端口（实际以进程输出为准）
	Cwd       string   `json:"cwd"`       // 相对项目根的工作目录
}

// PreviewStatus 查询结果（feedback 总览 / 状态轮询共用）。
type PreviewStatus struct {
	State     string `json:"state"`
	Framework string `json:"framework,omitempty"`
	Port      int    `json:"port,omitempty"`
	Error     string `json:"error,omitempty"`
}

// previewInstance 一个 Task 的 preview 进程句柄。
type previewInstance struct {
	taskID      string
	projectPath string
	framework   string
	port        int
	state       string
	cmd         *exec.Cmd
	logFile     *os.File
	stopped     bool // 用户主动 Stop（区别于异常退出的 error）
}

// PreviewManager 管理 taskID → dev server 进程。
type PreviewManager struct {
	logger *zap.Logger

	mu        sync.Mutex
	instances map[string]*previewInstance
}

// NewPreviewManager 创建 PreviewManager。
func NewPreviewManager(logger *zap.Logger) *PreviewManager {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &PreviewManager{logger: logger, instances: map[string]*previewInstance{}}
}

// Start 为 task 启动 preview：discovery → spawn → 探测端口 → running。
// 已在运行 → no-op；discovery 失败 → 返回错误（state=unavailable）。
// 异步执行：立即返回 starting，调用方轮询 Status。
func (pm *PreviewManager) Start(task *model.Task) error {
	if task == nil || task.WorktreePath == "" {
		return fmt.Errorf("preview: task missing worktree")
	}
	pm.mu.Lock()
	if inst, ok := pm.instances[task.ID]; ok && (inst.state == PreviewRunning || inst.state == PreviewStarting) {
		pm.mu.Unlock()
		return nil // 幂等
	}
	pm.mu.Unlock()

	profile, err := DiscoverPreview(task.WorktreePath)
	if err != nil {
		return err
	}
	go pm.spawn(task.ID, task.WorktreePath, profile)
	return nil
}

// spawn 实际 spawn 流程（goroutine 内）：
// 选空闲端口覆盖 → spawn（127.0.0.1）→ 扫描 stdout 拿真实端口 + 轮询探测 → running。
func (pm *PreviewManager) spawn(taskID, projectPath string, profile PreviewProfile) {
	port, err := freeLocalPort()
	if err != nil {
		pm.markState(taskID, PreviewError, "free port: "+err.Error())
		return
	}

	cwd := filepath.Join(projectPath, profile.Cwd)
	args := append([]string{}, profile.Command...)
	var env []string
	switch profile.Framework {
	case "vite":
		args = append(args, "--port", strconv.Itoa(port), "--strictPort")
	case "next":
		args = append(args, "-p", strconv.Itoa(port))
	case "nuxt":
		args = append(args, "--port", strconv.Itoa(port))
	default:
		env = append(env, fmt.Sprintf("PORT=%d", port))
	}
	env = append(env, previewEnv()...)

	logPath := filepath.Join(defaultFeedbackDataRoot(), fmt.Sprintf("preview-%s.log", taskID))
	_ = os.MkdirAll(filepath.Dir(logPath), 0755)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		pm.markState(taskID, PreviewError, "open log: "+err.Error())
		return
	}

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = cwd
	cmd.Env = env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = logFile.Close()
		pm.markState(taskID, PreviewError, "stdout pipe: "+err.Error())
		return
	}
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		pm.markState(taskID, PreviewError, "spawn: "+err.Error())
		return
	}

	inst := &previewInstance{
		taskID: taskID, projectPath: projectPath,
		framework: profile.Framework, port: port,
		state: PreviewStarting, cmd: cmd, logFile: logFile,
	}
	pm.mu.Lock()
	pm.instances[taskID] = inst
	pm.mu.Unlock()
	pm.logger.Info("preview starting",
		zap.String("task", taskID), zap.String("framework", profile.Framework),
		zap.Int("port", port), zap.Strings("cmd", args))

	// watcher：唯一 cmd.Wait()，感知异常退出
	go func() {
		waitErr := cmd.Wait()
		pm.mu.Lock()
		cur, ok := pm.instances[taskID]
		if ok && cur == inst {
			if waitErr != nil && !inst.stopped {
				inst.state = PreviewError
			} else {
				inst.state = PreviewStopped
			}
		}
		pm.mu.Unlock()
		_ = logFile.Close()
		if waitErr != nil && !inst.stopped {
			pm.logger.Warn("preview exited unexpectedly", zap.String("task", taskID), zap.Error(waitErr))
		}
	}()

	// stdout 扫描：解析真实端口（vite 冲突自愈会打印 Local: http://localhost:PORT/）
	resolved := make(chan int, 1)
	go scanPreviewPort(stdout, resolved, logFile)

	// 探测端口就绪（优先用 stdout 解析出的端口，超时兜底用预定端口）
	deadline := time.Now().Add(90 * time.Second)
	actual := port
	for time.Now().Before(deadline) {
		select {
		case p, ok := <-resolved:
			if ok && p > 0 {
				actual = p
			}
		default:
		}
		if portListening(actual) {
			pm.mu.Lock()
			if cur, ok := pm.instances[taskID]; ok && cur == inst {
				cur.port = actual
				cur.state = PreviewRunning
			}
			pm.mu.Unlock()
			pm.logger.Info("preview running", zap.String("task", taskID), zap.Int("port", actual))
			// 持续排空 stdout 直到进程退出，防管道写端阻塞
			go drainChan(resolved)
			return
		}
		select {
		case <-time.After(300 * time.Millisecond):
		case p, ok := <-resolved:
			if ok && p > 0 {
				actual = p
			}
		}
	}
	pm.Stop(taskID) // 启动超时：收掉进程，state=error
	pm.markState(taskID, PreviewError, "preview port not ready within 90s")
}

// Stop 停止 task 的 preview（SIGTERM→KILL，幂等）。
// 收尸与状态更新由 spawn 的 watcher goroutine（唯一 cmd.Wait）完成，这里只发信号等状态。
func (pm *PreviewManager) Stop(taskID string) {
	pm.mu.Lock()
	inst, ok := pm.instances[taskID]
	if ok {
		inst.stopped = true
	}
	pm.mu.Unlock()
	if !ok || inst.cmd == nil || inst.cmd.Process == nil {
		return
	}
	_ = inst.cmd.Process.Signal(syscall.SIGTERM)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		pm.mu.Lock()
		state := inst.state
		pm.mu.Unlock()
		if state == PreviewStopped || state == PreviewError {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = inst.cmd.Process.Kill()
}

// Status 查询 task 的 preview 状态。无实例时做一次 discovery（available/unavailable）。
func (pm *PreviewManager) Status(task *model.Task) PreviewStatus {
	if task == nil || task.WorktreePath == "" {
		return PreviewStatus{State: PreviewUnavailable}
	}
	pm.mu.Lock()
	inst, ok := pm.instances[task.ID]
	pm.mu.Unlock()
	if ok {
		return PreviewStatus{State: inst.state, Framework: inst.framework, Port: inst.port}
	}
	// 无实例：discovery 判定可运行性
	if _, err := DiscoverPreview(task.WorktreePath); err != nil {
		return PreviewStatus{State: PreviewUnavailable}
	}
	return PreviewStatus{State: PreviewAvailable}
}

// RunningPort 代理用：返回 task 绑定的 127.0.0.1 端口；未运行返回 0。
func (pm *PreviewManager) RunningPort(taskID string) int {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	inst, ok := pm.instances[taskID]
	if !ok || inst.state != PreviewRunning {
		return 0
	}
	return inst.port
}

// CleanupAll 停止全部 preview（服务器关停时）。
func (pm *PreviewManager) CleanupAll() {
	pm.mu.Lock()
	ids := make([]string, 0, len(pm.instances))
	for id := range pm.instances {
		ids = append(ids, id)
	}
	pm.mu.Unlock()
	for _, id := range ids {
		pm.Stop(id)
	}
}

// WatchBus 订阅任务事件：Task 终态 / 删除时自动回收 preview（p0-design.md §6.2）。
func (pm *PreviewManager) WatchBus(bus *EventBus) {
	sub := bus.Subscribe(64)
	go func() {
		for ev := range sub.Chan() {
			switch ev.Type {
			case "task_deleted":
				pm.Stop(ev.TaskID)
			case "task_completed", "task_failed":
				pm.Stop(ev.TaskID)
			case "task_updated":
				if ev.Task != nil && (ev.Task.Status == model.TaskCancelled) {
					pm.Stop(ev.TaskID)
				}
			}
		}
	}()
}

// markState 直接置状态（无实例时登记一个占位实例供 Status 读取）。
func (pm *PreviewManager) markState(taskID, state, errMsg string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	inst, ok := pm.instances[taskID]
	if !ok {
		inst = &previewInstance{taskID: taskID, state: state}
		pm.instances[taskID] = inst
	} else {
		inst.state = state
	}
	inst.state = state
	_ = errMsg
}

// --- Discovery（p0-design.md §7.1） ---

// previewOverrideFile 项目级覆盖配置（.pieqi/preview.json）。
const previewOverrideFile = ".pieqi/preview.json"

// DiscoverPreview 探测 projectPath 的 dev server 形态。识别不了返回错误（→ unavailable，不猜）。
func DiscoverPreview(projectPath string) (PreviewProfile, error) {
	// 0. 项目覆盖文件优先
	if data, err := os.ReadFile(filepath.Join(projectPath, previewOverrideFile)); err == nil {
		var p PreviewProfile
		if jsonUnmarshal(data, &p) == nil && len(p.Command) > 0 {
			return p, nil
		}
	}

	// 1. 探测项目根（及常见前端子目录）最近的 package.json
	candidates := []string{projectPath}
	for _, sub := range []string{"frontend", "web", "app"} {
		candidates = append(candidates, filepath.Join(projectPath, sub))
	}
	var pkgDir string
	for _, dir := range candidates {
		if fi, err := os.Stat(filepath.Join(dir, "package.json")); err == nil && !fi.IsDir() {
			pkgDir = dir
			break
		}
	}
	if pkgDir == "" {
		return PreviewProfile{}, fmt.Errorf("preview: no package.json found")
	}

	pkg := readPackageJSON(filepath.Join(pkgDir, "package.json"))
	runner := detectRunner(pkgDir) // pnpm / yarn / npm

	// 2. Framework 判定
	framework := ""
	for _, f := range []string{"vite", "next", "nuxt"} {
		if pkg.hasDep(f) {
			framework = f
			break
		}
	}
	if framework == "" {
		if _, ok := pkg.Scripts["dev"]; ok {
			framework = "node" // 通用 scripts.dev
		} else {
			return PreviewProfile{}, fmt.Errorf("preview: no dev script found")
		}
	}

	// 3. 端口：框架配置文件声明 → 框架默认值（绝不硬编码 5174 这类项目端口）
	port := detectPort(pkgDir, framework)
	cwd, _ := filepath.Rel(projectPath, pkgDir)

	return PreviewProfile{
		Framework: framework,
		Command:   []string{runner, "run", "dev"},
		Port:      port,
		Cwd:       cwd,
	}, nil
}

// pkgJSON package.json 的最小解析。
type pkgJSON struct {
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

func (p pkgJSON) hasDep(name string) bool {
	if _, ok := p.Dependencies[name]; ok {
		return true
	}
	_, ok := p.DevDependencies[name]
	return ok
}

func readPackageJSON(path string) pkgJSON {
	var pkg pkgJSON
	data, err := os.ReadFile(path)
	if err != nil {
		return pkg
	}
	_ = jsonUnmarshal(data, &pkg)
	return pkg
}

// detectRunner 按 lockfile 选包管理器。
func detectRunner(dir string) string {
	switch {
	case fileExists(filepath.Join(dir, "pnpm-lock.yaml")):
		return "pnpm"
	case fileExists(filepath.Join(dir, "yarn.lock")):
		return "yarn"
	default:
		return "npm"
	}
}

// detectPort 从框架配置文件里解析 port 声明；找不到用框架默认值。
func detectPort(dir, framework string) int {
	var configGlobs []string
	defaults := map[string]int{"vite": 5173, "next": 3000, "nuxt": 3000, "node": 3000}
	switch framework {
	case "vite":
		configGlobs = []string{"vite.config.*"}
	case "next":
		configGlobs = []string{"next.config.*"}
	case "nuxt":
		configGlobs = []string{"nuxt.config.*"}
	}
	portRe := regexp.MustCompile(`port["']?\s*[:=]\s*(\d+)`)
	for _, glob := range configGlobs {
		matches, _ := filepath.Glob(filepath.Join(dir, glob))
		for _, f := range matches {
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			if m := portRe.FindSubmatch(data); m != nil {
				if p, err := strconv.Atoi(string(m[1])); err == nil && p > 0 {
					return p
				}
			}
		}
	}
	return defaults[framework]
}

// --- 小工具 ---

// jsonUnmarshal 局部别名（避免 import 噪音集中）。
func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

// fileExists 路径存在且非目录。
func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// freeLocalPort 探测一个空闲的本地端口（listen :0 后立刻释放，存在被抢的小窗口）。
func freeLocalPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// portListening 探测 127.0.0.1:port 是否有进程监听。
func portListening(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// previewPortRe 匹配 dev server 输出里的 Local URL（vite/next/nuxt 都打印 localhost:PORT）。
var previewPortRe = regexp.MustCompile(`(?:localhost|127\.0\.0\.1):(\d+)`)

// scanPreviewPort 扫描 dev server stdout：解析真实端口 + 落盘日志。
// dev server 常驻不退出，这里持续读直到 EOF（进程退出）。
func scanPreviewPort(stdout io.Reader, resolved chan<- int, logFile *os.File) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	sent := false
	for scanner.Scan() {
		line := scanner.Text()
		_, _ = logFile.WriteString(line + "\n")
		if !sent {
			if m := previewPortRe.FindStringSubmatch(line); m != nil {
				if p, err := strconv.Atoi(m[1]); err == nil && p > 0 {
					resolved <- p
					sent = true // 只报第一个（Local URL）
				}
			}
		}
	}
	close(resolved)
}

// drainChan 持续排空 resolved channel（防 stdout 扫描 goroutine 阻塞在 send）。
func drainChan(ch <-chan int) {
	for range ch { //nolint:revive // 纯排空
	}
}

// previewEnv 构造 preview 子进程环境：
// 继承白名单基础变量，剔除 token 相关（防 dev server 代码读到隧道凭据）。
func previewEnv() []string {
	keepPrefix := []string{"PATH", "HOME", "USER", "LANG", "LC_", "TERM", "SHELL", "TMPDIR", "XDG_", "NODE_", "NPM_"}
	var out []string
	for _, kv := range os.Environ() {
		key := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			key = kv[:i]
		}
		// 剔除凭据类变量：隧道 token / Pieqi 内部变量
		if strings.Contains(key, "TOKEN") || strings.HasPrefix(key, "PIEQI_") || strings.HasPrefix(key, "BRIDGE_") {
			continue
		}
		for _, p := range keepPrefix {
			if key == p || strings.HasPrefix(key, p) {
				out = append(out, kv)
				break
			}
		}
	}
	if len(out) == 0 {
		out = append(out, "PATH="+os.Getenv("PATH"))
	}
	return out
}
