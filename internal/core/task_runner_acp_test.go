package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"pieqi/internal/agent"
	"pieqi/internal/model"

	"go.uber.org/zap"
)

// Task 4.4 ACP 路径测试：用 fake agentRunner + fake AgentAdapter 覆盖 runACP 及
// Intervene/Cancel/Resume 的 ACP 分支，确认 use_acp 门控与 Phase 1 claude -p 路径互不干扰。
//
// fake 镜像 *agent.AgentManager 的 run-cancel 传播（Run 派生 cancelable ctx 并存 cancel，
// Cancel 调它中断 SendPrompt），SendPrompt 行为可脚本化（触发 delta / 触发 perm / 返回 err / 阻塞）。
//
// 注：TaskStore.Get 的副本拷贝在 RLock 释放之后执行（task_store.go:77），与并发 Update 存在
// 既存数据竞争（与 HookPending 测试同源，非本任务引入）。因此本测试不在 runACP 活跃变更期间
// 调 store.Get：终态用 fake.Adapter==nil（runACP 的 defer Close 已跑完＝goroutine 退出、无并发写）
// 判定，waiting_input 用 fake 的 permFired 标志（onP 返回后 goroutine 阻塞、无并发写）判定，
// 仅在无并发写时才 store.Get 读取断言。

// --- fakeAgentRunner：测试用 agentRunner 实现 ---

type fakeAgentRunner struct {
	mu         sync.Mutex
	adapters   map[string]*fakeAgentAdapter
	runCancels map[string]context.CancelFunc
	script     fakeScript
	fellBack   bool
	openErr    error
	openCalls  []fakeOpenCall
	closeN     int
	runN       int // Run（prompt turn）调用次数，断言保活复用时用
	sessSeq    int
	onClosed   func(taskID string) // SetOnSessionClosed 注册；Close 时触发（镜像 AgentManager）
}

// fakeScript 描述每次 Open 新建 adapter 的 SendPrompt 行为（从 runner 复制到 adapter）。
type fakeScript struct {
	deltaText     string                   // 非空 → SendPrompt 触发一次 onD
	permReq       *agent.PermissionRequest // 非空 → SendPrompt 触发 onP 后阻塞等 Approve/Deny 释放
	sendErr       error                    // SendPrompt 返回的错误
	block         bool                     // true → SendPrompt 阻塞到 ctx 取消（Cancel 测试用）
	realSessionID string                   // 非空 → adapter.RealSessionID 返回它（模拟真实协议 sid 持久化）
}

type fakeOpenCall struct {
	taskID, projectID, cwd string
	resumeFrom             string
}

func newFakeAgentRunner(script fakeScript, fellBack bool) *fakeAgentRunner {
	return &fakeAgentRunner{
		adapters:   make(map[string]*fakeAgentAdapter),
		runCancels: make(map[string]context.CancelFunc),
		script:     script,
		fellBack:   fellBack,
	}
}

func (f *fakeAgentRunner) Open(ctx context.Context, taskID, projectID string, cfg agent.SessionConfig) (agent.AgentAdapter, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.openCalls = append(f.openCalls, fakeOpenCall{taskID: taskID, projectID: projectID, cwd: cfg.Cwd, resumeFrom: cfg.ResumeFrom})
	if f.openErr != nil {
		return nil, false, f.openErr
	}
	f.sessSeq++
	a := &fakeAgentAdapter{
		sessionID:         fmt.Sprintf("sess-%d", f.sessSeq),
		deltaText:         f.script.deltaText,
		permReq:           f.script.permReq,
		sendErr:           f.script.sendErr,
		block:             f.script.block,
		realSessionID:     f.script.realSessionID,
		permRelease:       make(chan struct{}),
		sendPromptStarted: make(chan struct{}, 1),
		done:              make(chan struct{}),
	}
	f.adapters[taskID] = a
	return a, f.fellBack, nil
}

// SessionID 返回 task 当前 adapter 的 sessionID（无则 ""）。runACP 持久化真实 sid 时取它喂给
// adapter.RealSessionID（对齐 *agent.AgentManager.SessionID）。
func (f *fakeAgentRunner) SessionID(taskID string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a, ok := f.adapters[taskID]; ok {
		return a.sessionID
	}
	return ""
}

