package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"pieqi/internal/config"
)

// --- fakeAdapter：测试用 AgentAdapter 实现 ---
//
// 记录所有方法调用次数/参数，行为可逐字段配置：
//   - NewSession：返回 newSessionID 或 newSessionErr
//   - SendPrompt：sendPromptBlock=true 时阻塞到 ctx 取消并返回 ctx.Err()，否则返回 sendPromptErr；
//     进入时向 sendPromptStarted 发非阻塞信号，供测试同步
//   - Cancel/Close：计数（cancelErr 可配）
//   - OnXxx 回调：存起来（测试不触发）
type fakeAdapter struct {
	mu sync.Mutex

	// NewSession
	newSessionID    string
	newSessionErr   error
	newSessionN     int
	newSessionCwd   []string
	newSessionCfg   []SessionConfig

	// SendPrompt
	sendPromptErr     error
	sendPromptBlock   bool
	sendPromptN       int
	sendPromptArgs    []fakeSendArg
	sendPromptStarted chan struct{}

	// Cancel / Close
	cancelN   int
	cancelErr error
	closeN    int

	// 回调注册
	cbMu   sync.RWMutex
	onD    ContentDeltaFunc
	onP    PermissionRequestFunc
	onT    ToolCallUpdateFunc

	done chan struct{}
}

type fakeSendArg struct {
	sessionID string
	prompt    string
}

var _ AgentAdapter = (*fakeAdapter)(nil)

func newFakeAdapter(sessionID string) *fakeAdapter {
	return &fakeAdapter{newSessionID: sessionID, done: make(chan struct{})}
}

func (f *fakeAdapter) NewSession(ctx context.Context, cfg SessionConfig) (string, error) {
	f.mu.Lock()
	f.newSessionN++
	f.newSessionCwd = append(f.newSessionCwd, cfg.Cwd)
	f.newSessionCfg = append(f.newSessionCfg, cfg)
	sid := f.newSessionID
	err := f.newSessionErr
	f.mu.Unlock()
	if err != nil {
		return "", err
	}
	return sid, nil
}

// RealSessionID 仿 ACPAgent：直接返回入参（fake 不区分真实/句柄 sid）。
func (f *fakeAdapter) RealSessionID(sessionID string) string { return sessionID }

func (f *fakeAdapter) SendPrompt(ctx context.Context, sessionID, prompt string) error {
	f.mu.Lock()
	f.sendPromptN++
	f.sendPromptArgs = append(f.sendPromptArgs, fakeSendArg{sessionID: sessionID, prompt: prompt})
	block := f.sendPromptBlock
	started := f.sendPromptStarted
	err := f.sendPromptErr
	f.mu.Unlock()

	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if block {
		<-ctx.Done()
		return ctx.Err()
	}
	return err
}

func (f *fakeAdapter) OnContentDelta(fn ContentDeltaFunc) { f.cbMu.Lock(); f.onD = fn; f.cbMu.Unlock() }
func (f *fakeAdapter) OnPermissionRequest(fn PermissionRequestFunc) {
	f.cbMu.Lock(); f.onP = fn; f.cbMu.Unlock()
}
func (f *fakeAdapter) OnToolCallUpdate(fn ToolCallUpdateFunc) { f.cbMu.Lock(); f.onT = fn; f.cbMu.Unlock() }

func (f *fakeAdapter) Approve(ctx context.Context, reqID, optionID string) error { return nil }
func (f *fakeAdapter) Deny(ctx context.Context, reqID string) error               { return nil }
func (f *fakeAdapter) RespondPermission(ctx context.Context, reqID string, allow bool, optionID string) error {
	return nil
}
func (f *fakeAdapter) InjectToolResult(ctx context.Context, sessionID, toolCallID string, result string, isError bool) error {
	return nil
}

func (f *fakeAdapter) Cancel(ctx context.Context, sessionID string) error {
	f.mu.Lock()
	f.cancelN++
	err := f.cancelErr
	f.mu.Unlock()
	return err
}

func (f *fakeAdapter) Close(ctx context.Context) error {
	f.mu.Lock()
	f.closeN++
	f.mu.Unlock()
	return nil
}

func (f *fakeAdapter) Done() <-chan struct{} { return f.done }

