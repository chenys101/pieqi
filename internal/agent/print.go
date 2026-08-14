// Package agent: print.go 实现 PrintAgent——把 Phase 1 的 `claude -p` + stream-json
// 驱动包装成一个 AgentAdapter 实现，作为 ACP 适配器不可用时的回退。
//
// 本任务（4a）只新增 agent 包文件，不改 Phase 1 代码（task_runner.go/stream_event.go），
// 不改 TaskRunner 行为。PrintAgent 自包含 stream-json 解析（print_stream.go），不 import core，
// 避免循环依赖。
//
// 设计要点：
//   - one-shot 模式：NewSession 只登记会话（cwd + uuid sessionID + ran=false），不 spawn；
//     SendPrompt 才 spawn claude 子进程跑一轮。
//   - 首轮/续轮区分：同 sessionID 第一次 SendPrompt 用 `-p <prompt> --session-id <sid>`；
//     后续用 `--resume <sid> -p <prompt>`（map[sessionID]*printSession.ran 区分）。
//   - 审批不经 adapter：claude -p 走外部 PreToolUse hook + HookService（由 4b AgentManager 接管），
//     故 OnPermissionRequest 为 no-op，Approve/Deny 返回 ErrNotSupported。
//   - 可测性：spawn 函数可注入（spawnFunc 字段）；dispatchLine 为独立可测方法。
package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// PrintConfig PrintAgent 配置（claude -p + stream-json 回退路径）。
type PrintConfig struct {
	Binary         string // claude 可执行文件，默认 "claude"
	Model          string // --model（空则不附加）
	SysPrompt      string // --append-system-prompt（空则不附加）
	PermissionMode string // --permission-mode，默认 "bypassPermissions"
}

// ErrNoConversation PrintAgent 续轮 --resume 时 claude 报会话不存在（"No conversation found"）。
// 调用方可据此回退为新会话重跑（保留 Phase 1 兜底）。
var ErrNoConversation = errors.New("print: claude session not found (No conversation found)")

// PrintAgent claude -p + stream-json 的 AgentAdapter 实现（ACP 适配器不可用时的回退）。
//
// 包装 Phase 1 TaskRunner.runClaude/parseStream/handleLine 的驱动逻辑，但自包含 stream-json
// 解析（print_stream.go），接口对齐 ACPAgent。一次只驱动一个 prompt turn（一个 running 进程）；
// 多 turn 由 AgentManager（4b）串行调 SendPrompt。
type PrintAgent struct {
	cfg    PrintConfig
	logger *zap.Logger
	spawn  spawnFunc // 可注入：默认 defaultSpawn（exec.Command），测试用 fake

	// 会话登记：sessionID -> 会话状态（cwd / 是否跑过首轮 / 真实 claude session id）
	sessMu sync.Mutex
	sess   map[string]*printSession

	// 当前 running 进程（一次只一个 prompt turn）。Cancel/Close 经它杀进程。
	procMu  sync.Mutex
	running *runningProc

	// done 在 Close 或进程异常退出时关闭（仿 ACPAgent.done）。
	done      chan struct{}
	closeOnce sync.Once // 守护 Close 幂等
	doneOnce  sync.Once // 守护 done 关闭（与 closeOnce 分离，避免 markDone 重入）

	// 回调（OnXxx 注册；并发读用 cbMu 保护）
	cbMu       sync.RWMutex
	onDelta    ContentDeltaFunc
	onPerm     PermissionRequestFunc // PrintAgent 路径下不实际使用（no-op 注册）
	onToolCall ToolCallUpdateFunc
}

// printSession 一个 claude 会话的登记信息。
type printSession struct {
	cwd string
	// ran 是否已跑过首轮（区分 --session-id vs --resume）。
	// 首次 SendPrompt 用 -p <prompt> --session-id <sid>；后续用 --resume <sid> -p <prompt>。
	ran bool
	// realSessionID 由 system init 行捕获（claude 真实会话 id，可能 ≠ 预生成 uuid）。
	// 续轮 --resume 用它而非预生成 uuid，避免 "No conversation found"。
	realSessionID string
}

