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
	runActive  map[string]bool // taskID -> 有进行中的 Run（镜像 AgentManager 的单 Run 约束）
	script     fakeScript
	fellBack   bool
	openErr    error
	// openErrOnlyResume：openErr 仅在 ResumeFrom 非空时生效（冷启动 load/resume not found
	// 后全新 Open 应成功）。false = 所有后续 Open 都失败。
	openErrOnlyResume bool
	openCalls         []fakeOpenCall
	closeN            int
	runN              int // Run（prompt turn）调用次数，断言保活复用时用
	busyN             int // 接下来 N 次 Run 返回 ErrSessionBusy（模拟并发提交撞上"已有轮次在跑"）
	imagesN           int // 最近一次 RunRich 收到的图片数（0 = 纯文本）；读用 lastImages()
	prompts           []string // 每次 Run/RunRich 收到的 prompt（断言无历史锚点）
	sessSeq           int
	onClosed          func(taskID, reason string) // SetOnSessionClosed 注册；Close 时触发（镜像 AgentManager）
}

// fakeScript 描述每次 Open 新建 adapter 的 SendPrompt 行为（从 runner 复制到 adapter）。
type fakeScript struct {
	deltaText     string                   // 非空 → SendPrompt 触发一次 onD
	permReq       *agent.PermissionRequest // 非空 → SendPrompt 触发 onP 后阻塞等 Approve/Deny 释放
	sendErr       error                    // SendPrompt 返回的错误
	block         bool                     // true → SendPrompt 阻塞到 ctx 取消（Cancel 测试用）
	delay         time.Duration            // 非零 → SendPrompt 先等这么久（拉长轮次，构造并发排队窗口）
	realSessionID string                   // 非空 → adapter.RealSessionID 返回它（模拟真实协议 sid 持久化）
	// noImageCapability 置 true 让替身声明"收不了图"（模拟 claude / print 这类纯文本 agent）。
	noImageCapability bool
}

type fakeOpenCall struct {
	taskID, projectID, cwd string
	resumeFrom             string
}

func newFakeAgentRunner(script fakeScript, fellBack bool) *fakeAgentRunner {
	return &fakeAgentRunner{
		adapters:   make(map[string]*fakeAgentAdapter),
		runCancels: make(map[string]context.CancelFunc),
		runActive:  make(map[string]bool),
		script:     script,
		fellBack:   fellBack,
	}
}