// fake 计数/参数读取辅助。
func (f *fakeAdapter) newSessionCount() int {
	f.mu.Lock(); defer f.mu.Unlock()
	return f.newSessionN
}
func (f *fakeAdapter) closeCount() int {
	f.mu.Lock(); defer f.mu.Unlock()
	return f.closeN
}
func (f *fakeAdapter) sendPromptCount() int {
	f.mu.Lock(); defer f.mu.Unlock()
	return f.sendPromptN
}
func (f *fakeAdapter) cancelCount() int {
	f.mu.Lock(); defer f.mu.Unlock()
	return f.cancelN
}
func (f *fakeAdapter) newSessionCwdsCopy() []string {
	f.mu.Lock(); defer f.mu.Unlock()
	out := make([]string, len(f.newSessionCwd))
	copy(out, f.newSessionCwd)
	return out
}
func (f *fakeAdapter) lastNewSessionCfg() (SessionConfig, bool) {
	f.mu.Lock(); defer f.mu.Unlock()
	if len(f.newSessionCfg) == 0 {
		return SessionConfig{}, false
	}
	return f.newSessionCfg[len(f.newSessionCfg)-1], true
}
func (f *fakeAdapter) firstSendArg() fakeSendArg {
	f.mu.Lock(); defer f.mu.Unlock()
	if len(f.sendPromptArgs) == 0 {
		return fakeSendArg{}
	}
	return f.sendPromptArgs[0]
}

// --- 工厂辅助 ---

// fakeFactory 返回总成功（fa + kind）的 adapterFactory。
func fakeFactory(fa *fakeAdapter, kind AgentKind) adapterFactory {
	return func() (AgentAdapter, AgentKind, error) { return fa, kind, nil }
}

// errFactory 返回总报 err 的 adapterFactory。
func errFactory(err error) adapterFactory {
	return func() (AgentAdapter, AgentKind, error) { return nil, "", err }
}

// openRes Open 的异步结果。
type openRes struct {
	adapter  AgentAdapter
	fellBack bool
	err      error
}

// openAsync 在 goroutine 里调 Open，结果送回 channel（便于带超时的并发上限测试）。
func openAsync(m *AgentManager, ctx context.Context, taskID, projectID, cwd string) <-chan openRes {
	ch := make(chan openRes, 1)
	go func() {
		a, fb, err := m.Open(ctx, taskID, projectID, SessionConfig{Cwd: cwd})
		ch <- openRes{a, fb, err}
	}()
	return ch
}

// --- 测试 ---

// TestManagerNewAgentManager_Defaults 校验 nil logger 兜底与 sessions 初始化。
func TestManagerNewAgentManager_Defaults(t *testing.T) {
	m := NewAgentManager(ManagerConfig{}, nil)
	if m == nil {
		t.Fatal("NewAgentManager returned nil")
	}
	if m.logger == nil {
		t.Error("logger nil, want nop logger")
	}
	if m.sessions == nil {
		t.Error("sessions map nil")
	}
}

// TestManagerDefaultFactories 校验默认工厂按 UseACP 构建，Kind 正确（不真跑进程，不调 NewSession）。
func TestManagerDefaultFactories(t *testing.T) {
	// UseACP=false：primary=Print，fallback=nil
	m := NewAgentManager(ManagerConfig{UseACP: false}, nil)
	if m.fallback != nil {
		t.Error("UseACP=false: fallback != nil, want nil")
	}
	a, kind, err := m.primary()
	if err != nil {
		t.Fatalf("primary: %v", err)
	}
	if a == nil {
		t.Fatal("primary adapter nil")
	}
	if kind != AgentKindPrint {
		t.Errorf("UseACP=false primary kind=%q want print", kind)
	}

	// UseACP=true：primary=ACP，fallback=Print
	m2 := NewAgentManager(ManagerConfig{UseACP: true, ACPConfig: config.ACPConfig{AgentType: "claude-code"}}, nil)
	if m2.fallback == nil {
		t.Error("UseACP=true: fallback == nil, want Print factory")
	}
	a2, kind2, err := m2.primary()
	if err != nil {
		t.Fatalf("primary: %v", err)
	}
	if a2 == nil {
		t.Fatal("primary adapter nil")
	}
	if kind2 != AgentKindACP {
		t.Errorf("UseACP=true primary kind=%q want acp", kind2)
	}
	fb, fbKind, fbErr := m2.fallback()
	if fbErr != nil {
		t.Fatalf("fallback: %v", fbErr)
	}
	if fb == nil {
		t.Fatal("fallback adapter nil")
	}
	if fbKind != AgentKindPrint {
		t.Errorf("fallback kind=%q want print", fbKind)
	}
}