// runningProc 一次 SendPrompt 的 running 进程信息（仿 Phase 1 liveProc）。
//
// 为可测试性，proc 用 claudeProcess 接口而非 *exec.Cmd 直接持有——
// 默认实现 execProcess 包 *exec.Cmd，测试可注入 fake。
// stdin 留着供 4b InjectToolResult 用（4a 暂不写 stdin）。
type runningProc struct {
	proc   claudeProcess
	stdin  io.WriteCloser
	cancel context.CancelFunc
}

// claudeProcess 抽象 claude 子进程，便于注入 fake（测试用）。
// 默认实现 execProcess 包装 *exec.Cmd；测试可注入自实现。
type claudeProcess interface {
	Stdout() io.Reader
	Stderr() string
	Wait() error
	Kill() error
}

// spawnFunc 由 PrintAgent 调用 spawn claude 进程（可注入）。
// 返回 claudeProcess + stdin（4b 写 stdin 用）+ error。
type spawnFunc func(ctx context.Context, name string, args []string, dir string) (claudeProcess, io.WriteCloser, error)

// 编译期断言：PrintAgent 实现 AgentAdapter。
var _ AgentAdapter = (*PrintAgent)(nil)

// NewPrintAgent 创建 PrintAgent。
// 不 spawn 进程；进程在 SendPrompt 时按需 spawn。
func NewPrintAgent(cfg PrintConfig, logger *zap.Logger) *PrintAgent {
	if cfg.Binary == "" {
		cfg.Binary = "claude"
	}
	if cfg.PermissionMode == "" {
		cfg.PermissionMode = "bypassPermissions"
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &PrintAgent{
		cfg:    cfg,
		logger: logger,
		spawn:  defaultSpawn,
		sess:   make(map[string]*printSession),
		done:   make(chan struct{}),
	}
}

// --- AgentAdapter 实现 ---

// NewSession 登记 claude 会话（PrintAgent 是 one-shot 模式，不在此 spawn 进程）。
// 生成 uuid sessionID 返回；后续 SendPrompt 用它驱动 claude。
//
// cfg.ResumeFrom 非空时为续问：预填 realSessionID=ResumeFrom 且 ran=true，让首次 SendPrompt
// 走 --resume <ResumeFrom> 复用已有会话上下文。若 claude 报 "No conversation found"，
// SendPrompt 返回 ErrNoConversation（调用方可据此回退为新会话重跑）。
func (a *PrintAgent) NewSession(ctx context.Context, cfg SessionConfig) (string, error) {
	if cfg.Cwd == "" {
		return "", errors.New("print: NewSession requires Cwd")
	}
	sid := uuid.New().String()
	s := &printSession{cwd: cfg.Cwd}
	if cfg.ResumeFrom != "" {
		// 续问：预填真实 session id，让首次 SendPrompt 走 --resume <ResumeFrom>
		s.realSessionID = cfg.ResumeFrom
		s.ran = true
	}
	a.sessMu.Lock()
	a.sess[sid] = s
	a.sessMu.Unlock()
	return sid, nil
}

// RealSessionID 返回会话的真实 session ID（用于持久化与续问）。
// 返回 system init 报告的真实 claude session ID（首次 SendPrompt 后才有）；
// 之前返回 sessionID 本身。NewSession 时若 ResumeFrom 非空，返回 ResumeFrom。
func (a *PrintAgent) RealSessionID(sessionID string) string {
	a.sessMu.Lock()
	defer a.sessMu.Unlock()
	if s, ok := a.sess[sessionID]; ok && s.realSessionID != "" {
		return s.realSessionID
	}
	return sessionID
}

// SendPrompt spawn claude 进程跑一轮 prompt，逐行解析 stream-json 派发回调。阻塞到进程退出。
//
// 参数构建（仿 TaskRunner.buildArgs）：
//   - 首轮（同 sessionID 第一次）：-p <prompt> --session-id <sid>
//   - 续轮（同 sessionID 之前已跑过）：--resume <sid> -p <prompt>（sid 取 system init 报告的真实 id）
//   - 追加 --model/--permission-mode/--output-format stream-json --verbose；SysPrompt 非空加 --append-system-prompt
//
// 派发：
//   - text 块 → OnContentDelta(IsThought=false)
//   - thinking 块 → OnContentDelta(IsThought=true)
//   - tool_use 块 → OnToolCallUpdate(IsNew=true)
//   - tool_result 块 → OnToolCallUpdate(IsNew=false, Status=completed/failed)
//   - system init → 更新内部真实 sessionID（续轮 --resume 用）
//   - result 行 → 记录最终结果
//
// 退出码非 0 或 result.is_error 或 result.subtype 非 success → 返回 error（带 stderr/result 摘要）。
func (a *PrintAgent) SendPrompt(ctx context.Context, sessionID, prompt string) error {
	a.sessMu.Lock()
	sess, ok := a.sess[sessionID]
	a.sessMu.Unlock()
	if !ok {
		return fmt.Errorf("print: unknown session %q (call NewSession first)", sessionID)
	}

	// TrimSpace：PWA 文本框常带末尾换行，claude -p 收到尾部 \n 会退出码 0 却不产任何输出
	// （仿 TaskRunner.run，防尾部 \n 空输出）。
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return errors.New("print: empty prompt")
	}

	// 续轮用 system init 报告的真实 claude session id（若有），避免 "No conversation found"。
	claudeSid := sessionID
	if sess.realSessionID != "" {
		claudeSid = sess.realSessionID
	}
	args := a.buildArgs(sess.ran, claudeSid, prompt)

	// spawn 进程（默认 exec；测试可注入 fake spawn）
	procCtx, cancel := context.WithCancel(ctx)
	proc, stdin, err := a.spawn(procCtx, a.cfg.Binary, args, sess.cwd)
	if err != nil {
		cancel()
		return fmt.Errorf("print: spawn claude: %w", err)
	}

	rp := &runningProc{proc: proc, stdin: stdin, cancel: cancel}
	a.setRunning(rp)
	defer a.clearRunning(rp)

	// 逐行解析 stdout，派发回调；同时捕获 result 事件与 system init 事件
	resultEvt, systemInitEvt := a.parseAndDispatch(sessionID, proc.Stdout())

	// 等进程退出
	waitErr := proc.Wait()
	cancel() // 释放 procCtx 资源

	// 退出码非 0：返回 error（带 stderr 摘要）。
	// 续轮 --resume 会话丢失（claude stderr 含 "No conversation found"）→ ErrNoConversation，
	// 调用方可据此回退为新会话重跑（保留 Phase 1 兜底）。
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			stderr := proc.Stderr()
			if isNoConversationErr(stderr) {
				return fmt.Errorf("%w: %s", ErrNoConversation, truncateStderr(stderr))
			}
			return fmt.Errorf("print: claude exit %d: %s", exitErr.ExitCode(), truncateStderr(stderr))
		}
		// ctx 取消 / 其他 wait 错误；也检测 stderr（保险）
		stderr := proc.Stderr()
		if isNoConversationErr(stderr) {
			return fmt.Errorf("%w: %s", ErrNoConversation, truncateStderr(stderr))
		}
		return fmt.Errorf("print: claude wait: %w (stderr: %s)", waitErr, truncateStderr(stderr))
	}

	// result.is_error 或 subtype 非 success → 返回 error（仿 TaskRunner.handleLine）
	if resultEvt != nil && resultEvt.IsError {
		return fmt.Errorf("print: result subtype=%s: %s", resultEvt.Subtype, truncateStderr(resultEvt.Text))
	}
	if resultEvt != nil && resultEvt.Subtype != "" && resultEvt.Subtype != "success" {
		return fmt.Errorf("print: result subtype=%s: %s", resultEvt.Subtype, truncateStderr(resultEvt.Text))
	}

	// system init 行报告的真实 session id：记到 sess，续轮 --resume 用它
	if systemInitEvt != nil && systemInitEvt.SessionID != "" {
		a.sessMu.Lock()
		if sess.realSessionID == "" {
			sess.realSessionID = systemInitEvt.SessionID
		}
		a.sessMu.Unlock()
	}
	// result 行也可能带 session_id（与 system init 一致），兜底记录
	if resultEvt != nil && resultEvt.SessionID != "" {
		a.sessMu.Lock()
		if sess.realSessionID == "" {
			sess.realSessionID = resultEvt.SessionID
		}
		a.sessMu.Unlock()
	}

	// 标记已跑过首轮（续轮走 --resume）
	a.sessMu.Lock()
	sess.ran = true
	a.sessMu.Unlock()
	return nil
}