func (f *fakeAgentRunner) Open(ctx context.Context, taskID, projectID string, cfg agent.SessionConfig) (agent.AgentAdapter, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.openCalls = append(f.openCalls, fakeOpenCall{taskID: taskID, projectID: projectID, cwd: cfg.Cwd, resumeFrom: cfg.ResumeFrom})
	if f.openErr != nil && (!f.openErrOnlyResume || cfg.ResumeFrom != "") {
		return nil, false, f.openErr
	}
	f.sessSeq++
	a := &fakeAgentAdapter{
		sessionID:         fmt.Sprintf("sess-%d", f.sessSeq),
		deltaText:         f.script.deltaText,
		permReq:           f.script.permReq,
		sendErr:           f.script.sendErr,
		block:             f.script.block,
		delay:             f.script.delay,
		realSessionID:     f.script.realSessionID,
		permRelease:       make(chan struct{}),
		sendPromptStarted: make(chan struct{}, 1),
		done:              make(chan struct{}),
		// 默认声明收图能力（多数用例关心的是"图有没有传到"而不是"能不能传"）；
		// 要测纯文本 agent 的拒绝路径，用 fakeScript.imageCapable=false 关掉。
		imageCapable: !f.script.noImageCapability,
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

func (f *fakeAgentRunner) Run(ctx context.Context, taskID, prompt, turnModel string) error {
	return f.RunRich(ctx, taskID, prompt, turnModel, nil)
}

// RunRich 镜像 AgentManager.RunRich：图片非空时走适配器的富文本入口（不支持则报错）。
// 复制 Run 的忙判定与 cancel 登记逻辑，两条路径的行为才一致 —— 否则测试会在
// "图片路径没还原 ErrSessionBusy 约束"上留下盲区。
func (f *fakeAgentRunner) RunRich(ctx context.Context, taskID, prompt, turnModel string, images []agent.ImageInput) error {
	f.mu.Lock()
	a := f.adapters[taskID]
	f.runN++
	f.prompts = append(f.prompts, prompt)
	if len(images) > 0 {
		f.imagesN = len(images)
	}
	// 镜像 AgentManager：同一 task 同时只允许一个 Run，并发第二个返回 ErrSessionBusy。
	// （不还原这条约束，测试就对"并发提交把任务打成 failed"这个 bug 免疫 —— 那正是回归盲区。）
	if f.runActive[taskID] {
		f.mu.Unlock()
		return fmt.Errorf("%w: prompt already running for task %s", agent.ErrSessionBusy, taskID)
	}
	if f.busyN > 0 {
		f.busyN--
		f.mu.Unlock()
		return fmt.Errorf("%w: prompt already running for task %s", agent.ErrSessionBusy, taskID)
	}
	f.runActive[taskID] = true
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		delete(f.runActive, taskID)
		f.mu.Unlock()
	}()
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
	if len(images) > 0 {
		if !a.SupportsImagePrompt() {
			return fmt.Errorf("%w: 当前 agent 传输不支持图片", agent.ErrImageNotSupported)
		}
		return a.SendRichPrompt(runCtx, a.sessionID, prompt, images)
	}
	return a.SendPrompt(runCtx, a.sessionID, prompt)
}

func (f *fakeAgentRunner) runCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runN
}

func (f *fakeAgentRunner) lastPrompt() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.prompts) == 0 {
		return ""
	}
	return f.prompts[len(f.prompts)-1]
}

func (f *fakeAgentRunner) promptAt(i int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i < 0 || i >= len(f.prompts) {
		return ""
	}
	return f.prompts[i]
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
	return f.CloseWithReason(taskID, agent.SessionCloseOther)
}

func (f *fakeAgentRunner) CloseWithReason(taskID, reason string) error {
	f.mu.Lock()
	a := f.adapters[taskID]
	cancel := f.runCancels[taskID]
	delete(f.adapters, taskID)
	delete(f.runCancels, taskID)
	f.closeN++
	onClosed := f.onClosed
	f.mu.Unlock()
	// 对齐 *agent.AgentManager.Close：先中断进行中的 turn（否则阻塞中的 SendPrompt 永不返回）。
	if cancel != nil {
		cancel()
	}
	if a == nil {
		return nil
	}
	_ = a.Close(context.Background())
	if onClosed != nil {
		if reason == "" {
			reason = agent.SessionCloseOther
		}
		onClosed(taskID, reason)
	}
	return nil
}

// SetOnSessionClosed 注册会话关闭回调（镜像 AgentManager；测试断言时可通过 onClosed 计数）。
func (f *fakeAgentRunner) SetOnSessionClosed(fn func(taskID, reason string)) {
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

// setOpenErr 设置后续 Open 返回的错误（所有 Open 均失败）。
func (f *fakeAgentRunner) setOpenErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.openErr = err
	f.openErrOnlyResume = false
}

// setOpenErrIfResume 仅当 ResumeFrom 非空时 Open 失败（模拟 load/resume not found；
// 清 sid 后的全新 Open 成功，覆盖 ensureACPSession 冷启动兜底）。
func (f *fakeAgentRunner) setOpenErrIfResume(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.openErr = err
	f.openErrOnlyResume = true
}