// TestManagerOpen_Success 校验 Open 成功：返回 adapter、fellBack=false、查询方法正确，
// fake.NewSession 调一次且 Cwd 透传。
func TestManagerOpen_Success(t *testing.T) {
	m := NewAgentManager(ManagerConfig{}, nil)
	fa := newFakeAdapter("sess-1")
	m.primary = fakeFactory(fa, AgentKindACP)
	m.fallback = nil

	adapter, fellBack, err := m.Open(context.Background(), "task-1", "proj-1", SessionConfig{Cwd: "/tmp"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if fellBack {
		t.Error("fellBack=true, want false")
	}
	if adapter == nil {
		t.Fatal("adapter nil")
	}
	if got := fa.newSessionCount(); got != 1 {
		t.Errorf("NewSession calls=%d want 1", got)
	}
	if cwds := fa.newSessionCwdsCopy(); len(cwds) != 1 || cwds[0] != "/tmp" {
		t.Errorf("NewSession Cwd=%v want [/tmp]", cwds)
	}
	if m.Adapter("task-1") != adapter {
		t.Error("Adapter() mismatch")
	}
	if m.SessionID("task-1") != "sess-1" {
		t.Errorf("SessionID=%q want sess-1", m.SessionID("task-1"))
	}
	if m.Kind("task-1") != AgentKindACP {
		t.Errorf("Kind=%q want acp", m.Kind("task-1"))
	}
	if m.FellBack("task-1") {
		t.Error("FellBack=true want false")
	}
	// 未知 taskID 查询返回零值
	if m.Adapter("nope") != nil {
		t.Error("Adapter(nope) != nil")
	}
	if m.SessionID("nope") != "" {
		t.Errorf("SessionID(nope)=%q want empty", m.SessionID("nope"))
	}
	if m.Kind("nope") != "" {
		t.Errorf("Kind(nope)=%q want empty", m.Kind("nope"))
	}
	if m.FellBack("nope") {
		t.Error("FellBack(nope)=true want false")
	}
}

// TestManagerOpen_FallbackOnPrimaryFactoryError 校验 4.3：primary 工厂失败 → 回退 fallback，
// fellBack=true、Kind=print、SessionID 来自 fallback；onFallback hook 异步带 primaryErr 触发。
func TestManagerOpen_FallbackOnPrimaryFactoryError(t *testing.T) {
	m := NewAgentManager(ManagerConfig{}, nil)
	primaryErr := errors.New("primary factory boom")
	fb := newFakeAdapter("fb-sess")
	m.primary = errFactory(primaryErr)
	m.fallback = fakeFactory(fb, AgentKindPrint)

	hookTaskID := make(chan string, 1)
	hookErr := make(chan error, 1)
	m.SetFallbackHook(func(taskID string, perr error) {
		hookTaskID <- taskID
		hookErr <- perr
	})

	adapter, fellBack, err := m.Open(context.Background(), "task-1", "proj-1", SessionConfig{Cwd: "/tmp"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !fellBack {
		t.Error("fellBack=false, want true")
	}
	if adapter == nil {
		t.Fatal("adapter nil")
	}
	if m.Kind("task-1") != AgentKindPrint {
		t.Errorf("Kind=%q want print", m.Kind("task-1"))
	}
	if m.SessionID("task-1") != "fb-sess" {
		t.Errorf("SessionID=%q want fb-sess", m.SessionID("task-1"))
	}
	if got := fb.newSessionCount(); got != 1 {
		t.Errorf("fallback NewSession calls=%d want 1", got)
	}

	select {
	case id := <-hookTaskID:
		if id != "task-1" {
			t.Errorf("hook taskID=%q want task-1", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fallback hook not called")
	}
	select {
	case e := <-hookErr:
		if !errors.Is(e, primaryErr) {
			t.Errorf("hook err=%v, want primaryErr", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fallback hook err not received")
	}
}

// TestManagerOpen_FallbackOnNewSessionError 校验 4.3：primary 工厂成功但 NewSession 失败也回退。
// primary 的 NewSession 被尝试一次且其 adapter 被 Close，回退到 fallback 成功。
func TestManagerOpen_FallbackOnNewSessionError(t *testing.T) {
	m := NewAgentManager(ManagerConfig{}, nil)
	primary := newFakeAdapter("p-sess")
	primary.newSessionErr = errors.New("primary new session boom")
	fb := newFakeAdapter("fb-sess")
	m.primary = fakeFactory(primary, AgentKindACP)
	m.fallback = fakeFactory(fb, AgentKindPrint)

	adapter, fellBack, err := m.Open(context.Background(), "task-1", "proj-1", SessionConfig{Cwd: "/tmp"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !fellBack {
		t.Error("fellBack=false, want true (primary NewSession failed → fallback)")
	}
	if adapter == nil {
		t.Fatal("adapter nil")
	}
	if m.Kind("task-1") != AgentKindPrint {
		t.Errorf("Kind=%q want print", m.Kind("task-1"))
	}
	if m.SessionID("task-1") != "fb-sess" {
		t.Errorf("SessionID=%q want fb-sess", m.SessionID("task-1"))
	}
	if got := primary.newSessionCount(); got != 1 {
		t.Errorf("primary NewSession calls=%d want 1", got)
	}
	if got := primary.closeCount(); got != 1 {
		t.Errorf("primary Close calls=%d want 1 (closed before fallback)", got)
	}
	if got := fb.newSessionCount(); got != 1 {
		t.Errorf("fallback NewSession calls=%d want 1", got)
	}
}

// TestManagerOpen_AllFail_SemReleased 校验 primary/fallback 全失败时 Open 返回错误，
// 且并发槽被释放（同 projectID 可再 Open 成功，验证 sem 未泄漏）。
func TestManagerOpen_AllFail_SemReleased(t *testing.T) {
	m := NewAgentManager(ManagerConfig{MaxConcurrent: 1}, nil)
	m.primary = errFactory(errors.New("primary boom"))
	m.fallback = errFactory(errors.New("fallback boom"))

	ctx := context.Background()
	if _, _, err := m.Open(ctx, "task-1", "proj-1", SessionConfig{Cwd: "/tmp"}); err == nil {
		t.Fatal("Open returned nil, want error (both factories fail)")
	}

	// sem 应已释放：同 projectID 可再 Open 成功
	fa := newFakeAdapter("sess-2")
	m.primary = fakeFactory(fa, AgentKindACP)
	m.fallback = nil
	select {
	case r := <-openAsync(m, ctx, "task-2", "proj-1", "/tmp"):
		if r.err != nil {
			t.Fatalf("second Open: %v (sem not released?)", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second Open blocked, sem not released after all-fail Open")
	}
}

// TestManagerOpen_DuplicateTaskID 校验已 Open 的 taskID 再次 Open 返回错误。
func TestManagerOpen_DuplicateTaskID(t *testing.T) {
	m := NewAgentManager(ManagerConfig{}, nil)
	m.primary = fakeFactory(newFakeAdapter("sess-1"), AgentKindACP)

	ctx := context.Background()
	if _, _, err := m.Open(ctx, "task-1", "proj-1", SessionConfig{Cwd: "/tmp"}); err != nil {
		t.Fatalf("Open 1: %v", err)
	}
	_, _, err := m.Open(ctx, "task-1", "proj-1", SessionConfig{Cwd: "/tmp"})
	if err == nil {
		t.Fatal("second Open returned nil, want error (duplicate taskID)")
	}
	if !strings.Contains(err.Error(), "already open") {
		t.Errorf("err=%q want contains 'already open'", err.Error())
	}
}

// TestManagerRun_Success 校验 Open 后 Run 调 fake.SendPrompt 一次，透传 prompt，返回 nil。
func TestManagerRun_Success(t *testing.T) {
	m := NewAgentManager(ManagerConfig{}, nil)
	fa := newFakeAdapter("sess-1")
	m.primary = fakeFactory(fa, AgentKindACP)

	ctx := context.Background()
	if _, _, err := m.Open(ctx, "task-1", "proj-1", SessionConfig{Cwd: "/tmp"}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := m.Run(ctx, "task-1", "hello"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := fa.sendPromptCount(); got != 1 {
		t.Errorf("SendPrompt calls=%d want 1", got)
	}
	arg := fa.firstSendArg()
	if arg.sessionID != "sess-1" || arg.prompt != "hello" {
		t.Errorf("SendPrompt arg=%+v want sess-1/hello", arg)
	}
}

// TestManagerRun_UnknownTask 校验 Run 未知 taskID 返回错误。
func TestManagerRun_UnknownTask(t *testing.T) {
	m := NewAgentManager(ManagerConfig{}, nil)
	if err := m.Run(context.Background(), "nope", "hi"); err == nil {
		t.Fatal("Run unknown task returned nil, want error")
	}
}

// TestManagerRun_ConcurrentReject 校验同一 taskID 两个 goroutine 并发 Run，
// 第二个返回 "already running"（fake.SendPrompt 阻塞控制）。
func TestManagerRun_ConcurrentReject(t *testing.T) {
	m := NewAgentManager(ManagerConfig{}, nil)
	fa := newFakeAdapter("sess-1")
	fa.sendPromptBlock = true
	fa.sendPromptStarted = make(chan struct{}, 1)
	m.primary = fakeFactory(fa, AgentKindACP)

	ctx := context.Background()
	if _, _, err := m.Open(ctx, "task-1", "proj-1", SessionConfig{Cwd: "/tmp"}); err != nil {
		t.Fatalf("Open: %v", err)
	}

	runDone := make(chan error, 2)
	go func() { runDone <- m.Run(ctx, "task-1", "hello") }()

	// 等第一个 Run 已进入 SendPrompt（此时 running=true 已设置）
	select {
	case <-fa.sendPromptStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for first Run to enter SendPrompt")
	}

	// 第二个并发 Run：应被拒绝
	if err := m.Run(ctx, "task-1", "again"); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second Run err=%v, want 'already running'", err)
	}

	// 清理：Close 中断第一个 Run
	if err := m.Close("task-1"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-runDone:
	case <-time.After(2 * time.Second):
		t.Fatal("first Run did not return after Close")
	}
}

// TestManagerCancel 校验 Run 阻塞中调 Cancel：fake.SendPrompt 的 ctx 被取消（Run 返回 ctx 错误），
// 且 fake.Cancel 被调用（协作取消）。
func TestManagerCancel(t *testing.T) {
	m := NewAgentManager(ManagerConfig{}, nil)
	fa := newFakeAdapter("sess-1")
	fa.sendPromptBlock = true
	fa.sendPromptStarted = make(chan struct{}, 1)
	m.primary = fakeFactory(fa, AgentKindACP)

	ctx := context.Background()
	if _, _, err := m.Open(ctx, "task-1", "proj-1", SessionConfig{Cwd: "/tmp"}); err != nil {
		t.Fatalf("Open: %v", err)
	}

	runDone := make(chan error, 1)
	go func() { runDone <- m.Run(ctx, "task-1", "hello") }()

	select {
	case <-fa.sendPromptStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for Run to enter SendPrompt")
	}

	if err := m.Cancel(ctx, "task-1"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	select {
	case err := <-runDone:
		if err == nil {
			t.Fatal("Run returned nil, want ctx error")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run err=%v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after Cancel")
	}

	// adapter.Cancel 被调用一次（协作取消）
	if got := fa.cancelCount(); got != 1 {
		t.Errorf("adapter.Cancel calls=%d want 1", got)
	}
}

// TestManagerCancel_NoSession 校验无 session 时 Cancel no-op 返回 nil。
func TestManagerCancel_NoSession(t *testing.T) {
	m := NewAgentManager(ManagerConfig{}, nil)
	if err := m.Cancel(context.Background(), "nope"); err != nil {
		t.Errorf("Cancel no session err=%v, want nil", err)
	}
}

// TestManagerClose_IdempotentAndSemRelease 校验 Close 幂等 + 释放 sem。
// Close 后再 Close 不 panic；同 projectID 可再 Open（sem 释放）；Close 后 Adapter 返回 nil。
func TestManagerClose_IdempotentAndSemRelease(t *testing.T) {
	m := NewAgentManager(ManagerConfig{MaxConcurrent: 1}, nil)
	m.primary = fakeFactory(newFakeAdapter("sess-1"), AgentKindACP)

	ctx := context.Background()
	if _, _, err := m.Open(ctx, "task-1", "proj-1", SessionConfig{Cwd: "/tmp"}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := m.Close("task-1"); err != nil {
		t.Fatalf("Close 1: %v", err)
	}
	if err := m.Close("task-1"); err != nil { // 幂等
		t.Fatalf("Close 2 (idempotent): %v", err)
	}
	if m.Adapter("task-1") != nil {
		t.Error("Adapter(task-1) != nil after Close")
	}

	// sem 已释放：同 projectID 可再 Open
	m.primary = fakeFactory(newFakeAdapter("sess-2"), AgentKindACP)
	select {
	case r := <-openAsync(m, ctx, "task-2", "proj-1", "/tmp"):
		if r.err != nil {
			t.Fatalf("second Open: %v (sem not released by Close?)", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second Open blocked, sem not released by Close")
	}
}

// TestManagerOpen_ConcurrencyLimit 校验 MaxConcurrent=1 时同 projectID 两个 Open 并发，
// 第二个阻塞直到第一个 Close。
func TestManagerOpen_ConcurrencyLimit(t *testing.T) {
	m := NewAgentManager(ManagerConfig{MaxConcurrent: 1}, nil)
	m.primary = fakeFactory(newFakeAdapter("sess-1"), AgentKindACP)

	ctx := context.Background()
	if _, _, err := m.Open(ctx, "task-1", "proj-1", SessionConfig{Cwd: "/tmp"}); err != nil {
		t.Fatalf("Open 1: %v", err)
	}

	// task-2 同 projectID：应阻塞（sem 满）
	m.primary = fakeFactory(newFakeAdapter("sess-2"), AgentKindACP)
	res2 := openAsync(m, ctx, "task-2", "proj-1", "/tmp")
	select {
	case <-res2:
		t.Fatal("second Open returned before first Close (concurrency limit not enforced)")
	case <-time.After(100 * time.Millisecond):
		// 预期：仍阻塞
	}

	// 释放 task-1：task-2 应获得 sem 并完成
	if err := m.Close("task-1"); err != nil {
		t.Fatalf("Close task-1: %v", err)
	}
	select {
	case r := <-res2:
		if r.err != nil {
			t.Fatalf("second Open: %v", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second Open did not complete after first Close")
	}
}

// TestManagerCloseAll 校验 Open 两个 task 后 CloseAll 全部关闭。
func TestManagerCloseAll(t *testing.T) {
	m := NewAgentManager(ManagerConfig{}, nil)
	fa := newFakeAdapter("sess-1")
	m.primary = fakeFactory(fa, AgentKindACP)

	ctx := context.Background()
	if _, _, err := m.Open(ctx, "task-1", "proj-1", SessionConfig{Cwd: "/tmp"}); err != nil {
		t.Fatalf("Open 1: %v", err)
	}
	if _, _, err := m.Open(ctx, "task-2", "proj-1", SessionConfig{Cwd: "/tmp"}); err != nil {
		t.Fatalf("Open 2: %v", err)
	}
	if m.Adapter("task-1") == nil || m.Adapter("task-2") == nil {
		t.Fatal("adapters nil before CloseAll")
	}
	if err := m.CloseAll(); err != nil {
		t.Fatalf("CloseAll: %v", err)
	}
	if m.Adapter("task-1") != nil || m.Adapter("task-2") != nil {
		t.Fatal("adapters not nil after CloseAll")
	}
}

// --- 空闲回收（reaper） ---

// TestManagerReaper_ClosesIdleSession 空闲回收：会话 idle 超过 IdleTimeout 且未在 running，
// CloseIdle 关闭它——adapter.Close 被调、onSessionClosed 钩子触发、并发槽释放。
func TestManagerReaper_ClosesIdleSession(t *testing.T) {
	m := NewAgentManager(ManagerConfig{IdleTimeout: time.Minute}, nil)
	fa := newFakeAdapter("sess-1")
	m.primary = fakeFactory(fa, AgentKindACP)
	m.fallback = nil

	if _, _, err := m.Open(context.Background(), "task-1", "proj-1", SessionConfig{Cwd: "/tmp"}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	closed := make(chan string, 1)
	m.SetOnSessionClosed(func(taskID string) { closed <- taskID })

	// 模拟会话已空闲超过阈值：把 lastActivity 拨回过去
	sess := m.session("task-1")
	if sess == nil {
		t.Fatal("session nil after Open")
	}
	sess.runMu.Lock()
	sess.lastActivity = time.Now().Add(-2 * time.Minute)
	sess.runMu.Unlock()

	m.CloseIdle(time.Now())

	if m.Adapter("task-1") != nil {
		t.Fatal("idle session should be closed by reaper")
	}
	if fa.closeN != 1 {
		t.Fatalf("adapter Close calls=%d want 1", fa.closeN)
	}
	select {
	case id := <-closed:
		if id != "task-1" {
			t.Fatalf("onSessionClosed got %q want task-1", id)
		}
	case <-time.After(time.Second):
		t.Fatal("onSessionClosed hook not fired")
	}
	// 并发槽已释放：closeN 后再 Open 同项目不应被占用的槽阻塞
	if _, _, err := m.Open(context.Background(), "task-2", "proj-1", SessionConfig{Cwd: "/tmp"}); err != nil {
		t.Fatalf("re-open after reap: %v", err)
	}
}

// TestManagerReaper_SkipsRunningSession running 中的会话不被回收（不打断进行中的 turn）。
func TestManagerReaper_SkipsRunningSession(t *testing.T) {
	m := NewAgentManager(ManagerConfig{IdleTimeout: time.Minute}, nil)
	fa := newFakeAdapter("sess-1")
	fa.sendPromptBlock = true
	fa.sendPromptStarted = make(chan struct{}, 1)
	m.primary = fakeFactory(fa, AgentKindACP)
	m.fallback = nil

	if _, _, err := m.Open(context.Background(), "task-1", "proj-1", SessionConfig{Cwd: "/tmp"}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	runDone := make(chan error, 1)
	go func() { runDone <- m.Run(context.Background(), "task-1", "hi") }()
	select {
	case <-fa.sendPromptStarted:
	case <-time.After(time.Second):
		t.Fatal("SendPrompt not started")
	}

	// 即使 lastActivity 很旧，running 中的会话也不回收
	sess := m.session("task-1")
	sess.runMu.Lock()
	sess.lastActivity = time.Now().Add(-2 * time.Minute)
	sess.runMu.Unlock()
	m.CloseIdle(time.Now())

	if m.Adapter("task-1") == nil {
		t.Fatal("running session should NOT be closed by reaper")
	}

	// 收尾：取消 Run 释放阻塞，关会话
	_ = m.Cancel(context.Background(), "task-1")
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after Cancel")
	}
	_ = m.Close("task-1")
}

// TestManagerStartStopReaper StartReaper 周期性回收 + StopReaper 停止（幂等）。
func TestManagerStartStopReaper(t *testing.T) {
	m := NewAgentManager(ManagerConfig{IdleTimeout: 50 * time.Millisecond}, nil)
	fa := newFakeAdapter("sess-1")
	m.primary = fakeFactory(fa, AgentKindACP)
	m.fallback = nil

	if _, _, err := m.Open(context.Background(), "task-1", "proj-1", SessionConfig{Cwd: "/tmp"}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	m.StartReaper(20 * time.Millisecond)
	// Open 后未跑 Run，lastActivity 为 zero time（远早于阈值）→ 首个 tick 即回收
	deadline := time.Now().Add(2 * time.Second)
	for m.Adapter("task-1") != nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if m.Adapter("task-1") != nil {
		t.Fatal("reaper did not close idle session")
	}
	m.StopReaper()
	m.StopReaper() // 幂等
}

// TestAgentManager_Open_ResumeFrom 校验 Open 把 SessionConfig.ResumeFrom 透传给 primary NewSession。
func TestAgentManager_Open_ResumeFrom(t *testing.T) {
	m := NewAgentManager(ManagerConfig{}, nil)
	fa := newFakeAdapter("sess-resumed")
	m.primary = fakeFactory(fa, AgentKindACP)
	m.fallback = nil

	cfg := SessionConfig{Cwd: "/tmp", ResumeFrom: "acp-sid-prev"}
	adapter, fellBack, err := m.Open(context.Background(), "task-1", "proj-1", cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if fellBack || adapter == nil {
		t.Fatalf("fellBack=%v adapter=%v", fellBack, adapter)
	}
	if got := fa.newSessionCount(); got != 1 {
		t.Fatalf("NewSession calls=%d want 1", got)
	}
	last, ok := fa.lastNewSessionCfg()
	if !ok {
		t.Fatal("no NewSession cfg recorded")
	}
	if last.Cwd != "/tmp" || last.ResumeFrom != "acp-sid-prev" {
		t.Errorf("cfg=%+v want Cwd=/tmp ResumeFrom=acp-sid-prev", last)
	}
	if m.SessionID("task-1") != "sess-resumed" {
		t.Errorf("SessionID=%q want sess-resumed", m.SessionID("task-1"))
	}
}

// TestAgentManager_Open_ResumePrimaryFails_Fallback 校验 primary NewSession 失败时回退到 fallback，
// 且 fallback 收到带 ResumeFrom 的 cfg（回退路径也支持 resume）。
func TestAgentManager_Open_ResumePrimaryFails_Fallback(t *testing.T) {
	m := NewAgentManager(ManagerConfig{}, nil)
	primary := newFakeAdapter("p-sess")
	primary.newSessionErr = errors.New("primary new session boom")
	fb := newFakeAdapter("fb-sess")
	m.primary = fakeFactory(primary, AgentKindACP)
	m.fallback = fakeFactory(fb, AgentKindPrint)

	cfg := SessionConfig{Cwd: "/tmp", ResumeFrom: "acp-sid-prev"}
	adapter, fellBack, err := m.Open(context.Background(), "task-1", "proj-1", cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !fellBack || adapter == nil {
		t.Fatalf("fellBack=%v adapter=%v", fellBack, adapter)
	}
	if got := primary.newSessionCount(); got != 1 {
		t.Errorf("primary NewSession calls=%d want 1", got)
	}
	if got := fb.newSessionCount(); got != 1 {
		t.Fatalf("fallback NewSession calls=%d want 1", got)
	}
	last, ok := fb.lastNewSessionCfg()
	if !ok {
		t.Fatal("no fallback NewSession cfg recorded")
	}
	if last.Cwd != "/tmp" || last.ResumeFrom != "acp-sid-prev" {
		t.Errorf("fallback cfg=%+v want Cwd=/tmp ResumeFrom=acp-sid-prev", last)
	}
}

// TestManagerConfigFromPieqi 校验由 config.PieqiConfig 构建 ManagerConfig 的字段映射。
func TestManagerConfigFromPieqi(t *testing.T) {
	pieqi := config.PieqiConfig{
		PermissionMode:          "bypassPermissions",
		MaxConcurrentPerProject: 3,
		ACP: config.ACPConfig{
			UseACP:       true,
			AgentType:    "qodercli",
			SpawnCommand: []string{"qodercli", "--acp", "--port", "1234"},
			InitTimeout:  45 * time.Second,
		},
	}

	mc := ManagerConfigFromPieqi(pieqi)

	if !mc.UseACP {
		t.Errorf("UseACP=false, want true")
	}
	if mc.MaxConcurrent != 3 {
		t.Errorf("MaxConcurrent=%d want 3", mc.MaxConcurrent)
	}
	if mc.ACPConfig.AgentType != "qodercli" {
		t.Errorf("ACPConfig.AgentType=%q want qodercli", mc.ACPConfig.AgentType)
	}
	if len(mc.ACPConfig.SpawnCommand) != 4 || mc.ACPConfig.SpawnCommand[0] != "qodercli" {
		t.Errorf("ACPConfig.SpawnCommand=%v want [qodercli --acp --port 1234]", mc.ACPConfig.SpawnCommand)
	}
	if mc.PrintConfig.Model != "" {
		t.Errorf("PrintConfig.Model=%q want empty (claude 自决)", mc.PrintConfig.Model)
	}
	if mc.PrintConfig.PermissionMode != "bypassPermissions" {
		t.Errorf("PrintConfig.PermissionMode=%q want bypassPermissions", mc.PrintConfig.PermissionMode)
	}
	// 确认由 helper 构建的 ManagerConfig 能直接喂给 NewAgentManager（UseACP=true → ACP 主 + Print 回退）
	m := NewAgentManager(mc, nil)
	if m.fallback == nil {
		t.Error("UseACP=true: fallback factory is nil, want Print factory")
	}
}

// TestManagerConfigFromPieqi_UseACPFalse 校验 UseACP=false 时 fallback 为 nil（仅 Print 路径）。
func TestManagerConfigFromPieqi_UseACPFalse(t *testing.T) {
	pieqi := config.PieqiConfig{
		PermissionMode:          "plan",
		MaxConcurrentPerProject: 0, // 不限
		ACP:                     config.ACPConfig{UseACP: false},
	}

	mc := ManagerConfigFromPieqi(pieqi)
	if mc.UseACP {
		t.Errorf("UseACP=true, want false")
	}
	if mc.MaxConcurrent != 0 {
		t.Errorf("MaxConcurrent=%d want 0", mc.MaxConcurrent)
	}
	m := NewAgentManager(mc, nil)
	if m.fallback != nil {
		t.Error("UseACP=false: fallback factory != nil, want nil")
	}
}
