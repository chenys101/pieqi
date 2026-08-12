package core

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"pieqi/internal/model"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// TaskRunner 跑单个 Task 的 claude 子进程：建 worktree -> 注入 PreToolUse hook settings
// -> spawn claude（stream-json）-> 解析事件 -> 状态转换 -> Publish。
// PreToolUse hook 经 HookService 阻塞等人类决策。
type TaskRunner struct {
	logger    *zap.Logger
	store     *TaskStore
	wm        *WorktreeManager
	bus       *EventBus
	hooks     *HookService
	baseBranch string // worktree 基准分支，默认 "main"
	model     string
	sysPrompt string
	permissionMode string
	cleanupWorktrees bool

	// hook 注入：仅 permissionMode==bypassPermissions 时写 settings.json
	execPath       string   // bridge 可执行文件绝对路径，os.Executable() 取
	port           int      // 主进程端口，hook 子进程回连 /internal/hook 用
	hookTools      []string // 拦截的工具名，如 ["Bash","Write","Edit","NotebookEdit"]
	hookTimeoutSec int      // hook 等决策上限（秒），应 ≥ HookService 超时

	// 每项目并发上限：maxConcurrent<=0 表示不限制
	maxConcurrent int
	projSems      sync.Map // projectID -> *semaphore

	// notify IM 回执回调（由 Bridge 注入，避免循环依赖）。
	// task 进 waiting_input/完成/失败时，若有 OriginChannel 则往原渠道 push 通知。
	notify func(task *model.Task, text string)

	mu      sync.Mutex
	running map[string]*liveProc // taskID -> 活跃进程
}

type liveProc struct {
	taskID  string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	cancel  context.CancelFunc
	done    chan struct{} // claude 进程退出
}

// NewTaskRunner 创建 runner。
// execPath/port/hookTools/hookTimeoutSec 用于往 worktree 注入 PreToolUse hook settings；
// 仅 permissionMode=="bypassPermissions" 时注入（plan 模式下 claude 自身会 ask）。
// maxConcurrent 为每项目并发上限，<=0 表示不限制。
// baseBranch 为 worktree 基准分支，空则默认 "main"。
func NewTaskRunner(logger *zap.Logger, store *TaskStore, wm *WorktreeManager, bus *EventBus, hooks *HookService, model, sysPrompt, permissionMode string, cleanupWorktrees bool, execPath string, port int, hookTools []string, hookTimeoutSec, maxConcurrent int, baseBranch string) *TaskRunner {
	if permissionMode == "" {
		permissionMode = "bypassPermissions"
	}
	if hookTimeoutSec <= 0 {
		hookTimeoutSec = 1800 // 30min，与 HookService 默认对齐
	}
	if baseBranch == "" {
		baseBranch = "main"
	}
	tr := &TaskRunner{
		logger:           logger,
		store:            store,
		wm:               wm,
		bus:              bus,
		hooks:            hooks,
		baseBranch:       baseBranch,
		model:            model,
		sysPrompt:        sysPrompt,
		permissionMode:   permissionMode,
		cleanupWorktrees: cleanupWorktrees,
		execPath:         execPath,
		port:             port,
		hookTools:        hookTools,
		hookTimeoutSec:   hookTimeoutSec,
		maxConcurrent:    maxConcurrent,
		running:          make(map[string]*liveProc),
	}
	// hook 触发时置 waiting_input（hook 是 waiting_input 的权威信号，见 plan A.6）
	if hooks != nil {
		hooks.SetOnPending(func(taskID, toolUseID, toolName, summary string) {
			tr.setWaitingInput(taskID, toolUseID, toolName, summary)
		})
	}
	return tr
}

// SetNotifier 注入 IM 回执回调（由 Bridge 调用，避免 TaskRunner->Bridge 循环依赖）。
// task 进 waiting_input/完成/失败时回调被触发；notify 可为 nil（无 IM 渠道时）。
func (tr *TaskRunner) SetNotifier(fn func(task *model.Task, text string)) {
	tr.notify = fn
}

// semaphore 轻量计数信号量，用于每项目并发上限。
type semaphore struct {
	ch chan struct{}
}

func newSemaphore(n int) *semaphore {
	return &semaphore{ch: make(chan struct{}, n)}
}

