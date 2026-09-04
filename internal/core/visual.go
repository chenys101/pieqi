// visual.go VisualCaptureManager：视觉采集编排（p2-design.md §2，ADR-0006）。
//
// 职责：托管 services/visual-capture（node + Playwright）子进程（Proc 模式：
// 幂等 spawn + /health 探活 + watcher 唯一 Wait + SIGTERM→KILL）、
// 截图落盘（~/.pieqi/previews/<taskID>/screenshots/）、
// console/network 事件窗口（内存 ring，随取随读，服务重启即失——P2 接受）。
//
// 安全约束：只把 127.0.0.1 preview 端口交给采集服务（服务自身也校验 URL 前缀），
// 采集服务不持有隧道 token（同 Preview 子进程约束）。
package core

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"crypto/rand"
	"encoding/hex"

	"go.uber.org/zap"
)

// VisualStartTimeout spawn 后等健康的最大时长。
const VisualStartTimeout = 20 * time.Second

// VisualEventsMax 每 task 的 console/network 事件窗口容量（ring，保留最近 N 条）。
const VisualEventsMax = 200

// Screenshot 一次截图记录（p2-design.md §3.1）。
type Screenshot struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"task_id"`
	PreviewID string    `json:"preview_id"` // preview 实例标识（taskID:port）
	URL       string    `json:"url"`        // /api/tasks/:id/preview/screenshots/<id>.png
	CreatedAt time.Time `json:"created_at"`
}

// ConsoleEntry preview 页面运行时 console 事件（只留 error/warn，§4）。
type ConsoleEntry struct {
	Level string    `json:"level"` // error | warn
	Text  string    `json:"text"`
	At    time.Time `json:"at"`
}

// NetworkEntry 失败的网络请求（只留 4xx/5xx/failed，§5；status=0 = failed）。
type NetworkEntry struct {
	URL    string    `json:"url"`
	Method string    `json:"method"`
	Status int       `json:"status"`
	At     time.Time `json:"at"`
}

// captureResponse visual-capture 服务 /v1/capture 的响应。
type captureResponse struct {
	PNGBase64 string         `json:"png_base64"`
	Console   []ConsoleEntry `json:"console"`
	Network   []NetworkEntry `json:"network"`
	Error     string         `json:"error"`
}

// VisualCaptureManager 托管视觉采集服务与结果窗口。
type VisualCaptureManager struct {
	logger  *zap.Logger
	root    string // <dataRoot>/previews（截图存储根）
	nodeCmd string
	dir     string // services/visual-capture 源码目录；空 = 自动探测
	port    int

	mu      sync.Mutex
	cmd     *exec.Cmd // 非 nil = 已 spawn 且未退出
	startMu sync.Mutex

	eventsMu sync.Mutex
	console  map[string][]ConsoleEntry // taskID → 窗口（ring）
	network  map[string][]NetworkEntry

	client *http.Client
}

// NewVisualCaptureManager 创建视觉采集管理器。root 为截图存储根（空 = ~/.pieqi/previews）。
func NewVisualCaptureManager(logger *zap.Logger, root string) *VisualCaptureManager {
	if root == "" {
		root = filepath.Join(defaultFeedbackDataRoot(), "previews")
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &VisualCaptureManager{
		logger:  logger,
		root:    root,
		nodeCmd: "node",
		port:    18791,
		console: map[string][]ConsoleEntry{},
		network: map[string][]NetworkEntry{},
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

// --- Proc 模式：spawn / 探活 / 关停 ---

// EnsureRunning 确保采集服务在运行（幂等；外部已跑的实例直接复用）。
func (v *VisualCaptureManager) EnsureRunning(ctx context.Context) error {
	v.startMu.Lock()
	defer v.startMu.Unlock()

	v.mu.Lock()
	alive := v.cmd != nil
	v.mu.Unlock()
	if alive {
		return nil
	}
	if v.probeHealth(ctx) == nil {
		return nil // 外部实例已健康
	}

	dir := v.resolveDir()
	if dir == "" {
		return errors.New("visual: service dir not found (set PIEQI_VISUAL_DIR or place services/visual-capture/)")
	}
	logPath := filepath.Join(defaultFeedbackDataRoot(), "visual-capture.log")
	_ = os.MkdirAll(filepath.Dir(logPath), 0755)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("visual: log file: %w", err)
	}

	cmd := exec.Command(v.nodeCmd, "src/index.js")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("VISUAL_PORT=%d", v.port),
		"VISUAL_HOST=127.0.0.1",
	)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("visual: spawn: %w", err)
	}
	v.mu.Lock()
	v.cmd = cmd
	v.mu.Unlock()

	// watcher：唯一 Wait，退出即清状态（下次 EnsureRunning 重新 spawn）。
	go func() {
		_ = cmd.Wait()
		_ = logFile.Close()
		v.mu.Lock()
		if v.cmd == cmd {
			v.cmd = nil
		}
		v.mu.Unlock()
	}()

	if err := v.waitHealth(ctx); err != nil {
		_ = cmd.Process.Kill()
		v.mu.Lock()
		if v.cmd == cmd {
			v.cmd = nil
		}
		v.mu.Unlock()
		return fmt.Errorf("visual: start: %w", err)
	}
	v.logger.Info("visual-capture auto-started", zap.Int("port", v.port), zap.String("dir", dir))
	return nil
}

// Stop 停止托管的采集服务（SIGTERM→KILL；未托管 no-op）。
func (v *VisualCaptureManager) Stop(ctx context.Context) error {
	v.mu.Lock()
	cmd := v.cmd
	v.mu.Unlock()
	if cmd == nil {
		return nil
	}
	if runtime.GOOS != "windows" {
		_ = cmd.Process.Signal(syscall.SIGTERM)
	} else {
		_ = cmd.Process.Kill()
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		v.mu.Lock()
		cleared := v.cmd == nil
		v.mu.Unlock()
		if cleared {
			return nil
		}
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	_ = cmd.Process.Kill()
	return nil
}

func (v *VisualCaptureManager) probeHealth(ctx context.Context) error {
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(probeCtx, http.MethodGet, v.baseURL()+"/health", nil)
	resp, err := v.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health status %d", resp.StatusCode)
	}
	return nil
}

func (v *VisualCaptureManager) waitHealth(ctx context.Context) error {
	deadline := time.Now().Add(VisualStartTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	for time.Now().Before(deadline) {
		if v.probeHealth(ctx) == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return fmt.Errorf("visual-capture not healthy within %s", VisualStartTimeout)
}

func (v *VisualCaptureManager) baseURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", v.port)
}

// resolveDir 定位服务源码目录：显式配置 → 环境变量 → exe/cwd 邻接。
func (v *VisualCaptureManager) resolveDir() string {
	if v.dir != "" {
		if fi, err := os.Stat(v.dir); err == nil && fi.IsDir() {
			return v.dir
		}
		return ""
	}
	if d := os.Getenv("PIEQI_VISUAL_DIR"); d != "" {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			return d
		}
	}
	if exe, err := os.Executable(); err == nil {
		if d := filepath.Join(filepath.Dir(exe), "services", "visual-capture"); dirExists(d) {
			return d
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		if d := filepath.Join(cwd, "services", "visual-capture"); dirExists(d) {
			return d
		}
	}
	return ""
}

// --- Capture：截图 + 事件窗口 ---

// Capture 对运行中的 preview 端口执行一次采集会话：
// 截图落盘 + console/network 事件并入该 task 的内存窗口。
// port 必须为 127.0.0.1 上的 preview 端口（调用方 RunningPort 取得）。
func (v *VisualCaptureManager) Capture(ctx context.Context, taskID string, port int, fullPage bool) (*Screenshot, error) {
	if port <= 0 {
		return nil, errors.New("visual: no running preview")
	}
	if err := v.EnsureRunning(ctx); err != nil {
		return nil, err
	}

	payload, _ := json.Marshal(map[string]any{
		"url":        fmt.Sprintf("http://127.0.0.1:%d/", port),
		"full_page":  fullPage,
		"timeout_ms": 15000,
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, v.baseURL()+"/v1/capture", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("visual: capture request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("visual: read capture: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("visual: capture status %d: %s", resp.StatusCode, truncateRunes(string(body), 200))
	}
	var cr captureResponse
	if err := json.Unmarshal(body, &cr); err != nil {
		return nil, fmt.Errorf("visual: decode capture: %w", err)
	}
	if cr.Error != "" {
		return nil, fmt.Errorf("visual: capture: %s", cr.Error)
	}
	png, err := base64.StdEncoding.DecodeString(cr.PNGBase64)
	if err != nil || len(png) == 0 {
		return nil, errors.New("visual: empty screenshot")
	}

	// 事件窗口并入（ring 截断）
	v.appendEvents(taskID, cr.Console, cr.Network)

	id := randomShotID()
	dir := v.shotDir(taskID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, id+".png"), png, 0644); err != nil {
		return nil, err
	}
	return &Screenshot{
		ID:        id,
		TaskID:    taskID,
		PreviewID: fmt.Sprintf("%s:%d", taskID, port),
		URL:       "/api/tasks/" + taskID + "/preview/screenshots/" + id + ".png",
		CreatedAt: time.Now(),
	}, nil
}

func (v *VisualCaptureManager) appendEvents(taskID string, console []ConsoleEntry, network []NetworkEntry) {
	v.eventsMu.Lock()
	defer v.eventsMu.Unlock()
	v.console[taskID] = append(v.console[taskID], console...)
	if len(v.console[taskID]) > VisualEventsMax {
		v.console[taskID] = v.console[taskID][len(v.console[taskID])-VisualEventsMax:]
	}
	v.network[taskID] = append(v.network[taskID], network...)
	if len(v.network[taskID]) > VisualEventsMax {
		v.network[taskID] = v.network[taskID][len(v.network[taskID])-VisualEventsMax:]
	}
}

// Console 返回 task 的 console 事件窗口（since 之后；since 零值 = 全部）。
func (v *VisualCaptureManager) Console(taskID string, since time.Time) []ConsoleEntry {
	v.eventsMu.Lock()
	defer v.eventsMu.Unlock()
	return filterConsole(v.console[taskID], since)
}

// Network 返回 task 的失败请求窗口（since 之后；since 零值 = 全部）。
func (v *VisualCaptureManager) Network(taskID string, since time.Time) []NetworkEntry {
	v.eventsMu.Lock()
	defer v.eventsMu.Unlock()
	return filterNetwork(v.network[taskID], since)
}

func filterConsole(in []ConsoleEntry, since time.Time) []ConsoleEntry {
	out := make([]ConsoleEntry, 0, len(in))
	for _, e := range in {
		if since.IsZero() || e.At.After(since) {
			out = append(out, e)
		}
	}
	return out
}

// ConsoleSummaryOf 把 console 窗口聚合为 Evidence 摘要（P2 §4/§6）。
// nil-safe：无事件时仍返回零值摘要（前端据此渲染「无错误」）。
func (v *VisualCaptureManager) ConsoleSummaryOf(taskID string, since time.Time) *ConsoleSummary {
	entries := v.Console(taskID, since)
	sum := &ConsoleSummary{Entries: entries}
	for _, e := range entries {
		if e.Level == "error" {
			sum.Errors++
		} else {
			sum.Warnings++
		}
	}
	return sum
}

// NetworkSummaryOf 把失败请求窗口聚合为 Evidence 摘要（P2 §5/§6）。
func (v *VisualCaptureManager) NetworkSummaryOf(taskID string, since time.Time) *NetworkSummary {
	entries := v.Network(taskID, since)
	return &NetworkSummary{Failures: len(entries), Entries: entries}
}

// AttachVisual 把视觉证据并入 Evidence（P2 §6）：
// 最新 maxShots 张截图 URL（时间倒序）+ console/network 全窗口摘要。
// maxShots <= 0 时不挂截图（仍挂 console/network）。
func (v *VisualCaptureManager) AttachVisual(ev *Evidence, maxShots int) {
	if shots := v.ListScreenshots(ev.TaskID); len(shots) > 0 && maxShots > 0 {
		n := len(shots)
		if n > maxShots {
			n = maxShots
		}
		for _, shot := range shots[:n] {
			ev.Screenshots = append(ev.Screenshots, shot.URL)
		}
	}
	ev.Console = v.ConsoleSummaryOf(ev.TaskID, time.Time{})
	ev.Network = v.NetworkSummaryOf(ev.TaskID, time.Time{})
}

// WatchBus 订阅任务事件：task 删除时回收截图与事件窗口（与 PreviewManager 同模式）。
func (v *VisualCaptureManager) WatchBus(bus *EventBus) {
	sub := bus.Subscribe(64)
	go func() {
		for ev := range sub.Chan() {
			if ev.Type == "task_deleted" {
				v.Cleanup(ev.TaskID)
			}
		}
	}()
}

func filterNetwork(in []NetworkEntry, since time.Time) []NetworkEntry {
	out := make([]NetworkEntry, 0, len(in))
	for _, e := range in {
		if since.IsZero() || e.At.After(since) {
			out = append(out, e)
		}
	}
	return out
}

// --- 截图存储 ---

// shotIDPattern 合法截图 id（防路径逃逸：URL 路径参数不可信输入）。
var shotIDPattern = regexp.MustCompile(`^[a-f0-9]{16}$`)

// ListScreenshots 列出该 task 的截图（创建时间倒序：最新在前）。
func (v *VisualCaptureManager) ListScreenshots(taskID string) []Screenshot {
	entries, err := os.ReadDir(v.shotDir(taskID))
	if err != nil {
		return nil
	}
	var shots []Screenshot
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".png")
		if !shotIDPattern.MatchString(id) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		shots = append(shots, Screenshot{
			ID:        id,
			TaskID:    taskID,
			URL:       "/api/tasks/" + taskID + "/preview/screenshots/" + id + ".png",
			CreatedAt: info.ModTime(),
		})
	}
	sort.Slice(shots, func(i, j int) bool { return shots[i].CreatedAt.After(shots[j].CreatedAt) })
	return shots
}

// ScreenshotPath 截图文件路径（id 校验防逃逸）。不存在返回 ok=false。
func (v *VisualCaptureManager) ScreenshotPath(taskID, id string) (string, bool) {
	if !shotIDPattern.MatchString(id) {
		return "", false
	}
	p := filepath.Join(v.shotDir(taskID), id+".png")
	if _, err := os.Stat(p); err != nil {
		return "", false
	}
	return p, true
}

// Cleanup 删除该 task 的全部截图与事件窗口（task 删除时调用）。幂等。
func (v *VisualCaptureManager) Cleanup(taskID string) {
	_ = os.RemoveAll(v.shotDir(taskID))
	v.eventsMu.Lock()
	delete(v.console, taskID)
	delete(v.network, taskID)
	v.eventsMu.Unlock()
}

func (v *VisualCaptureManager) shotDir(taskID string) string {
	return filepath.Join(v.root, taskID, "screenshots")
}

func randomShotID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func dirExists(d string) bool {
	fi, err := os.Stat(d)
	return err == nil && fi.IsDir()
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