// buildArgs 构建 claude 命令行参数（仿 TaskRunner.buildArgs）。
//
// 首轮：-p <prompt> --session-id <sid> 创建会话
// 续轮：--resume <sid> -p <prompt> 复用历史上下文（--session-id 不能复用已存在会话）
// 追加：--output-format stream-json --verbose；Model/PermissionMode/SysPrompt 非空时附加。
func (a *PrintAgent) buildArgs(isResume bool, sessionID, prompt string) []string {
	args := make([]string, 0, 12)
	if isResume {
		args = append(args, "--resume", sessionID, "-p", prompt)
	} else {
		args = append(args, "-p", prompt, "--session-id", sessionID)
	}
	args = append(args, "--output-format", "stream-json", "--verbose")
	if a.cfg.Model != "" {
		args = append(args, "--model", a.cfg.Model)
	}
	if a.cfg.PermissionMode != "" {
		args = append(args, "--permission-mode", a.cfg.PermissionMode)
	}
	if a.cfg.SysPrompt != "" {
		args = append(args, "--append-system-prompt", a.cfg.SysPrompt)
	}
	return args
}

// parseAndDispatch 逐行读取 stdout，派发回调。
// 返回 result 事件（若有）与 system init 事件（若有），供 SendPrompt 处理。
func (a *PrintAgent) parseAndDispatch(sessionID string, stdout io.Reader) (resultEvt, systemInitEvt *printStreamEvent) {
	// 1MB 缓冲防长行截断（仿 TaskRunner.parseStream）
	reader := bufio.NewReaderSize(stdout, 1024*1024)
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			r, s := a.dispatchLine(sessionID, line)
			if r != nil {
				resultEvt = r
			}
			if s != nil {
				systemInitEvt = s
			}
		}
		if err != nil {
			if err != io.EOF {
				a.logger.Warn("print: read stream", zap.Error(err))
			}
			return resultEvt, systemInitEvt
		}
	}
}