// acquire 阻塞直到拿到一个槽位。n<=0 时 sem 为 nil，acquire 是空操作。
func (s *semaphore) acquire() {
	if s == nil {
		return
	}
	s.ch <- struct{}{}
}

// release 归还一个槽位。s 为 nil 时空操作。
func (s *semaphore) release() {
	if s == nil {
		return
	}
	select {
	case <-s.ch:
	default:
	}
}

// projectSem 返回 projectID 对应的信号量；maxConcurrent<=0 时返回 nil（不限制）。
func (tr *TaskRunner) projectSem(projectID string) *semaphore {
	if tr.maxConcurrent <= 0 {
		return nil
	}
	v, _ := tr.projSems.LoadOrStore(projectID, newSemaphore(tr.maxConcurrent))
	return v.(*semaphore)
}

// Start 异步启动一个任务：建 worktree -> spawn claude -> 流式解析。
// 调用方传入已 Create 的 task（status=pending）。
//
// 注意：任务生命周期独立于触发它的 HTTP 请求/IM 消息，故用 context.Background()
// 派生，而非调用方传入的 ctx（请求结束后 ctx 会被 cancel，会杀掉 git/claude 子进程）。
// 任务的取消由 Cancel 方法通过内部 cancel 控制。
// Start 启动一轮新任务：建 worktree -> spawn claude（stream-json）。
// ctx 仅用于派生，实际运行用 context.Background()（见 run 注释）。
func (tr *TaskRunner) Start(ctx context.Context, task *model.Task) {
	go tr.run(context.Background(), task, "")
}

// Resume 在已结束（completed/failed/cancelled）的任务上续问：复用同一 ClaudeSessionID
// 与 worktree，用补充文本作为新 prompt 重跑一轮 claude。--session-id 让 claude 续上下文。
//
// 也接受路径 B 的 waiting_input（choice kind）：Claude 文本提问后进程已 end_turn 退出，
// 用户选完选项续跑。路径 A 的 waiting_input（approval kind）进程仍活着挂 hook channel，
// 不允许 Resume（会起第二个 claude 进程争抢 session），应走 Intervene。
func (tr *TaskRunner) Resume(taskID, text string) error {
	t, ok := tr.store.Get(taskID)
	if !ok {
		return fmt.Errorf("task not found: %s", taskID)
	}
	// 路径 B 的 choice waiting_input 也可 resume；路径 A 的 approval waiting_input 不行
	resumable := t.Status == model.TaskCompleted || t.Status == model.TaskFailed ||
		t.Status == model.TaskCancelled ||
		(t.Status == model.TaskWaitingInput && t.CurrentDecision != nil &&
			t.CurrentDecision.Kind == model.DecisionKindChoice)
	if !resumable {
		return fmt.Errorf("task not resumable: %s", t.Status)
	}
	if t.WorktreePath == "" || t.ClaudeSessionID == "" {
		return fmt.Errorf("task missing worktree or session, cannot resume")
	}
	// 双保险：确认进程确实已死（不在 tr.running），避免对路径 A 误 resume 起第二个 claude。
	tr.mu.Lock()
	_, alive := tr.running[taskID]
	tr.mu.Unlock()
	if alive {
		return fmt.Errorf("task still has a live process, use intervene instead")
	}
	// 追加一条 user 事件，标记续问起点（前端渲染为右对齐气泡，与首次 prompt 一致）
	tr.appendEvent(taskID, model.TaskEvent{Type: model.EventUser, Text: text})
	go tr.run(context.Background(), t, text)
	return nil
}

// GenerateTitleAsync 异步生成任务的一句话标题（大模型摘要）。
// 不阻塞任务创建流程：spawn 一个轻量 claude -p 摘要 prompt，成功后写入 task.Title 并推送。
// 失败静默保留前端启发式标题（titleText 智能截断），不影响主流程。
func (tr *TaskRunner) GenerateTitleAsync(taskID string) {
	go tr.generateTitle(taskID)
}