func (f *fakeAgentRunner) Run(ctx context.Context, taskID, prompt string) error {
	f.mu.Lock()
	a := f.adapters[taskID]
	f.runN++
	f.mu.Unlock()
	if a == nil {
		return fmt.Errorf("agent: no session for task %s", taskID)
	}
	// 派生 cancelable ctx 并登记 cancel，Cancel 经它中断 SendPrompt（对齐 AgentManager.Run）。
	runCtx, cancel := context.WithCancel(ctx)
	f.mu.Lock()
	f.runCancels[taskID] = cancel
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		delete(f.runCancels, taskID)
		f.mu.Unlock()
		cancel()
	}()
	return a.SendPrompt(runCtx, a.sessionID, prompt)
}

func (f *fakeAgentRunner) runCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runN
}

func (f *fakeAgentRunner) Cancel(ctx context.Context, taskID string) error {
	f.mu.Lock()
	a := f.adapters[taskID]
	cancel := f.runCancels[taskID]
	f.mu.Unlock()
	if a == nil {
		return nil
	}
	if cancel != nil {
		cancel()
	}
	return a.Cancel(ctx, a.sessionID)
}

func (f *fakeAgentRunner) Close(taskID string) error {
	f.mu.Lock()
	a := f.adapters[taskID]
	delete(f.adapters, taskID)
	delete(f.runCancels, taskID)
	f.closeN++
	onClosed := f.onClosed
	f.mu.Unlock()
	if a == nil {
		return nil
	}
	_ = a.Close(context.Background())
	if onClosed != nil {
		onClosed(taskID)
	}
	return nil
}

// SetOnSessionClosed 注册会话关闭回调（镜像 AgentManager；测试断言时可通过 onClosed 计数）。
func (f *fakeAgentRunner) SetOnSessionClosed(fn func(taskID string)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onClosed = fn
}

func (f *fakeAgentRunner) Adapter(taskID string) agent.AgentAdapter {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a, ok := f.adapters[taskID]; ok {
		return a
	}
	return nil
}

func (f *fakeAgentRunner) openCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.openCalls)
}

func (f *fakeAgentRunner) firstOpenCall() fakeOpenCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.openCalls) == 0 {
		return fakeOpenCall{}
	}
	return f.openCalls[0]
}

// openCallAt 返回第 i 次 Open 调用记录（i 从 0 起；越界返回零值）。测试据 ResumeFrom 透传断言。
func (f *fakeAgentRunner) openCallAt(i int) fakeOpenCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i < 0 || i >= len(f.openCalls) {
		return fakeOpenCall{}
	}
	return f.openCalls[i]
}

// setOpenErr 设置后续 Open 返回的错误（模拟续问时原会话丢失：ACP load/resume 报错）。
func (f *fakeAgentRunner) setOpenErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.openErr = err
}

func (f *fakeAgentRunner) closeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closeN
}

// adapter 返回 task 当前 open 的 fake adapter（无则 nil）。测试在 adapter 仍 open 时捕获引用，
// 以便 Close 后仍能读取其调用计数。
func (f *fakeAgentRunner) adapter(taskID string) *fakeAgentAdapter {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.adapters[taskID]
}

// --- fakeAgentAdapter：测试用 agent.AgentAdapter 实现 ---

type fakeAgentAdapter struct {
	sessionID string
	// 脚本（构造时设定，后续只读）
	deltaText     string
	permReq       *agent.PermissionRequest
	sendErr       error
	block         bool
	realSessionID string // 非空 → RealSessionID 返回它（模拟 ACPAgent 真实协议 sid 与句柄 sid 一致时的持久化目标）

	mu           sync.Mutex
	approveCalls []fakeApproveArgs
	cancelN      int
	closeN       int
	permFiredN   int // onP 触发次数（测试据此判断已进 waiting_input 且 goroutine 阻塞）

	releaseOnce sync.Once
	closeOnce   sync.Once
	permRelease chan struct{}

	cbMu sync.RWMutex
	onD  agent.ContentDeltaFunc
	onP  agent.PermissionRequestFunc
	onT  agent.ToolCallUpdateFunc

	sendPromptStarted chan struct{}
	done              chan struct{}
}

type fakeApproveArgs struct {
	reqID, optionID string
}

var _ agent.AgentAdapter = (*fakeAgentAdapter)(nil)

func (f *fakeAgentAdapter) NewSession(ctx context.Context, cfg agent.SessionConfig) (string, error) {
	return f.sessionID, nil
}

// RealSessionID 仿 ACPAgent 语义：realSessionID 非空时返回它（模拟真实协议 sid 持久化），
// 否则返回入参句柄 sid（与 ACPAgent 一致：ACP sessionId 即真实协议资源 ID）。
func (f *fakeAgentAdapter) RealSessionID(sessionID string) string {
	if f.realSessionID != "" {
		return f.realSessionID
	}
	return sessionID
}