// dispatchLine 解析一行 stream-json 并派发到回调（独立可测方法）。
//
// 返回该行的 result 事件与 system init 事件（若有）；其他事件直接派发到回调。
// sessionID 用于回调的 SessionID 字段（即 NewSession 返回的 uuid，非 claude 真实 id）。
func (a *PrintAgent) dispatchLine(sessionID, line string) (resultEvt, systemInitEvt *printStreamEvent) {
	events := parsePrintStreamLine(line)
	for i := range events {
		ev := &events[i]
		switch ev.Kind {
		case printEventSystemInit:
			systemInitEvt = ev
		case printEventAssistantText:
			a.fireDelta(ContentDelta{SessionID: sessionID, Text: ev.Text, IsThought: false})
		case printEventAssistantThinking:
			a.fireDelta(ContentDelta{SessionID: sessionID, Text: ev.Text, IsThought: true})
		case printEventAssistantToolUse:
			a.fireToolCall(ToolCallUpdateInfo{
				SessionID:  sessionID,
				IsNew:      true,
				ToolCallID: ev.ToolCallID,
				Title:      ev.ToolName,
				RawInput:   ev.ToolInput,
			})
		case printEventUserToolResult:
			status := "completed"
			if ev.IsError {
				status = "failed"
			}
			// RawOutput 是 json.RawMessage；ToolOutput 是文本，需 JSON 编码为合法 JSON 字符串
			a.fireToolCall(ToolCallUpdateInfo{
				SessionID:  sessionID,
				IsNew:      false,
				ToolCallID: ev.ToolCallID,
				Status:     status,
				RawOutput:  jsonString(ev.ToolOutput),
			})
		case printEventResult:
			resultEvt = ev
		}
	}
	return resultEvt, systemInitEvt
}