// setBusyN 让接下来 n 次 Run 返回 ErrSessionBusy（模拟并发提交撞上"已有轮次在跑"）。
func (f *fakeAgentRunner) setBusyN(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.busyN = n
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
	delay         time.Duration // 非零 → SendPrompt 先等这么久（构造并发排队窗口）
	realSessionID string        // 非空 → RealSessionID 返回它（模拟 ACPAgent 真实协议 sid 与句柄 sid 一致时的持久化目标）

	mu           sync.Mutex
	approveCalls []fakeApproveArgs
	cancelN      int
	closeN       int
	permFiredN   int // onP 触发次数（测试据此判断已进 waiting_input 且 goroutine 阻塞）
	inFlight     int // 当前在跑 SendPrompt 的次数
	maxInFlight  int // 峰值并发 SendPrompt 数：串行化（排队）的直接证据
	promptN      int // SendPrompt 总调用次数
	imagesN      int // SendRichPrompt 收到的图片总张数（断言"图真的传到 adapter 了"）
	// imageCapable 该替身是否声明收图能力（默认 true；置 false 模拟 claude 之类纯文本 agent）。
	imageCapable bool

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
	return f.doPrompt(ctx, sessionID, prompt)
}

// SendRichPrompt 测试替身：记下图片张数后走同一条 prompt 路径。
// 两条入口共用 doPrompt 是为了让"带图/不带图"在 fake 上**行为一致**
// （延时、并发探测、权限脚本都不走两遍），否则测试会在等价路径上给出不同结论。
func (f *fakeAgentAdapter) SendRichPrompt(ctx context.Context, sessionID, prompt string, images []agent.ImageInput) error {
	f.mu.Lock()
	f.imagesN += len(images)
	f.mu.Unlock()
	return f.doPrompt(ctx, sessionID, prompt)
}

var (
	_ agent.RichPromptSender   = (*fakeAgentAdapter)(nil)
	_ agent.ImagePromptCapable = (*fakeAgentAdapter)(nil)
)

// SupportsImagePrompt 测试替身默认支持收图（imageCapable 置 false 可关）。
func (f *fakeAgentAdapter) SupportsImagePrompt() bool { return f.imageCapable }

func (f *fakeAgentAdapter) doPrompt(ctx context.Context, sessionID, prompt string) error {
	// 并发探测：SendPrompt 同时在跑的次数必须恒为 1（串行队列不变式）。
	f.mu.Lock()
	f.inFlight++
	f.promptN++
	if f.inFlight > f.maxInFlight {
		f.maxInFlight = f.inFlight
	}
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.inFlight--
		f.mu.Unlock()
	}()

	// 通知测试 SendPrompt 已进入（Cancel / append_prompt 测试据此同步）。
	select {
	case f.sendPromptStarted <- struct{}{}:
	default:
	}

	// 脚本化延时：拉长一轮的时长，便于在"轮还在跑"的窗口里提交并发请求。
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
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

// maxConcurrentPrompts 返回峰值并发 SendPrompt 次数（1 = 全程串行，没有两轮并行）。
func (f *fakeAgentAdapter) maxConcurrentPrompts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxInFlight
}

// promptCount 返回 SendPrompt 被调用的总次数。
func (f *fakeAgentAdapter) promptCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.promptN
}

// imagesReceived 返回 SendRichPrompt 累计收到的图片张数（0 = 从未走富文本入口）。
func (f *fakeAgentAdapter) imagesReceived() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.imagesN
}

// lastImages 返回最近一次 RunRich 收到的图片张数（0 = 纯文本）。
func (f *fakeAgentRunner) lastImages() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.imagesN
}

// SupportsImagePrompt 镜像 AgentManager：问当前 adapter 能不能收图。
func (f *fakeAgentRunner) SupportsImagePrompt(taskID string) bool {
	f.mu.Lock()
	a := f.adapters[taskID]
	f.mu.Unlock()
	return a != nil && a.SupportsImagePrompt()
}

// --- 测试辅助 ---