func (f *fakeAgentAdapter) SendPrompt(ctx context.Context, sessionID, prompt string) error {
	// 通知测试 SendPrompt 已进入（Cancel / append_prompt 测试据此同步）。
	select {
	case f.sendPromptStarted <- struct{}{}:
	default:
	}

	f.cbMu.RLock()
	onP := f.onP
	onD := f.onD
	f.cbMu.RUnlock()

	// 先触发权限请求（若脚本配置），然后阻塞等 Approve/Deny 释放或 ctx 取消。
	// onP 返回即意味着 waiting_input 已落库，置 permFiredN 供测试无竞争检测。
	if f.permReq != nil && onP != nil {
		onP(*f.permReq)
		f.mu.Lock()
		f.permFiredN++
		f.mu.Unlock()
		select {
		case <-f.permRelease:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if f.deltaText != "" && onD != nil {
		onD(agent.ContentDelta{Text: f.deltaText})
	}
	if f.block {
		<-ctx.Done()
		return ctx.Err()
	}
	return f.sendErr
}

func (f *fakeAgentAdapter) OnContentDelta(fn agent.ContentDeltaFunc) {
	f.cbMu.Lock()
	f.onD = fn
	f.cbMu.Unlock()
}
func (f *fakeAgentAdapter) OnPermissionRequest(fn agent.PermissionRequestFunc) {
	f.cbMu.Lock()
	f.onP = fn
	f.cbMu.Unlock()
}
func (f *fakeAgentAdapter) OnToolCallUpdate(fn agent.ToolCallUpdateFunc) {
	f.cbMu.Lock()
	f.onT = fn
	f.cbMu.Unlock()
}

// release 释放 SendPrompt 的权限阻塞（Approve/Deny/Close 均可触发，幂等）。
func (f *fakeAgentAdapter) release() {
	f.releaseOnce.Do(func() { close(f.permRelease) })
}

func (f *fakeAgentAdapter) Approve(ctx context.Context, reqID, optionID string) error {
	f.mu.Lock()
	f.approveCalls = append(f.approveCalls, fakeApproveArgs{reqID: reqID, optionID: optionID})
	f.mu.Unlock()
	f.release()
	return nil
}

func (f *fakeAgentAdapter) Deny(ctx context.Context, reqID string) error {
	f.release()
	return nil
}

func (f *fakeAgentAdapter) RespondPermission(ctx context.Context, reqID string, allow bool, optionID string) error {
	if allow {
		return f.Approve(ctx, reqID, optionID)
	}
	return f.Deny(ctx, reqID)
}

func (f *fakeAgentAdapter) InjectToolResult(ctx context.Context, sessionID, toolCallID string, result string, isError bool) error {
	return agent.ErrNotSupported
}

func (f *fakeAgentAdapter) Cancel(ctx context.Context, sessionID string) error {
	f.mu.Lock()
	f.cancelN++
	f.mu.Unlock()
	return nil
}

func (f *fakeAgentAdapter) Close(ctx context.Context) error {
	f.mu.Lock()
	f.closeN++
	f.mu.Unlock()
	f.release()
	f.closeOnce.Do(func() { close(f.done) })
	return nil
}

func (f *fakeAgentAdapter) Done() <-chan struct{} { return f.done }

// 计数/参数读取辅助（在 adapter 仍 open 时捕获引用后调用）。
func (f *fakeAgentAdapter) cancelCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cancelN
}
func (f *fakeAgentAdapter) approveCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.approveCalls)
}
func (f *fakeAgentAdapter) approveCallAt(i int) fakeApproveArgs {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i < 0 || i >= len(f.approveCalls) {
		return fakeApproveArgs{}
	}
	return f.approveCalls[i]
}
func (f *fakeAgentAdapter) permFired() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.permFiredN > 0
}

// --- 测试辅助 ---

// newACPTestRunner 构造一个注入 fake agentRunner、启用 ACP 路径的 TaskRunner。
// cleanupWorktrees=false 且 task 预置 WorktreePath，跳过 wm.Create/Cleanup，无需真实 git repo。
func newACPTestRunner(t *testing.T, script fakeScript, fellBack bool) (*TaskRunner, *TaskStore, *EventBus, *fakeAgentRunner) {
	t.Helper()
	store, _ := NewTaskStore(t.TempDir())
	wm := NewWorktreeManager(zap.NewNop(), t.TempDir())
	bus := NewEventBus()
	hooks := NewHookService(5 * time.Second)
	tr := NewTaskRunner(zap.NewNop(), store, wm, bus, hooks, "", "bypassPermissions", false, "", 0, nil, 0, 0, "main")
	fake := newFakeAgentRunner(script, fellBack)
	tr.SetAgentManager(fake, true, 0)
	return tr, store, bus, fake
}