// OnContentDelta 注册内容增量回调。
func (a *PrintAgent) OnContentDelta(fn ContentDeltaFunc) {
	a.cbMu.Lock()
	a.onDelta = fn
	a.cbMu.Unlock()
}

// OnToolCallUpdate 注册工具调用更新回调。
func (a *PrintAgent) OnToolCallUpdate(fn ToolCallUpdateFunc) {
	a.cbMu.Lock()
	a.onToolCall = fn
	a.cbMu.Unlock()
}

// OnPermissionRequest PrintAgent 审批不经 adapter：claude -p 走外部 PreToolUse hook + HookService，
// 由 4b 的 AgentManager 接管。此处为 no-op：忽略回调注册，不存储。
//
// 与 ACPAgent 不同（ACP 协议把 RequestPermission 作为一等公民），
// claude -p 路径的审批经 HookService 阻塞等决策，PrintAgent 不参与。
func (a *PrintAgent) OnPermissionRequest(fn PermissionRequestFunc) {
	// no-op：PrintAgent 审批由 AgentManager 经 HookService 处理，不经 adapter 接口。
}

// Approve PrintAgent 审批不经 adapter（走 HookService），不支持。
// 4b 的 AgentManager 直接调 HookService.Resolve 处理审批，绕过本方法。
func (a *PrintAgent) Approve(ctx context.Context, reqID, optionID string) error {
	return ErrNotSupported
}

// Deny PrintAgent 审批不经 adapter（走 HookService），不支持。
// 4b 的 AgentManager 直接调 HookService.Resolve 处理审批，绕过本方法。
func (a *PrintAgent) Deny(ctx context.Context, reqID string) error {
	return ErrNotSupported
}

// InjectToolResult PrintAgent 不支持：append_prompt 由 AgentManager 经 Resume 路由（4b 处理）。
//
// PrintAgent 是 one-shot 模式，每轮 SendPrompt 跑完进程即退出。
// 续问（追加 prompt）由 AgentManager 串行调 SendPrompt(--resume) 实现，
// 不需要在进程存活期间写 stdin 注入 tool_result。
func (a *PrintAgent) InjectToolResult(ctx context.Context, sessionID, toolCallID string, result string, isError bool) error {
	return ErrNotSupported
}

// Cancel 取消正在进行的 prompt turn：杀当前 running claude 进程（若有）。
// 没有 running 进程时 no-op（不报错，便于 AgentManager 无脑调）。
func (a *PrintAgent) Cancel(ctx context.Context, sessionID string) error {
	a.procMu.Lock()
	rp := a.running
	a.procMu.Unlock()
	if rp == nil {
		return nil
	}
	rp.cancel()
	_ = rp.proc.Kill()
	return nil
}

// Close 关闭 adapter：杀进程 + 标记 done。幂等。
func (a *PrintAgent) Close(ctx context.Context) error {
	a.closeOnce.Do(func() {
		a.procMu.Lock()
		rp := a.running
		a.procMu.Unlock()
		if rp != nil {
			rp.cancel()
			_ = rp.proc.Kill()
		}
		a.markDone()
	})
	return nil
}