func (tr *TaskRunner) generateTitle(taskID string) {
	t, ok := tr.store.Get(taskID)
	if !ok || t == nil || strings.TrimSpace(t.Prompt) == "" {
		return
	}
	dir := t.WorktreePath
	if dir == "" {
		dir = t.ProjectPath
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	prompt := "用不超过 20 个字的一句话概括下面这个开发任务，直接输出标题本身，不要引号、不要前缀、不要解释：\n\n" + t.Prompt
	cmd := exec.CommandContext(ctx, "claude", "-p", prompt, "--model", tr.model, "--output-format", "text")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		tr.logger.Debug("generate title", zap.String("task", taskID), zap.Error(err))
		return
	}
	title := cleanTitle(string(out))
	if title == "" {
		return
	}
	updated, err := tr.store.Update(taskID, func(t *model.Task) bool {
		if t.Title == title {
			return false
		}
		t.Title = title
		return true
	})
	if err != nil {
		tr.logger.Debug("store title", zap.String("task", taskID), zap.Error(err))
		return
	}
	if updated != nil {
		tr.bus.Publish(Event{Type: "task_updated", TaskID: taskID, Task: updated})
	}
}

// cleanTitle 清洗模型生成的标题：去引号/前后缀/多余空白，压成单行并限长。
func cleanTitle(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"'"“”‘’`)
	s = strings.TrimPrefix(s, "标题：")
	s = strings.TrimPrefix(s, "标题:")
	s = strings.TrimSpace(s)
	// 折叠空白为单行
	s = strings.Join(strings.Fields(s), " ")
	// 拒绝"模型没理解任务"的元回复（如"你的消息里没有附带要概括的开发任务内容"）。
	// 这类文本不是标题，命中则返回空串，调用方保留前端启发式标题，避免污染任务列表。
	for _, phrase := range []string{"没有附带", "请把", "请提供", "请说明", "请告诉我", "无法概括", "发给我", "undefined"} {
		if strings.Contains(s, phrase) {
			return ""
		}
	}
	runes := []rune(s)
	const max = 30
	if len(runes) > max {
		s = string(runes[:max]) + "…"
	}
	return strings.TrimSpace(s)
}

// run 跑一轮 claude 子进程。resumePrompt 非空表示是续问（用补充文本而非 task.Prompt）。
//
// 每项目并发上限：阻塞等槽位（任务仍是 pending 状态，直到获得槽位才往下走）
func (tr *TaskRunner) run(parentCtx context.Context, task *model.Task, resumePrompt string) {
	sem := tr.projectSem(task.ProjectID)
	sem.acquire()
	defer sem.release()

	// 项目直接用 task 自带的 ProjectID/ProjectPath（不再预注册）。
	// BaseBranch 统一取配置（默认 main）。
	project := &model.Project{ID: task.ProjectID, RepoPath: task.ProjectPath, BaseBranch: tr.baseBranch}
	if task.WorktreePath == "" {
		wtPath, err := tr.wm.Create(parentCtx, project, task.ID)
		if err != nil {
			tr.failTask(task.ID, "create worktree: "+err.Error())
			return
		}
		var updated *model.Task
		updated, err = tr.store.Update(task.ID, func(t *model.Task) bool {
			t.WorktreePath = wtPath
			return true
		})
		if err != nil {
			tr.failTask(task.ID, "store worktree path: "+err.Error())
			return
		}
		task = updated
	}

	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	tr.injectHookSettings(task)

	// TrimSpace：PWA 文本框常带末尾换行，claude -p 收到尾部 \n 会退出码 0 却不产任何输出
	// （空输出被兜底标成 completed）。发给 claude 前统一去首尾空白，覆盖所有来源。
	prompt := strings.TrimSpace(task.Prompt)
	isResume := resumePrompt != ""
	if isResume {
		prompt = strings.TrimSpace(resumePrompt)
	}

	// 首轮：claude -p <prompt> --session-id <id> 创建会话
	// 首轮/续问参数构建。
	// 首轮：claude -p <prompt> --session-id <id> 创建会话
	// 续问：claude --resume <id> -p <prompt> 复用历史会话上下文
	// （--session-id 不能复用一个已存在会话，会报 "Session ID already in use"）
	buildArgs := func(resume bool, sessionID string) []string {
		args := []string{}
		if resume {
			args = append(args, "--resume", sessionID, "-p", prompt)
		} else {
			args = append(args, "-p", prompt, "--session-id", sessionID)
		}
		args = append(args,
			"--model", tr.model,
			"--permission-mode", tr.permissionMode,
			"--output-format", "stream-json",
			"--verbose",
		)
		if tr.sysPrompt != "" {
			args = append(args, "--append-system-prompt", tr.sysPrompt)
		}
		return args
	}

	// 跑一轮 claude：负责管道、running 注册、流式解析与退出等待。
	// 返回 (退出错误, stderr 文本, stdout 行数, stdout 字节数)。
	runClaude := func(resume bool, sessionID string) (error, string, int, int) {
		cmd := exec.CommandContext(ctx, "claude", buildArgs(resume, sessionID)...)
		cmd.Dir = task.WorktreePath

		stdin, err := cmd.StdinPipe()
		if err != nil {
			return fmt.Errorf("stdin pipe: %w", err), "", 0, 0
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return fmt.Errorf("stdout pipe: %w", err), "", 0, 0
		}
		stderrBuf := newConcurrentBuffer()
		cmd.Stderr = stderrBuf

		if err := cmd.Start(); err != nil {
			return fmt.Errorf("start claude: %w", err), "", 0, 0
		}

		lp := &liveProc{taskID: task.ID, cmd: cmd, stdin: stdin, cancel: cancel, done: make(chan struct{})}
		tr.mu.Lock()
		tr.running[task.ID] = lp
		tr.mu.Unlock()
		defer func() {
			tr.mu.Lock()
			delete(tr.running, task.ID)
			tr.mu.Unlock()
		}()

		tr.setRunning(task.ID)

		// 流式解析 stdout；返回读取行数/字节数，供空输出诊断
		streamLines, streamBytes := tr.parseStream(task.ID, stdout)

		// 等 claude 退出
		waitErr := cmd.Wait()
		close(lp.done)
		return waitErr, stderrBuf.String(), streamLines, streamBytes
	}

	// 首轮用 --session-id，续问用 --resume。若续问发现会话丢失
	// （"No conversation found"，坏运行/代理差异导致 id 与实际不一致），
	// 回退为新会话重跑一次，保证补充消息仍能被回答（即使丢失历史上下文）。
	claudeErr, stderrStr, streamLines, streamBytes := runClaude(isResume, task.ClaudeSessionID)
	if claudeErr != nil && isResume && strings.Contains(stderrStr, "No conversation found") {
		tr.logger.Warn("resume session not found, fallback to fresh session",
			zap.String("task", task.ID), zap.String("session", task.ClaudeSessionID))
		newID := uuid.New().String()
		tr.store.Update(task.ID, func(t *model.Task) bool {
			t.ClaudeSessionID = newID
			return true
		})
		claudeErr, stderrStr, streamLines, streamBytes = runClaude(false, newID)
	}
	if claudeErr != nil {
		tr.failTask(task.ID, fmt.Sprintf("claude exit: %v\nstderr: %s", claudeErr, stderrStr))
		return
	}
	// 正常退出但未拿到 result 事件：若任务确实没产出任何内容（无文本/工具事件、空 output），
	// 不静默标 completed —— 否则会出现"会话无响应"却无任何提示。改为标 failed 并带出 stdout
	// 统计与 stderr，便于定位（如 825667b3 这类 claude 干活了但 stream 一条没捕获的情况）。
	// 若已有内容但缺 result 事件，保持原行为（视为完成）。
	if t, ok := tr.store.Get(task.ID); ok && t.Status != model.TaskCompleted && t.Status != model.TaskFailed {
		if t.Output == "" && !taskHasContent(t) {
			tr.failTask(task.ID, fmt.Sprintf(
				"claude 退出码 0 但未产出任何内容（stdout %d 行/%d 字节）\nstderr: %s",
				streamLines, streamBytes, truncateErr(stderrStr)))
		} else {
			tr.completeTask(task.ID, t.Output)
		}
	}

	if tr.cleanupWorktrees {
		_ = tr.wm.Cleanup(context.Background(), project, task.ID, task.WorktreePath)
	}
}

// injectHookSettings 往 task 的 worktree 写 .claude/settings.json，配置 PreToolUse hook。
// 仅 bypassPermissions 时注入（plan 模式 claude 自身会 ask）。失败仅 warn，不阻塞主流程。
func (tr *TaskRunner) injectHookSettings(task *model.Task) {
	if tr.permissionMode != "bypassPermissions" || tr.execPath == "" || len(tr.hookTools) == 0 {
		return
	}
	if task.WorktreePath == "" {
		return
	}
	hookCmd := buildHookCmd(tr.execPath, task.ID, tr.port)
	if err := WriteHookSettings(task.WorktreePath, hookCmd, tr.hookTools, tr.hookTimeoutSec); err != nil {
		tr.logger.Warn("write hook settings", zap.String("task", task.ID), zap.Error(err))
	}
}

// parseStream 逐行读取 stream-json 并转换状态。返回读取的行数与字节数，
// 供空输出诊断（claude 退出 0 却没产出时，用此区分"stdout 真空"还是"事件被丢"）。
func (tr *TaskRunner) parseStream(taskID string, stdout io.Reader) (lines, bytes int) {
	reader := bufio.NewReaderSize(stdout, 1024*1024) // 1MB 缓冲防长行截断
	pendingToolUses := map[string]string{} // tool_use id -> tool name，供 tool_result 关联

	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			lines++
			bytes += len(line)
			tr.handleLine(taskID, line, pendingToolUses)
		}
		if err != nil {
			if err != io.EOF {
				tr.logger.Warn("read stream", zap.String("task", taskID), zap.Error(err))
			}
			return lines, bytes
		}
	}
}

func (tr *TaskRunner) handleLine(taskID, line string, pending map[string]string) {
	sl, err := parseStreamLine(line)
	if err != nil || sl == nil {
		return
	}

	switch sl.Type {
	case "system":
		// init 行报告 claude 真实会话 id。首轮可能因坏运行/代理差异未按预生成 uuid 建会话，
		// 这里捕获真实 id 覆盖 ClaudeSessionID，保证后续 --resume 能找到会话（避免 "No conversation found"）。
		if sl.Subtype == "init" && sl.SessionID != "" {
			tr.store.Update(taskID, func(t *model.Task) bool {
				if t.ClaudeSessionID == sl.SessionID {
					return false
				}
				t.ClaudeSessionID = sl.SessionID
				return true
			})
		}
	case "assistant":
		msg, err := sl.extractMessage()
		if err != nil || msg == nil {
			return
		}
		for _, b := range msg.Content {
			switch b.Type {
			case "tool_use":
				// tool_use 出现在流里 = PreToolUse hook 已放行；记 id->name 供 tool_result 关联
				if pending != nil {
					pending[b.ID] = b.Name
				}
				tr.appendEvent(taskID, model.TaskEvent{
					Type: model.EventToolUse, ToolName: b.Name, ToolUseID: b.ID, Input: b.Input,
				})
			case "thinking":
				// thinking 块内容在 b.Thinking（不在 b.Text），空串则跳过
				if strings.TrimSpace(b.Thinking) != "" {
					tr.appendEvent(taskID, model.TaskEvent{Type: model.EventThinking, Text: b.Thinking})
				}
			case "text":
				if strings.TrimSpace(b.Text) != "" {
					tr.appendEvent(taskID, model.TaskEvent{Type: model.EventText, Text: b.Text})
				}
			}
		}
	case "user":
		msg, err := sl.extractMessage()
		if err != nil || msg == nil {
			return
		}
		for _, b := range msg.Content {
			if b.Type == "tool_result" {
				name := ""
				if pending != nil {
					name = pending[b.ToolUseID]
					delete(pending, b.ToolUseID)
				}
				tr.appendEvent(taskID, model.TaskEvent{
					Type: model.EventToolResult, ToolName: name, ToolUseID: b.ToolUseID,
					Result: toolResultText(b), IsError: b.IsError,
				})
			}
		}
	case "result":
		r, err := sl.extractResult()
		if err != nil || r == nil {
			return
		}
		if r.IsError || r.Subtype != "success" {
			tr.failTask(taskID, fmt.Sprintf("result subtype=%s: %s", r.Subtype, r.Result))
			return
		}
		// 路径 B：Claude 文本提问（[CHOICE] 格式）触发，拦截 completeTask 改置 waiting_input。
		// 命中则 claude 进程已 end_turn 退出，天然不耗 token，用户选完走 Resume 续跑。
		if tr.maybePauseForChoice(taskID, r.Result) {
			return
		}
		tr.completeTask(taskID, r.Result)
	}
}

// Intervene 对 waiting_input 任务投递决策或追加 prompt。
func (tr *TaskRunner) Intervene(taskID string, in model.Intervention) error {
	if in.Kind == "decision" {
		if err := tr.hooks.Resolve(taskID, in.DecisionID, in.Choice); err != nil {
			return err
		}
		// hook 已放行/拒绝，claude 会继续执行；task 从 waiting_input 切回 running。
		// （deny 时 claude 收到拒绝也会继续走别的路，仍是 running 而非卡住）
		tr.setRunning(taskID)
		return nil
	}
	// append_prompt：写新 user 消息到 claude stdin（stream-json）
	tr.mu.Lock()
	lp, ok := tr.running[taskID]
	tr.mu.Unlock()
	if !ok {
		return fmt.Errorf("task not running: %s", taskID)
	}
	userMsg := buildStreamUserMessage(in.Text)
	if _, err := lp.stdin.Write(append([]byte(userMsg), '\n')); err != nil {
		return fmt.Errorf("write stdin: %w", err)
	}
	tr.setRunning(taskID)
	return nil
}

// Cancel 取消任务：杀 claude 进程。
//
// 路径 B 的 waiting_input（choice）进程已死、不在 tr.running，直接置 cancelled
// 不依赖杀进程。路径 A 的 waiting_input 进程仍活着，走原杀进程逻辑。
func (tr *TaskRunner) Cancel(taskID string) error {
	tr.mu.Lock()
	lp, ok := tr.running[taskID]
	tr.mu.Unlock()
	if !ok {
		// 进程已死（路径 B 的 choice waiting_input，或终态任务）：直接置 cancelled
		updated := tr.transition(taskID, model.TaskCancelled, func(t *model.Task) {
			if t.Status == model.TaskWaitingInput || t.Status == model.TaskRunning {
				now := time.Now()
				t.FinishedAt = &now
				return
			}
		})
		if updated != nil {
			return nil
		}
		return fmt.Errorf("task not cancellable: %s", taskID)
	}
	lp.cancel()
	tr.store.Update(taskID, func(t *model.Task) bool {
		if t.Status == model.TaskRunning || t.Status == model.TaskWaitingInput {
			t.Status = model.TaskCancelled
			now := time.Now()
			t.FinishedAt = &now
			return true
		}
		return false
	})
	tr.bus.Publish(Event{Type: "task_updated", TaskID: taskID})
	return nil
}

// --- 状态转换 ---

func (tr *TaskRunner) setRunning(taskID string) {
	tr.transition(taskID, model.TaskRunning, func(t *model.Task) {
		t.CurrentDecision = nil
		t.Error = "" // 续问时清掉之前的错误
		t.FinishedAt = nil // 从终态恢复，重新进入运行
		now := time.Now()
		if t.StartedAt == nil {
			t.StartedAt = &now
		}
	})
}

func (tr *TaskRunner) setWaitingInput(taskID, decisionID, toolName, summary string) {
	updated := tr.transition(taskID, model.TaskWaitingInput, func(t *model.Task) {
		t.CurrentDecision = &model.Decision{
			ID: decisionID, Kind: model.DecisionKindApproval,
			ToolName: toolName, Summary: summary,
			Options: []string{"approve", "deny"}, CreatedAt: time.Now(),
		}
	})
	if updated != nil {
		tr.notifyWaitingInput(updated)
	}
}

// setWaitingInputChoice 路径 B：Claude 文本提问触发，进程已 end_turn 退出。
// options 为候选选项列表，question 作为 Summary。ID 生成新 uuid（路径 B 无 tool_use id）。
func (tr *TaskRunner) setWaitingInputChoice(taskID, question string, options []string) {
	updated := tr.transition(taskID, model.TaskWaitingInput, func(t *model.Task) {
		t.CurrentDecision = &model.Decision{
			ID: uuid.New().String(), Kind: model.DecisionKindChoice,
			Summary: question, Options: options, CreatedAt: time.Now(),
		}
	})
	if updated != nil {
		tr.notifyWaitingInput(updated)
	}
}

// maybePauseForChoice 路径 B 的拦截入口：检查 task 最后一个 text 事件是否匹配
// [CHOICE] 提问格式，命中则置 waiting_input(choice) 拦截 completeTask。
//
// 当前已禁用（直接返回 false）：非 Claude 后端（deepseek）对 [CHOICE] 格式遵从不稳定
// 且自创变体，拦截命中率低且误判风险累积。改为纯文本方案拍板协议（见 ChoicePromptSection），
// 模型列方案+★标推荐，任务正常 completed，用户续问回复选择。解析代码 parseChoiceBlock
// 等保留，将来换真 Claude 后端可复活路径 B 弹窗（改回 return 真实判断即可）。
func (tr *TaskRunner) maybePauseForChoice(taskID, fallbackOutput string) bool {
	_ = taskID
	_ = fallbackOutput
	return false
}

// maybePauseForChoiceDisabled 是路径 B 拦截的原始实现，当前未启用（见上方禁用说明）。
// 保留以便将来复活：换真 Claude 后端后，把 maybePauseForChoice 改为调用本函数即可恢复
// [CHOICE] 格式拦截 + waiting_input(choice) + Resume 续跑的弹窗路径。
func (tr *TaskRunner) maybePauseForChoiceDisabled(taskID, fallbackOutput string) bool {
	t, ok := tr.store.Get(taskID)
	if !ok || t == nil {
		return false
	}
	// 取最后一个 text 事件（appendEvent 的 200ms 合并保证提问文本是完整一块）
	var lastText string
	for i := len(t.Events) - 1; i >= 0; i-- {
		if t.Events[i].Type == model.EventText {
			lastText = t.Events[i].Text
			break
		}
	}
	if lastText == "" {
		lastText = fallbackOutput // 兜底：旧任务无 events 或 text 全在 output
	}
	pc, remainder := parseChoiceBlock(lastText)
	if pc == nil {
		return false
	}
	// 命中：若有前导正文，把最后那个 text event 改成只含正文（剥离 CHOICE 块，
	// 避免前端把格式串当回答渲染）。remainder 为空则删掉该空 text event。
	// （只在确实有 text event 且非空时改写；纯 CHOICE 无正文且 lastText 来自 fallback 时跳过。）
	if lastText != "" {
		tr.transition(taskID, "", func(t *model.Task) {
			if len(t.Events) == 0 {
				return
			}
			last := &t.Events[len(t.Events)-1]
			if last.Type != model.EventText {
				return
			}
			if remainder == "" {
				// 纯 CHOICE 块无正文：删掉这个空 text event
				t.Events = t.Events[:len(t.Events)-1]
				return
			}
			last.Text = remainder
		})
	}
	tr.setWaitingInputChoice(taskID, pc.Question, pc.Options)
	return true
}

// notifyWaitingInput 往 IM 原渠道推送「需决策/选择」通知，让手机端也能收到。
// 无 OriginChannel（HTTP/CLI 来源）或未注入 notify 时静默跳过。
// 按 Decision.Kind 分文案：choice 列出候选选项，approval 提示 approve/deny。
func (tr *TaskRunner) notifyWaitingInput(t *model.Task) {
	if tr.notify == nil || t.OriginChannel == "" || t.OriginChatID == "" || t.CurrentDecision == nil {
		return
	}
	id := t.ID
	if len(id) > 8 {
		id = id[:8]
	}
	cd := t.CurrentDecision
	var text string
	if cd.Kind == model.DecisionKindChoice {
		var b strings.Builder
		fmt.Fprintf(&b, "❓ 任务 #%s 需要你选择\n%s\n\n", id, cd.Summary)
		for _, opt := range cd.Options {
			fmt.Fprintf(&b, "- %s\n", opt)
		}
		b.WriteString("\n打开 PWA 点击选项选择")
		text = b.String()
	} else {
		summary := cd.Summary
		if summary == "" {
			summary = cd.ToolName
		}
		text = fmt.Sprintf("⚠️ 任务 #%s 需要决策\n%s\n\n回复 /approve 或 /deny，或打开 PWA 处理", id, summary)
	}
	tr.notify(t, text)
}

func (tr *TaskRunner) appendOutput(taskID, text string) {
	tr.transition(taskID, "", func(t *model.Task) {
		t.Output = t.Output + text
	})
}

// appendEvent 往 task.Events 追加一个执行事件并推送。
// text 类型做 200ms 合并(最后一个 event 是 text 且在 200ms 内则拼接,减少事件数与写盘);
// tool_use/tool_result 直接新增。seq = 在数组中的序号(1-based)。
func (tr *TaskRunner) appendEvent(taskID string, ev model.TaskEvent) {
	now := time.Now()
	ev.At = now
	tr.transition(taskID, "", func(t *model.Task) {
		// text 合并:最后一个 event 是 text 且 200ms 内
		if ev.Type == model.EventText && len(t.Events) > 0 {
			last := &t.Events[len(t.Events)-1]
			if last.Type == model.EventText && now.Sub(last.At) < 200*time.Millisecond {
				last.Text += ev.Text
				last.At = now
				return
			}
		}
		ev.Seq = len(t.Events) + 1
		t.Events = append(t.Events, ev)
	})
}

func (tr *TaskRunner) completeTask(taskID, output string) {
	updated := tr.transition(taskID, model.TaskCompleted, func(t *model.Task) {
		t.Output = output
		t.CurrentDecision = nil
		now := time.Now()
		t.FinishedAt = &now
	})
	tr.notifyFinished(updated, "✅ 任务完成")
}

// taskHasContent 判断任务是否已有用户可见的产出（output 非空，或存在文本/工具调用/结果事件）。
// 用于区分"claude 确实没干活"与"有产出但缺 result 事件"。
func taskHasContent(t *model.Task) bool {
	if t == nil {
		return false
	}
	if t.Output != "" {
		return true
	}
	for _, ev := range t.Events {
		switch ev.Type {
		case model.EventText, model.EventToolUse, model.EventToolResult:
			return true
		}
	}
	return false
}

// truncateErr 截断错误文本到上限（避免 stderr/长错误把任务 Error 撑爆）。
func truncateErr(s string) string {
	const max = 2000
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "\n…(已截断)"
}

func (tr *TaskRunner) failTask(taskID, errMsg string) {
	updated := tr.transition(taskID, model.TaskFailed, func(t *model.Task) {
		t.Error = errMsg
		t.CurrentDecision = nil
		now := time.Now()
		t.FinishedAt = &now
	})
	tr.logger.Error("task failed", zap.String("task", taskID), zap.String("err", errMsg))
	tr.notifyFinished(updated, "❌ 任务失败: "+errMsg)
}

// notifyFinished 往 IM 原渠道推送任务终态通知。
// 无 OriginChannel 或未注入 notify 时静默跳过；output 截断避免超长消息。
func (tr *TaskRunner) notifyFinished(t *model.Task, prefix string) {
	if tr.notify == nil || t == nil || t.OriginChannel == "" || t.OriginChatID == "" {
		return
	}
	id := t.ID
	if len(id) > 8 {
		id = id[:8]
	}
	out := t.Output
	if len(out) > 500 {
		out = out[:500] + "..."
	}
	text := prefix + " #" + id
	if out != "" {
		text += "\n\n" + out
	}
	tr.notify(t, text)
}

// transition 更新任务状态并发布事件。targetStatus 空表示只改字段（如 appendOutput）。
// 返回更新后的 task 副本（供调用方做通知等后续动作）；无变更或出错时返回 nil。
func (tr *TaskRunner) transition(taskID string, target model.TaskStatus, mutator func(*model.Task)) *model.Task {
	updated, err := tr.store.Update(taskID, func(t *model.Task) bool {
		if target != "" {
			// waiting_input 与 running 之间可反复切换；completed/failed/cancelled 是终态
			if t.Status == model.TaskCompleted || t.Status == model.TaskFailed || t.Status == model.TaskCancelled {
				return false
			}
			t.Status = target
		}
		mutator(t)
		return true
	})
	if err != nil {
		return nil
	}
	evType := "task_updated"
	if target == model.TaskCompleted {
		evType = "task_completed"
	}
	tr.bus.Publish(Event{Type: evType, TaskID: taskID, Task: updated})
	return updated
}

// buildStreamUserMessage 构造 --input-format stream-json 的 user 消息行。
// 格式：{"type":"user","message":{"role":"user","content":[{"type":"text","text":"..."}]}}
func buildStreamUserMessage(text string) string {
	b, _ := json.Marshal(map[string]interface{}{
		"type": "user",
		"message": map[string]interface{}{
			"role": "user",
			"content": []map[string]string{
				{"type": "text", "text": text},
			},
		},
	})
	return string(b)
}

// --- helpers ---

// concurrentBuffer 并发安全的 stderr 收集器。
type concurrentBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func newConcurrentBuffer() *concurrentBuffer { return &concurrentBuffer{} }

func (b *concurrentBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *concurrentBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// newTaskID 生成任务 ID（独立函数便于测试）。
func newTaskID() string { return uuid.New().String() }

// worktreePathFor 给定 worktreeBase/projectID/taskID 拼路径（测试用）。
func worktreePathFor(base, projectID, taskID string) string {
	return filepath.Join(base, projectID, taskID)
}