// createACPTestTask 建一个 pending task，预置 WorktreePath 跳过 worktree 创建。
func createACPTestTask(t *testing.T, store *TaskStore) *model.Task {
	t.Helper()
	wt := t.TempDir()
	task, err := store.Create(&model.Task{
		ProjectID:    "proj-acp",
		ProjectPath:  wt,
		WorktreePath: wt,
		Prompt:       "do something",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return task
}

// waitFor 轮询 cond 直到 true 或超时（超时 fatal）。
func waitFor(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", desc)
}

// waitACPAdapter 等 runACP 的 Open 把 adapter 登记进来并返回其引用。
// 直接读 fake.adapter 可能在 Open 尚未执行时拿到 nil（Start 只 go tr.run，runACP 还没跑到 Open），
// 故先轮询等 Open 完成；Cancel/Intervene 等需在 Run 期间同步读 adapter 的测试用此。
func waitACPAdapter(t *testing.T, fake *fakeAgentRunner, taskID string) *fakeAgentAdapter {
	t.Helper()
	var a *fakeAgentAdapter
	waitFor(t, 2*time.Second, "agent open", func() bool {
		a = fake.adapter(taskID)
		return a != nil
	})
	return a
}

// waitRunACPDone 等 runACP 一轮跑完：task 落到终态（completed/failed/cancelled），goroutine
// 已退出、无并发 store 写，可安全 store.Get 做终态断言。
//
// 保活语义下（ACP 路径）runACPTurn 轮末不再 Close 会话（跨轮保活复用），故不能用 closeCount
// 同步；终态转换（completeTask/failTask，或 Cancel 置 cancelled）是 runACPTurn 中最后的 store
// 写，其后只剩 context cancel 与 PrintAgent 的 Close（onSessionClosed 在 cleanupWorktrees=false
// 时无 store 写），观察到终态即可安全读。
func waitRunACPDone(t *testing.T, store *TaskStore, taskID string) {
	t.Helper()
	waitFor(t, 2*time.Second, "runACP turn finish (terminal status)", func() bool {
		tt, ok := store.Get(taskID)
		if !ok || tt == nil {
			return false
		}
		switch tt.Status {
		case model.TaskCompleted, model.TaskFailed, model.TaskCancelled:
			return true
		}
		return false
	})
}

// getTask 安全读取任务（仅在无并发写时调用：runACP 已退出或 goroutine 阻塞于审批）。
func getTask(t *testing.T, store *TaskStore, taskID string) *model.Task {
	t.Helper()
	t2, ok := store.Get(taskID)
	if !ok || t2 == nil {
		t.Fatalf("task %s not found", taskID)
	}
	return t2
}

// --- 测试用例 ---

// TestTaskRunner_ACP_SuccessWithOutput 成功+有输出：fake 触发一次 delta 后返回 nil。
// 断言 task completed、Output 含 delta 文本、Open 收到正确 projectID/cwd。
// 保活语义：ACP 会话轮末不关（Close==0、adapter 存活、wires 保留供续问复用），
// 关闭会话（模拟空闲回收）后 onSessionClosed 钩子才清 wires。
func TestTaskRunner_ACP_SuccessWithOutput(t *testing.T) {
	tr, store, _, fake := newACPTestRunner(t, fakeScript{deltaText: "hello"}, false)
	task := createACPTestTask(t, store)
	tr.Start(context.Background(), task)

	waitRunACPDone(t, store, task.ID)

	got := getTask(t, store, task.ID)
	if got.Status != model.TaskCompleted {
		t.Fatalf("status=%s, want completed", got.Status)
	}
	if !strings.Contains(got.Output, "hello") {
		t.Fatalf("output=%q, want contains hello", got.Output)
	}
	oc := fake.firstOpenCall()
	if oc.projectID != "proj-acp" || oc.cwd != task.WorktreePath {
		t.Fatalf("open call=%+v, want projectID=proj-acp cwd=%s", oc, task.WorktreePath)
	}
	// 保活：会话跨轮存活、wires 保留（供续问复用），轮末不 Close
	if fake.closeCount() != 0 {
		t.Fatalf("agent Close calls=%d want 0 (ACP session kept alive after run)", fake.closeCount())
	}
	if fake.Adapter(task.ID) == nil {
		t.Fatalf("ACP session should stay alive after completion")
	}
	if tr.permWire(task.ID) == nil {
		t.Fatal("wires should stay registered while session is alive (kept for resume)")
	}
	// 会话关闭（模拟空闲回收器）→ onSessionClosed 钩子清 wires
	if err := tr.agentMgr.Close(task.ID); err != nil {
		t.Fatalf("close (simulate reap): %v", err)
	}
	if tr.permWire(task.ID) != nil {
		t.Fatal("wires should be cleared after session closed")
	}
}

// TestTaskRunner_ACP_RunError Run 错误：fake SendPrompt 返回 error。
// 断言 task failed、Error 含 "agent run"、Close 被调。
func TestTaskRunner_ACP_RunError(t *testing.T) {
	tr, store, _, fake := newACPTestRunner(t, fakeScript{sendErr: errors.New("boom")}, false)
	task := createACPTestTask(t, store)
	tr.Start(context.Background(), task)

	waitRunACPDone(t, store, task.ID)

	got := getTask(t, store, task.ID)
	if got.Status != model.TaskFailed {
		t.Fatalf("status=%s, want failed", got.Status)
	}
	if !strings.Contains(got.Error, "agent run") {
		t.Fatalf("error=%q, want contains 'agent run'", got.Error)
	}
	// 保活语义：ACP 路径轮末不关会话（失败也可续问复用），会话仍登记
	if fake.closeCount() != 0 {
		t.Fatalf("agent Close calls=%d want 0 (ACP session kept alive after run)", fake.closeCount())
	}
	if fake.Adapter(task.ID) == nil {
		t.Fatalf("ACP session should stay alive (kept for reuse)")
	}
}

// TestTaskRunner_ACP_EmptyOutput 空输出：fake SendPrompt 直接返回 nil 不触发任何 delta。
// 断言 task failed（"未产出任何内容"）。
func TestTaskRunner_ACP_EmptyOutput(t *testing.T) {
	tr, store, _, _ := newACPTestRunner(t, fakeScript{}, false)
	task := createACPTestTask(t, store)
	tr.Start(context.Background(), task)

	waitRunACPDone(t, store, task.ID)

	got := getTask(t, store, task.ID)
	if got.Status != model.TaskFailed {
		t.Fatalf("status=%s, want failed", got.Status)
	}
	if !strings.Contains(got.Error, "未产出任何内容") {
		t.Fatalf("error=%q, want contains '未产出任何内容'", got.Error)
	}
}

// TestTaskRunner_ACP_FallbackEvent 回退事件：fake Open 返回 fellBack=true。
// 断言 task.Events 含一条 EventStatus("...回退...")。
func TestTaskRunner_ACP_FallbackEvent(t *testing.T) {
	tr, store, _, fake := newACPTestRunner(t, fakeScript{deltaText: "hi"}, true)
	task := createACPTestTask(t, store)
	tr.Start(context.Background(), task)

	waitRunACPDone(t, store, task.ID)

	got := getTask(t, store, task.ID)
	if got.Status != model.TaskCompleted {
		t.Fatalf("status=%s, want completed", got.Status)
	}
	found := false
	for _, ev := range got.Events {
		if ev.Type == model.EventStatus && strings.Contains(ev.Text, "回退") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("events missing fallback status event: %+v", got.Events)
	}
	// PrintAgent 回退路径是"一次性"进程语义：轮末关会话（与 ACP 保活相反）。
	// Close 在 completeTask 之后异步执行，须轮询等待。
	waitFor(t, 2*time.Second, "print agent closed at turn end", func() bool {
		return fake.closeCount() == 1
	})
}

// TestTaskRunner_ACP_Cancel Cancel：fake SendPrompt 阻塞；调 tr.Cancel。
// 断言 task cancelled、fake.Cancel 被调、阻塞的 Run 最终返回（goroutine 退出）。
func TestTaskRunner_ACP_Cancel(t *testing.T) {
	tr, store, _, fake := newACPTestRunner(t, fakeScript{block: true}, false)
	task := createACPTestTask(t, store)
	tr.Start(context.Background(), task)

	// 等 SendPrompt 进入阻塞（保证 fake.Run 已登记 runCancel，Cancel 能中断）。
	a := waitACPAdapter(t, fake, task.ID)
	<-a.sendPromptStarted

	if err := tr.Cancel(task.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	waitRunACPDone(t, store, task.ID)

	got := getTask(t, store, task.ID)
	if got.Status != model.TaskCancelled {
		t.Fatalf("status=%s, want cancelled", got.Status)
	}
	if a.cancelCount() != 1 {
		t.Fatalf("adapter Cancel calls=%d want 1", a.cancelCount())
	}
}

// TestTaskRunner_ACP_InterveneDecision Intervene decision 路由：fake SendPrompt 触发 onPerm 后阻塞等审批。
// approve → 选中 allow 选项；deny → 选中 reject 选项。断言 Approve 收到对应 optionID、task 最终 completed。
func TestTaskRunner_ACP_InterveneDecision(t *testing.T) {
	for _, tc := range []struct {
		name       string
		choice     string
		wantOption string
	}{
		{"approve", "approve", "a1"},
		{"deny", "deny", "d1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			permReq := &agent.PermissionRequest{
				ReqID: "r1",
				Options: []agent.PermissionOption{
					{ID: "a1", Kind: agent.PermissionOptionAllowOnce},
					{ID: "d1", Kind: agent.PermissionOptionRejectOnce},
				},
			}
			tr, store, _, fake := newACPTestRunner(t, fakeScript{permReq: permReq, deltaText: "done"}, false)
			task := createACPTestTask(t, store)
			tr.Start(context.Background(), task)

			// 等 onP 触发（permFired）——此时 waiting_input 已落库且 SendPrompt 阻塞、无并发 store 写。
			a := waitACPAdapter(t, fake, task.ID)
			waitFor(t, 2*time.Second, "perm fired (waiting_input)", a.permFired)

			// 安全读：goroutine 阻塞于审批，无并发写。
			got := getTask(t, store, task.ID)
			if got.Status != model.TaskWaitingInput {
				t.Fatalf("status=%s, want waiting_input", got.Status)
			}
			if got.CurrentDecision == nil || got.CurrentDecision.ID != "r1" {
				t.Fatalf("decision=%+v, want id r1", got.CurrentDecision)
			}

			if err := tr.Intervene(task.ID, model.Intervention{
				Kind: "decision", DecisionID: "r1", Choice: tc.choice,
			}); err != nil {
				t.Fatalf("Intervene %s: %v", tc.choice, err)
			}
			// Resolve 同步把 task 回 running（backToRunning），随后 Run 返回 nil，task completed。
			waitRunACPDone(t, store, task.ID)

			got = getTask(t, store, task.ID)
			if got.Status != model.TaskCompleted {
				t.Fatalf("status=%s, want completed after %s", got.Status, tc.choice)
			}
			if got := a.approveCount(); got != 1 {
				t.Fatalf("Approve calls=%d want 1", got)
			}
			if c := a.approveCallAt(0); c.reqID != "r1" || c.optionID != tc.wantOption {
				t.Fatalf("approve call=%+v want r1/%s", c, tc.wantOption)
			}
		})
	}
}

// TestTaskRunner_ACP_InterveneAppendPromptNotSupported ACP 路径不支持 append_prompt（stdin 注入）。
// 断言返回 "not supported" 错误，随后取消阻塞的 task 清理。
func TestTaskRunner_ACP_InterveneAppendPromptNotSupported(t *testing.T) {
	tr, store, _, fake := newACPTestRunner(t, fakeScript{block: true}, false)
	task := createACPTestTask(t, store)
	tr.Start(context.Background(), task)

	a := waitACPAdapter(t, fake, task.ID)
	<-a.sendPromptStarted

	err := tr.Intervene(task.ID, model.Intervention{Kind: "append_prompt", Text: "more"})
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("err=%v, want contains 'not supported'", err)
	}

	// 清理：取消阻塞的 task，让 runACP goroutine 退出。
	if err := tr.Cancel(task.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	waitRunACPDone(t, store, task.ID)
	got := getTask(t, store, task.ID)
	if got.Status != model.TaskCancelled {
		t.Fatalf("status=%s, want cancelled after cleanup", got.Status)
	}
}

// TestTaskRunner_ACP_Resume Resume on ACP：先把 task 跑到 completed，再 Resume 续问。
// 保活语义：首轮结束后 ACP 会话跨轮保活，Resume 直接复用同一会话（SendPrompt），
// 不重新 Open/LoadSession（openCount 保持 1），Run 被调第二次。
func TestTaskRunner_ACP_Resume(t *testing.T) {
	tr, store, _, fake := newACPTestRunner(t, fakeScript{deltaText: "hello"}, false)
	task := createACPTestTask(t, store)
	tr.Start(context.Background(), task)

	// 第一轮完成：会话保活（不 Close），openCount==1。
	waitRunACPDone(t, store, task.ID)
	if fake.openCount() != 1 {
		t.Fatalf("open calls=%d want 1 after first run", fake.openCount())
	}
	if fake.closeCount() != 0 {
		t.Fatalf("close calls=%d want 0 (ACP session kept alive after first run)", fake.closeCount())
	}
	if got := getTask(t, store, task.ID).Status; got != model.TaskCompleted {
		t.Fatalf("status=%s, want completed after first run", got)
	}

	// Resume 续问：保活复用同一会话，不重新 Open（openCount 保持 1），Run 调第二次。
	runBefore := fake.runCount()
	if err := tr.Resume(task.ID, "more"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	waitFor(t, 2*time.Second, "second turn finish", func() bool {
		tt, ok := store.Get(task.ID)
		return ok && tt != nil && tt.Status == model.TaskCompleted && fake.runCount() > runBefore
	})
	if fake.openCount() != 1 {
		t.Fatalf("open calls=%d want 1 after resume (session reused, no re-open)", fake.openCount())
	}
	if fake.runCount() != runBefore+1 {
		t.Fatalf("run calls=%d want %d (resume reuses live session, one more turn)", fake.runCount(), runBefore+1)
	}
	if fake.Adapter(task.ID) == nil {
		t.Fatalf("ACP session should stay alive after resume turn")
	}

	got := getTask(t, store, task.ID)
	found := false
	for _, ev := range got.Events {
		if ev.Type == model.EventUser && ev.Text == "more" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("events missing user 'more': %+v", got.Events)
	}
}

// TestRunACP_PersistsRealSessionID 首跑成功后真实 session ID 被持久化到 Task.ACPSessionID
// （替代脆弱的 CLI 匹配）。ACPAgent 语义：sessionID 即真实协议资源 ID；fake 用 realSessionID
// 显式模拟“真实 sid 与句柄 sid 不同”的情形，确认持久化的是 RealSessionID 而非句柄。
func TestRunACP_PersistsRealSessionID(t *testing.T) {
	tr, store, _, _ := newACPTestRunner(t, fakeScript{deltaText: "hi", realSessionID: "acp-real-sid"}, false)
	task := createACPTestTask(t, store)
	tr.Start(context.Background(), task)

	waitRunACPDone(t, store, task.ID)

	got := getTask(t, store, task.ID)
	if got.Status != model.TaskCompleted {
		t.Fatalf("status=%s, want completed", got.Status)
	}
	if got.ACPSessionID != "acp-real-sid" {
		t.Fatalf("ACPSessionID=%q, want acp-real-sid (RealSessionID must be persisted, not the handle sid)", got.ACPSessionID)
	}
}

// TestRunACP_Resume_PassesACPSessionID 保活会话被回收（reaper 关闭，Adapter==nil）后，
// 续问走 open 路径：runACP 据 task.ACPSessionID 构造 SessionConfig.ResumeFrom 并透传给
// Open（session/load/resume 复用上下文）。断言第二次 Open 收到 ResumeFrom=首跑持久化的真实 sid。
func TestRunACP_Resume_PassesACPSessionID(t *testing.T) {
	tr, store, _, fake := newACPTestRunner(t, fakeScript{deltaText: "hi", realSessionID: "acp-real-sid"}, false)
	task := createACPTestTask(t, store)
	tr.Start(context.Background(), task)

	waitRunACPDone(t, store, task.ID)
	if got := getTask(t, store, task.ID).ACPSessionID; got != "acp-real-sid" {
		t.Fatalf("first run ACPSessionID=%q, want acp-real-sid", got)
	}
	if fake.openCount() != 1 {
		t.Fatalf("open calls=%d want 1 after first run", fake.openCount())
	}
	// 首跑 Open 不应带 ResumeFrom（新建会话，非续问）。
	if first := fake.openCallAt(0); first.resumeFrom != "" {
		t.Fatalf("first open resumeFrom=%q, want empty (new session)", first.resumeFrom)
	}

	// 模拟空闲回收器把保活会话关掉（reaper Close 会触发 onSessionClosed 钩子清 wires）。
	if err := tr.agentMgr.Close(task.ID); err != nil {
		t.Fatalf("close (simulate reap): %v", err)
	}
	if fake.Adapter(task.ID) != nil {
		t.Fatalf("adapter should be gone after reap")
	}

	if err := tr.Resume(task.ID, "more"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	// 续问 re-open 走 open 路径：等第二次 Open 登记 + 轮次终态。
	waitFor(t, 2*time.Second, "resume re-open finish", func() bool {
		tt, ok := store.Get(task.ID)
		return ok && tt != nil && tt.Status == model.TaskCompleted && fake.openCount() >= 2
	})

	// 第二次 Open 应带 ResumeFrom=acp-real-sid（session/load/resume 复用上下文）。
	second := fake.openCallAt(1)
	if second.resumeFrom != "acp-real-sid" {
		t.Fatalf("second open resumeFrom=%q, want acp-real-sid", second.resumeFrom)
	}
	if second.cwd != task.WorktreePath {
		t.Fatalf("second open cwd=%q, want %s", second.cwd, task.WorktreePath)
	}
}

// TestRunACP_Resume_SessionLost 续问时原会话丢失（ACP load/resume 报错 / PrintAgent --resume
// "No conversation found"）：Open 失败。断言由协议层 surface——追加 status 事件提示用户 +
// failTask 带明确原因，不静默失败；且 Open 失败路径不登记 adapter，Close 不被调用（无资源泄漏）。
func TestRunACP_Resume_SessionLost(t *testing.T) {
	tr, store, _, fake := newACPTestRunner(t, fakeScript{deltaText: "hi", realSessionID: "acp-real-sid"}, false)
	task := createACPTestTask(t, store)
	tr.Start(context.Background(), task)

	waitRunACPDone(t, store, task.ID)
	if got := getTask(t, store, task.ID).ACPSessionID; got != "acp-real-sid" {
		t.Fatalf("first run ACPSessionID=%q, want acp-real-sid", got)
	}

	// 模拟空闲回收器把保活会话关掉：续问失去活会话，才会走 open 路径（ACP load/resume）。
	if err := tr.agentMgr.Close(task.ID); err != nil {
		t.Fatalf("close (simulate reap): %v", err)
	}
	// 续问时模拟原会话丢失：Open（load/resume）报错。
	fake.setOpenErr(errors.New("acp: load session acp-real-sid: session not found"))
	closeBefore := fake.closeCount()
	openBefore := fake.openCount()
	if err := tr.Resume(task.ID, "more"); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	// 第二次 Open 已发起（fake 自身 mutex 保护，无 store 竞态）。Open 失败路径不创建 adapter、
	// 不触发 Close，故不能用 closeCount 同步；改用 openCount++ + 终态 failed。
	waitFor(t, 2*time.Second, "resume open attempt", func() bool {
		return fake.openCount() > openBefore
	})
	// failTask 是 Open 失败路径最后的 store 写（其后仅 context cancel，无 store 写），
	// 观察到 failed 即可安全 getTask（与文件内既有 getTask-after-wait 约定一致）。
	waitFor(t, 2*time.Second, "task failed (session lost surfaced)", func() bool {
		tt, ok := store.Get(task.ID)
		return ok && tt != nil && tt.Status == model.TaskFailed
	})

	got := getTask(t, store, task.ID)
	if got.Status != model.TaskFailed {
		t.Fatalf("status=%s, want failed (session lost must surface, not stay silent)", got.Status)
	}
	if !strings.Contains(got.Error, "agent open") {
		t.Fatalf("error=%q, want contains 'agent open'", got.Error)
	}
	found := false
	for _, ev := range got.Events {
		if ev.Type == model.EventStatus && strings.Contains(ev.Text, "续问失败") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("events missing 续问失败 status event (session lost must be surfaced): %+v", got.Events)
	}
	// Open 失败路径不登记 adapter，Close 不应被调用（防双重清理/资源泄漏）。
	if fake.closeCount() != closeBefore {
		t.Fatalf("closeCount %d -> %d; Open-fail path must not call Close", closeBefore, fake.closeCount())
	}
}

// TestTaskRunner_ACP_DefaultPathUnaffected 不注入 AgentManager 时走 Phase 1 claude -p 路径。
// 简单断言 isACPPath 返回 false（旧路径 Intervene/Cancel/Resume 行为由既有测试覆盖）。
func TestTaskRunner_ACP_DefaultPathUnaffected(t *testing.T) {
	store, _ := NewTaskStore(t.TempDir())
	wm := NewWorktreeManager(zap.NewNop(), t.TempDir())
	bus := NewEventBus()
	hooks := NewHookService(5 * time.Second)
	tr := NewTaskRunner(zap.NewNop(), store, wm, bus, hooks, "", "bypassPermissions", false, "", 0, nil, 0, 0, "main")
	task, _ := store.Create(&model.Task{ProjectID: "cb", WorktreePath: "/wt", Prompt: "p"})

	if tr.isACPPath(task.ID) {
		t.Fatal("isACPPath should be false when SetAgentManager not called")
	}
}