// Done 返回进程退出/Close 时关闭的 channel（仿 ACPAgent.done）。
//
// PrintAgent 一次只驱动一个 prompt turn，SendPrompt 自身阻塞等进程退出，
// 故 done 主要由 Close 触发；多 turn 复用同一 adapter 时 done 不在每轮关闭。
func (a *PrintAgent) Done() <-chan struct{} { return a.done }

// --- 内部辅助 ---

func (a *PrintAgent) markDone() {
	a.doneOnce.Do(func() { close(a.done) })
}

func (a *PrintAgent) setRunning(rp *runningProc) {
	a.procMu.Lock()
	a.running = rp
	a.procMu.Unlock()
}

func (a *PrintAgent) clearRunning(rp *runningProc) {
	a.procMu.Lock()
	if a.running == rp {
		a.running = nil
	}
	a.procMu.Unlock()
}

func (a *PrintAgent) fireDelta(d ContentDelta) {
	a.cbMu.RLock()
	fn := a.onDelta
	a.cbMu.RUnlock()
	if fn != nil {
		fn(d)
	}
}

func (a *PrintAgent) fireToolCall(u ToolCallUpdateInfo) {
	a.cbMu.RLock()
	fn := a.onToolCall
	a.cbMu.RUnlock()
	if fn != nil {
		fn(u)
	}
}

// --- 默认 spawn 实现（生产路径） ---

// execProcess 包装 *exec.Cmd 实现 claudeProcess。
// 同时实现 io.Writer 用作 cmd.Stderr（并发安全）。
type execProcess struct {
	cmd      *exec.Cmd
	stdout   io.Reader
	stderrMu sync.Mutex
	stderr   strings.Builder
}

func (p *execProcess) Stdout() io.Reader { return p.stdout }
func (p *execProcess) Stderr() string {
	p.stderrMu.Lock()
	defer p.stderrMu.Unlock()
	return p.stderr.String()
}
func (p *execProcess) Wait() error { return p.cmd.Wait() }
func (p *execProcess) Kill() error {
	if p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}

// Write 实现 io.Writer，供 cmd.Stderr 写入（并发安全）。
func (p *execProcess) Write(b []byte) (int, error) {
	p.stderrMu.Lock()
	defer p.stderrMu.Unlock()
	return p.stderr.Write(b)
}

// defaultSpawn 默认 spawn 实现：exec.Command + StdoutPipe + StderrPipe + StdinPipe。
func defaultSpawn(ctx context.Context, name string, args []string, dir string) (claudeProcess, io.WriteCloser, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	proc := &execProcess{}
	cmd.Stderr = proc // proc 实现 io.Writer
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("print: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("print: stdout pipe: %w", err)
	}
	proc.cmd = cmd
	proc.stdout = stdout
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("print: start claude: %w", err)
	}
	return proc, stdin, nil
}

// jsonString 把 string 编码为合法 JSON 字符串（带引号），用作 RawOutput。
// 空 string 返回 nil（避免 RawOutput 为 ""）。
func jsonString(s string) json.RawMessage {
	if s == "" {
		return nil
	}
	b, err := json.Marshal(s)
	if err != nil {
		return nil
	}
	return b
}

// truncateStderr 截断错误文本到上限（仿 TaskRunner.truncateErr，避免 stderr 撑爆 error）。
func truncateStderr(s string) string {
	const max = 2000
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "\n…(已截断)"
}

// isNoConversationErr 检测 claude -p 续轮 --resume 时会话不存在的标志串。
// 兼容大小写与子串匹配（claude 实测 stderr 含 "No conversation found"）。
func isNoConversationErr(stderr string) bool {
	return strings.Contains(strings.ToLower(stderr), "no conversation found")
}