// newACPTestRunner 构造一个注入 fake agentRunner、启用 ACP 路径的 TaskRunner。
// cleanupWorktrees=false 且 task 预置 WorktreePath，跳过 wm.Create/Cleanup，无需真实 git repo。
func newACPTestRunner(t *testing.T, script fakeScript, fellBack bool) (*TaskRunner, *TaskStore, *EventBus, *fakeAgentRunner) {
	t.Helper()
	store := newTestTaskStore(t)
	wm := NewWorktreeManager(zap.NewNop(), t.TempDir())
	bus := NewEventBus()
	hooks := NewHookService(5 * time.Second)
	tr := NewTaskRunner(zap.NewNop(), store, wm, bus, hooks, "", "bypassPermissions", false, "", 0, nil, 0, 0)
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
	if err := tr.Resume(task.ID, "more", ""); err != nil {
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

	if err := tr.Resume(task.ID, "more", ""); err != nil {
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

// TestRunACP_Resume_SessionLost 冷启动续问撞 load/resume session not found：清死 sid 后
// 开全新会话并完成，不反复 failed。status 提示「续问上下文已丢失，已用全新会话继续」。
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
	// resume Open 失败；ResumeFrom="" 的全新 Open 成功（openErrOnlyResume）。
	fake.setOpenErrIfResume(errors.New("acp: load session acp-real-sid: session not found"))
	openBefore := fake.openCount()
	if err := tr.Resume(task.ID, "more", ""); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	// 至少两次 Open：resume 失败一次 + 全新成功一次；终态 completed。
	waitFor(t, 2*time.Second, "session-lost recover completed", func() bool {
		tt, ok := store.Get(task.ID)
		return ok && tt != nil && tt.Status == model.TaskCompleted && fake.openCount() >= openBefore+2
	})

	got := getTask(t, store, task.ID)
	if got.Status != model.TaskCompleted {
		t.Fatalf("status=%s error=%q, want completed after fresh session", got.Status, got.Error)
	}
	found := false
	for _, ev := range got.Events {
		if ev.Type == model.EventStatus && strings.Contains(ev.Text, "续问上下文已丢失，已用全新会话继续") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("events missing session-lost status: %+v", got.Events)
	}
	// 第二次 Open（index openBefore）带死 sid；第三次（openBefore+1）ResumeFrom 为空。
	resumeAttempt := fake.openCallAt(openBefore)
	if resumeAttempt.resumeFrom != "acp-real-sid" {
		t.Fatalf("resume open resumeFrom=%q, want acp-real-sid", resumeAttempt.resumeFrom)
	}
	fresh := fake.openCallAt(openBefore + 1)
	if fresh.resumeFrom != "" {
		t.Fatalf("fresh open resumeFrom=%q, want empty", fresh.resumeFrom)
	}
}

// TestRunACP_Resume_KeepAliveSessionNotFound 保活复用活适配器但对端已不认 sid：
// prompt 报 Session not found → Close 僵尸 → 清 ACPSessionID → 全新 Open → 重跑成功。
func TestRunACP_Resume_KeepAliveSessionNotFound(t *testing.T) {
	tr, store, _, fake := newACPTestRunner(t, fakeScript{deltaText: "hi", realSessionID: "acp-real-sid"}, false)
	task := createACPTestTask(t, store)
	tr.Start(context.Background(), task)

	waitRunACPDone(t, store, task.ID)
	a := fake.adapter(task.ID)
	if a == nil {
		t.Fatal("keepalive adapter missing after first run")
	}
	openBefore := fake.openCount()
	closeBefore := fake.closeCount()

	// 连接仍活（Done 未关），但 prompt 撞 Cursor JSON Session not found（无增量，纯失败）。
	a.deltaText = ""
	a.sendErr = errors.New(`acp: prompt: {"code":-32603,"message":"Internal error","data":{"details":"Session dead-sid not found"}}`)
	if err := tr.Resume(task.ID, "继续", ""); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	waitFor(t, 2*time.Second, "keepalive session-not-found recover", func() bool {
		tt, ok := store.Get(task.ID)
		return ok && tt != nil && tt.Status == model.TaskCompleted &&
			fake.openCount() > openBefore && fake.closeCount() > closeBefore
	})

	got := getTask(t, store, task.ID)
	if got.Status != model.TaskCompleted {
		t.Fatalf("status=%s error=%q, want completed", got.Status, got.Error)
	}
	found := false
	for _, ev := range got.Events {
		if ev.Type == model.EventStatus && strings.Contains(ev.Text, "续问上下文已丢失，已用全新会话继续") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("events missing session-lost status: %+v", got.Events)
	}
	fresh := fake.openCallAt(openBefore) // 首次全新 Open（保活路径不先 resume Open）
	if fresh.resumeFrom != "" {
		t.Fatalf("fresh open after keepalive miss resumeFrom=%q, want empty", fresh.resumeFrom)
	}
}

// TestRunACP_Resume_KeepAliveSessionLost cursor 不用 "not found"，prompt 阶段报的是
// {"code":-32603,"message":"Internal error: session lost"}。识别不出来时这条链路会直接
// failTask（一周内 5 次失败里 2 次就是这个），行为契约与 SessionNotFound 完全一致：
// Close 僵尸 → 清 ACPSessionID → 全新 Open → 重跑本轮成功。
func TestRunACP_Resume_KeepAliveSessionLost(t *testing.T) {
	tr, store, _, fake := newACPTestRunner(t, fakeScript{deltaText: "hi", realSessionID: "acp-real-sid"}, false)
	task := createACPTestTask(t, store)
	tr.Start(context.Background(), task)

	waitRunACPDone(t, store, task.ID)
	a := fake.adapter(task.ID)
	if a == nil {
		t.Fatal("keepalive adapter missing after first run")
	}
	openBefore := fake.openCount()
	closeBefore := fake.closeCount()

	a.deltaText = ""
	a.sendErr = errors.New(`acp: prompt: {"code":-32603,"message":"Internal error: session lost"}`)
	if err := tr.Resume(task.ID, "继续", ""); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	waitFor(t, 2*time.Second, "keepalive session-lost recover", func() bool {
		tt, ok := store.Get(task.ID)
		return ok && tt != nil && tt.Status == model.TaskCompleted &&
			fake.openCount() > openBefore && fake.closeCount() > closeBefore
	})

	got := getTask(t, store, task.ID)
	if got.Status != model.TaskCompleted {
		t.Fatalf("status=%s error=%q, want completed", got.Status, got.Error)
	}
	found := false
	for _, ev := range got.Events {
		if ev.Type == model.EventStatus && strings.Contains(ev.Text, "续问上下文已丢失，已用全新会话继续") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("events missing session-lost status: %+v", got.Events)
	}
	if fresh := fake.openCallAt(openBefore); fresh.resumeFrom != "" {
		t.Fatalf("fresh open after session lost resumeFrom=%q, want empty", fresh.resumeFrom)
	}
}

// TestRunACP_Resume_NoACPSessionAnchorsPrompt 首轮从未建成 ACP 会话（无 ACPSessionID）时续问：
// 允许重试，但发给 agent 的 prompt 必须钉本任务原文，且回执不能假装「正在恢复会话」。
// 回归 c764d612：裸「继续」+ 预分配 ClaudeSessionID 放行 → 空会话串到别的 transcript。
func TestRunACP_Resume_NoACPSessionAnchorsPrompt(t *testing.T) {
	tr, store, _, fake := newACPTestRunner(t, fakeScript{deltaText: "ok"}, false)
	task := createACPTestTask(t, store)
	_, err := store.Update(task.ID, func(tt *model.Task) bool {
		tt.Status = model.TaskFailed
		tt.Error = "agent open: Cursor 未登录"
		tt.ACPSessionID = ""
		tt.Prompt = "approval unresolved: 怎么导致 acp 中断"
		return true
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if err := tr.Resume(task.ID, "继续", ""); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if !hasEvent(t, store, task.ID, model.EventStatus, "无对端会话可恢复") {
		t.Fatalf("want no-restore receipt, events=%+v", getTask(t, store, task.ID).Events)
	}
	if hasEvent(t, store, task.ID, model.EventStatus, "正在恢复会话") {
		t.Fatalf("must not claim session restore when ACPSessionID empty")
	}

	// 任务进 Resume 前已是 failed，不能用 waitRunACPDone（它会立刻命中旧终态）。
	waitFor(t, 2*time.Second, "fresh resume completed", func() bool {
		tt, ok := store.Get(task.ID)
		return ok && tt != nil && tt.Status == model.TaskCompleted && fake.runCount() >= 1
	})
	got := fake.lastPrompt()
	if !strings.Contains(got, "approval unresolved") {
		t.Fatalf("anchored prompt missing original task: %q", got)
	}
	if !strings.Contains(got, "不要查找其它会话") {
		t.Fatalf("anchored prompt missing anti-wander head: %q", got)
	}
	if !strings.Contains(got, "当前请求：继续") {
		t.Fatalf("anchored prompt missing current request: %q", got)
	}
}

// TestRunACP_Resume_WarmBareContinueUnchanged 热保活有历史时，裸「继续」原样转发（不加锚点）。
func TestRunACP_Resume_WarmBareContinueUnchanged(t *testing.T) {
	tr, store, _, fake := newACPTestRunner(t, fakeScript{deltaText: "hello"}, false)
	task := createACPTestTask(t, store)
	tr.Start(context.Background(), task)
	waitRunACPDone(t, store, task.ID)
	if fake.adapter(task.ID) == nil {
		t.Fatal("keepalive adapter missing")
	}
	if err := tr.Resume(task.ID, "继续", ""); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	waitFor(t, 2*time.Second, "warm resume done", func() bool {
		tt, ok := store.Get(task.ID)
		return ok && tt != nil && tt.Status == model.TaskCompleted && fake.runCount() >= 2
	})
	if got := fake.lastPrompt(); got != "继续" {
		t.Fatalf("warm resume prompt=%q, want bare 继续", got)
	}
}

// TestRunACP_Resume_SessionLostAnchorsPrompt load 失败新开后，发给 agent 的文案必须带本任务锚点。
func TestRunACP_Resume_SessionLostAnchorsPrompt(t *testing.T) {
	tr, store, _, fake := newACPTestRunner(t, fakeScript{deltaText: "hi", realSessionID: "acp-real-sid"}, false)
	task := createACPTestTask(t, store)
	tr.Start(context.Background(), task)
	waitRunACPDone(t, store, task.ID)
	if err := tr.agentMgr.Close(task.ID); err != nil {
		t.Fatalf("close: %v", err)
	}
	fake.setOpenErrIfResume(errors.New("acp: load session acp-real-sid: session not found"))
	if err := tr.Resume(task.ID, "继续", ""); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	waitFor(t, 2*time.Second, "session-lost anchored", func() bool {
		tt, ok := store.Get(task.ID)
		return ok && tt != nil && tt.Status == model.TaskCompleted && fake.runCount() >= 2
	})
	got := fake.lastPrompt()
	if !strings.Contains(got, "do something") {
		t.Fatalf("session-lost fresh prompt missing task: %q", got)
	}
	if !strings.Contains(got, "当前请求：继续") {
		t.Fatalf("session-lost fresh prompt missing request: %q", got)
	}
}

// TestTaskRunner_ACP_DefaultPathUnaffected 不注入 AgentManager 时走 Phase 1 claude -p 路径。
// 简单断言 isACPPath 返回 false（旧路径 Intervene/Cancel/Resume 行为由既有测试覆盖）。
func TestTaskRunner_ACP_DefaultPathUnaffected(t *testing.T) {
	store := newTestTaskStore(t)
	wm := NewWorktreeManager(zap.NewNop(), t.TempDir())
	bus := NewEventBus()
	hooks := NewHookService(5 * time.Second)
	tr := NewTaskRunner(zap.NewNop(), store, wm, bus, hooks, "", "bypassPermissions", false, "", 0, nil, 0, 0)
	task, _ := store.Create(&model.Task{ProjectID: "cb", WorktreePath: "/wt", Prompt: "p"})

	if tr.isACPPath(task.ID) {
		t.Fatal("isACPPath should be false when SetAgentManager not called")
	}
}
