package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"pieqi/internal/agent"
	"pieqi/internal/model"
)

// fakePermAdapter 测试用 AgentAdapter：存储 OnPermissionRequest/OnToolCallUpdate 回调供手动触发，
// 并记录 Approve/Deny 调用。模拟 ACP RequestPermission/SessionUpdate 到达时触发已注册回调。
type fakePermAdapter struct {
	onDelta    agent.ContentDeltaFunc
	onPerm     agent.PermissionRequestFunc
	onToolCall agent.ToolCallUpdateFunc
	done       chan struct{}

	mu           sync.Mutex
	approveCalls []fakeApproveCall
	denyCalls    []string
}

type fakeApproveCall struct {
	reqID    string
	optionID string
}

func newFakePermAdapter() *fakePermAdapter {
	return &fakePermAdapter{done: make(chan struct{})}
}

func (f *fakePermAdapter) NewSession(context.Context, agent.SessionConfig) (string, error) {
	return "sess", nil
}
func (f *fakePermAdapter) RealSessionID(sessionID string) string              { return sessionID }
func (f *fakePermAdapter) SendPrompt(context.Context, string, string) error   { return nil }
func (f *fakePermAdapter) OnContentDelta(fn agent.ContentDeltaFunc)           { f.onDelta = fn }
func (f *fakePermAdapter) OnPermissionRequest(fn agent.PermissionRequestFunc) { f.onPerm = fn }
func (f *fakePermAdapter) OnToolCallUpdate(fn agent.ToolCallUpdateFunc)       { f.onToolCall = fn }
func (f *fakePermAdapter) Approve(_ context.Context, reqID, optionID string) error {
	f.mu.Lock()
	f.approveCalls = append(f.approveCalls, fakeApproveCall{reqID, optionID})
	f.mu.Unlock()
	return nil
}
func (f *fakePermAdapter) Deny(_ context.Context, reqID string) error {
	f.mu.Lock()
	f.denyCalls = append(f.denyCalls, reqID)
	f.mu.Unlock()
	return nil
}
func (f *fakePermAdapter) RespondPermission(ctx context.Context, reqID string, allow bool, optionID string) error {
	if allow {
		return f.Approve(ctx, reqID, optionID)
	}
	return f.Deny(ctx, reqID)
}
func (f *fakePermAdapter) InjectToolResult(context.Context, string, string, string, bool) error {
	return nil
}
func (f *fakePermAdapter) Cancel(context.Context, string) error { return nil }
func (f *fakePermAdapter) Close(context.Context) error          { return nil }
func (f *fakePermAdapter) Done() <-chan struct{}                { return f.done }

// emitPerm 手动触发已注册的 OnPermissionRequest 回调（模拟 ACP RequestPermission 到达）。
func (f *fakePermAdapter) emitPerm(req agent.PermissionRequest) {
	if f.onPerm != nil {
		f.onPerm(req)
	}
}

// emitToolCall 手动触发已注册的 OnToolCallUpdate 回调（模拟 ACP SessionUpdate 到达）。
func (f *fakePermAdapter) emitToolCall(info agent.ToolCallUpdateInfo) {
	if f.onToolCall != nil {
		f.onToolCall(info)
	}
}

func (f *fakePermAdapter) approveCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.approveCalls)
}
func (f *fakePermAdapter) denyCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.denyCalls)
}
func (f *fakePermAdapter) lastApprove() (string, string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.approveCalls) == 0 {
		return "", "", false
	}
	c := f.approveCalls[len(f.approveCalls)-1]
	return c.reqID, c.optionID, true
}

// setupPermWire 构造一套 wired 环境：fake adapter + bus + 订阅 + store + 一个 running task。
// task 带 IM 来源渠道，便于验证 notify 回调。免审名单默认关闭（nil）。
func setupPermWire(t *testing.T, timeout time.Duration) (*fakePermAdapter, *EventBus, *Subscription, *TaskStore, string, *PermissionWire, *[]string) {
	t.Helper()
	return setupPermWireAuto(t, timeout, nil)
}

// setupPermWireAuto 同 setupPermWire，但按 autoApprove 名单配置免审。
func setupPermWireAuto(t *testing.T, timeout time.Duration, autoApprove []string) (*fakePermAdapter, *EventBus, *Subscription, *TaskStore, string, *PermissionWire, *[]string) {
	t.Helper()
	bus := NewEventBus()
	sub := bus.Subscribe(64)
	store, err := NewTaskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tt, err := store.Create(&model.Task{
		ProjectID:      "p",
		Prompt:         "hi",
		Status:         model.TaskRunning,
		OriginChannel:  "im",
		OriginChatID:   "c1",
		OriginIdentity: "u1",
	})
	if err != nil {
		t.Fatal(err)
	}
	fa := newFakePermAdapter()
	var notifyTexts []string
	var notifyMu sync.Mutex
	notify := func(_ *model.Task, text string) {
		notifyMu.Lock()
		notifyTexts = append(notifyTexts, text)
		notifyMu.Unlock()
	}
	pw := WirePermission(fa, bus, store, tt.ID, notify, timeout, autoApprove, nil)
	return fa, bus, sub, store, tt.ID, pw, &notifyTexts
}

// standardOptions 一组典型的 ACP 权限选项（allow_once/allow_always/reject_once/reject_always）。
func standardOptions() []agent.PermissionOption {
	return []agent.PermissionOption{
		{ID: "o1", Name: "Allow Once", Kind: agent.PermissionOptionAllowOnce},
		{ID: "o2", Name: "Allow Always", Kind: agent.PermissionOptionAllowAlways},
		{ID: "o3", Name: "Reject Once", Kind: agent.PermissionOptionRejectOnce},
		{ID: "o4", Name: "Reject Always", Kind: agent.PermissionOptionRejectAlways},
	}
}

// permReq 构造一个权限请求。
func permReq(reqID, title, kind string, opts []agent.PermissionOption) agent.PermissionRequest {
	return agent.PermissionRequest{
		ReqID:      reqID,
		SessionID:  "sess",
		ToolCallID: reqID,
		ToolTitle:  title,
		ToolKind:   kind,
		Options:    opts,
	}
}

// waitForStatus 轮询 store 直到 task 进入期望状态或超时。
func waitForStatus(t *testing.T, store *TaskStore, taskID string, want model.TaskStatus, wait time.Duration) {
	t.Helper()
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if tt, ok := store.Get(taskID); ok && tt.Status == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	tt, _ := store.Get(taskID)
	t.Fatalf("task status=%q, want %q (timed out)", tt.Status, want)
}

// findTaskUpdated 在事件列表中找到第一个 task_updated 事件，返回其 Task。
func findTaskUpdated(t *testing.T, evs []Event) *model.Task {
	t.Helper()
	for _, ev := range evs {
		if ev.Type == "task_updated" && ev.Task != nil {
			return ev.Task
		}
	}
	t.Fatalf("no task_updated event in %+v", evs)
	return nil
}

// TestWirePermission_RequestTriggersWaitingInput 收到权限请求 → task 进 waiting_input +
// CurrentDecision 正确 + task_updated 发出 + IM notify 调用。
func TestWirePermission_RequestTriggersWaitingInput(t *testing.T) {
	fa, _, sub, store, taskID, pw, notifyTexts := setupPermWire(t, 30*time.Minute)
	defer pw.Unwire()

	fa.emitPerm(permReq("req-1", "Bash", "execute", standardOptions()))

	evs := drainEvents(sub, 80*time.Millisecond)
	tt := findTaskUpdated(t, evs)

	if tt.Status != model.TaskWaitingInput {
		t.Fatalf("status=%q, want waiting_input", tt.Status)
	}
	if tt.CurrentDecision == nil {
		t.Fatal("CurrentDecision nil")
	}
	cd := tt.CurrentDecision
	if cd.ID != "req-1" {
		t.Errorf("decision.ID=%q, want req-1", cd.ID)
	}
	if cd.Kind != model.DecisionKindApproval {
		t.Errorf("decision.Kind=%q, want approval", cd.Kind)
	}
	// 标题是"一屏读得完的一句话"：这条请求的 title 本来就短（"Bash"），原样保留。
	if cd.ToolName != "Bash" {
		t.Errorf("decision.ToolName=%q, want Bash", cd.ToolName)
	}
	// 摘要是要人批的正文：**全文原样**，不再拼 " (execute)"（kind 语义已由标题/风险承载）。
	if cd.Summary != "Bash" {
		t.Errorf("decision.Summary=%q, want 原样 title", cd.Summary)
	}
	// approve_session 出现在 Options 里 = 告诉前端"这条路径支持同类免审"。
	wantOpts := []string{"approve", "deny", "approve_session"}
	if len(cd.Options) != len(wantOpts) {
		t.Fatalf("decision.Options=%v, want %v", cd.Options, wantOpts)
	}
	for i, w := range wantOpts {
		if cd.Options[i] != w {
			t.Errorf("decision.Options[%d]=%q, want %q", i, cd.Options[i], w)
		}
	}

	// IM notify 应被调用一次，文案含任务号、风险等级与摘要。
	// 文案刻意不再写"需要决策"——IM 那条要能一眼看完"什么任务、什么操作、多重"，
	// 而任务的等待状态由卡片/PWA 呈现，重复一遍只是占屏。
	if len(*notifyTexts) == 0 {
		t.Fatal("IM notify not called")
	}
	if !strings.Contains((*notifyTexts)[0], taskID[:8]) {
		t.Errorf("notify text=%q, want contain task id %q", (*notifyTexts)[0], taskID[:8])
	}
	// kind=execute → L2，文案必须标出档位（手机上据此决定要不要细看）。
	if !strings.Contains((*notifyTexts)[0], "L2") {
		t.Errorf("notify text=%q, want contain risk level 'L2'", (*notifyTexts)[0])
	}

	// store 持久化的状态与事件一致。
	persisted, ok := store.Get(taskID)
	if !ok || persisted.Status != model.TaskWaitingInput || persisted.CurrentDecision == nil {
		t.Fatalf("store not persisted to waiting_input: %+v", persisted)
	}
}

// TestWirePermission_ResolveApprove 批准 → adapter.Approve 被调（带首个 allow optionID=o1）+
// task 回 running + 定时器停止（之后不再触发 Deny）。
func TestWirePermission_ResolveApprove(t *testing.T) {
	fa, _, _, store, taskID, pw, _ := setupPermWire(t, 80*time.Millisecond)
	defer pw.Unwire()

	fa.emitPerm(permReq("req-2", "Write", "edit", standardOptions()))
	waitForStatus(t, store, taskID, model.TaskWaitingInput, time.Second)

	if err := pw.Resolve("req-2", "approve"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// Approve 应被调，optionID 为首个 allow_once（o1）。
	if got := fa.approveCount(); got != 1 {
		t.Fatalf("approve calls=%d, want 1", got)
	}
	if _, opt, ok := fa.lastApprove(); !ok || opt != "o1" {
		t.Fatalf("approve optionID=%q, want o1", opt)
	}
	// task 回 running，CurrentDecision 清空。
	waitForStatus(t, store, taskID, model.TaskRunning, time.Second)
	tt, _ := store.Get(taskID)
	if tt.CurrentDecision != nil {
		t.Errorf("CurrentDecision not cleared: %+v", tt.CurrentDecision)
	}

	// 等待超过原超时阈值，确认定时器已停止（未触发 Deny）。
	time.Sleep(180 * time.Millisecond)
	if got := fa.denyCount(); got != 0 {
		t.Errorf("deny calls=%d after Resolve, want 0 (timer should be stopped)", got)
	}
}

// TestWirePermission_ResolveApprovePicksAllowAlways 无 allow_once 时 approve 选 allow_always。
func TestWirePermission_ResolveApprovePicksAllowAlways(t *testing.T) {
	fa, _, _, store, taskID, pw, _ := setupPermWire(t, time.Minute)
	defer pw.Unwire()

	opts := []agent.PermissionOption{
		{ID: "oA", Name: "Allow Always", Kind: agent.PermissionOptionAllowAlways},
		{ID: "oR", Name: "Reject Once", Kind: agent.PermissionOptionRejectOnce},
	}
	fa.emitPerm(permReq("req-A", "Edit", "", opts))
	waitForStatus(t, store, taskID, model.TaskWaitingInput, time.Second)

	if err := pw.Resolve("req-A", "approve"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if _, opt, ok := fa.lastApprove(); !ok || opt != "oA" {
		t.Fatalf("approve optionID=%q, want oA (allow_always)", opt)
	}
}

// TestWirePermission_ResolveDenyWithRejectOption deny 且有 reject 选项 → 用 Approve 选中 reject_once。
func TestWirePermission_ResolveDenyWithRejectOption(t *testing.T) {
	fa, _, _, store, taskID, pw, _ := setupPermWire(t, time.Minute)
	defer pw.Unwire()

	fa.emitPerm(permReq("req-D", "Bash", "execute", standardOptions()))
	waitForStatus(t, store, taskID, model.TaskWaitingInput, time.Second)

	if err := pw.Resolve("req-D", "deny"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// 选 reject_once（o3）经 Approve 投递，Deny 不应被调。
	if got := fa.approveCount(); got != 1 {
		t.Fatalf("approve calls=%d, want 1 (deny via selecting reject option)", got)
	}
	if _, opt, ok := fa.lastApprove(); !ok || opt != "o3" {
		t.Fatalf("approve optionID=%q, want o3 (reject_once)", opt)
	}
	if got := fa.denyCount(); got != 0 {
		t.Errorf("deny calls=%d, want 0 (reject option selected, not Deny)", got)
	}
	waitForStatus(t, store, taskID, model.TaskRunning, time.Second)
}

// TestWirePermission_ResolveDenyNoRejectOption deny 且无 reject 选项 → adapter.Deny 被调（→ Cancelled）。
func TestWirePermission_ResolveDenyNoRejectOption(t *testing.T) {
	fa, _, _, store, taskID, pw, _ := setupPermWire(t, time.Minute)
	defer pw.Unwire()

	opts := []agent.PermissionOption{
		{ID: "o1", Name: "Allow Once", Kind: agent.PermissionOptionAllowOnce},
	} // 无 reject 选项
	fa.emitPerm(permReq("req-D2", "Bash", "execute", opts))
	waitForStatus(t, store, taskID, model.TaskWaitingInput, time.Second)

	if err := pw.Resolve("req-D2", "deny"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := fa.denyCount(); got != 1 {
		t.Fatalf("deny calls=%d, want 1", got)
	}
	if fa.approveCount() != 0 {
		t.Errorf("approve calls=%d, want 0", fa.approveCount())
	}
	waitForStatus(t, store, taskID, model.TaskRunning, time.Second)
}

// TestWirePermission_Timeout 超时 → adapter.Deny 被调 + IM notify 超时 + task 回 running。
func TestWirePermission_Timeout(t *testing.T) {
	fa, _, sub, store, taskID, pw, notifyTexts := setupPermWire(t, 50*time.Millisecond)
	defer pw.Unwire()

	fa.emitPerm(permReq("req-T", "Bash", "execute", standardOptions()))
	waitForStatus(t, store, taskID, model.TaskWaitingInput, time.Second)

	// 等待超时触发（带裕量）。
	waitForStatus(t, store, taskID, model.TaskRunning, time.Second)

	if got := fa.denyCount(); got != 1 {
		t.Errorf("deny calls=%d after timeout, want 1", got)
	}
	tt, _ := store.Get(taskID)
	if tt.Status != model.TaskRunning {
		t.Errorf("status=%q, want running after timeout", tt.Status)
	}
	if tt.CurrentDecision != nil {
		t.Errorf("CurrentDecision not cleared after timeout: %+v", tt.CurrentDecision)
	}

	// 超时应再发一次 task_updated（running）。
	evs := drainEvents(sub, 80*time.Millisecond)
	sawRunningUpdate := false
	for _, ev := range evs {
		if ev.Type == "task_updated" && ev.Task != nil && ev.Task.Status == model.TaskRunning {
			sawRunningUpdate = true
		}
	}
	if !sawRunningUpdate {
		t.Errorf("no task_updated(running) after timeout: %+v", evs)
	}

	// IM notify 应含超时文案。
	found := false
	for _, txt := range *notifyTexts {
		if strings.Contains(txt, "超时") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no timeout notify text in %v", *notifyTexts)
	}
}

// TestWirePermission_ResolveAfterTimeout 超时后再 Resolve 应失败（已被处理，先到先得）。
func TestWirePermission_ResolveAfterTimeout(t *testing.T) {
	fa, _, _, store, taskID, pw, _ := setupPermWire(t, 50*time.Millisecond)
	defer pw.Unwire()

	fa.emitPerm(permReq("req-RT", "Bash", "execute", standardOptions()))
	waitForStatus(t, store, taskID, model.TaskRunning, time.Second) // 等超时把 task 置回 running

	if err := pw.Resolve("req-RT", "approve"); err == nil {
		t.Fatal("Resolve after timeout should fail")
	}
	// 超时已 Deny 一次，Resolve 不应再驱动 adapter。
	if got := fa.denyCount(); got != 1 {
		t.Errorf("deny calls=%d, want 1 (only timeout)", got)
	}
	if got := fa.approveCount(); got != 0 {
		t.Errorf("approve calls=%d, want 0", got)
	}
}

// TestWirePermission_UnwireDeniesPending Unwire 时挂起的请求被 Deny，回调注销。
func TestWirePermission_UnwireDeniesPending(t *testing.T) {
	fa, _, _, store, taskID, pw, _ := setupPermWire(t, time.Minute)

	fa.emitPerm(permReq("req-U", "Bash", "execute", standardOptions()))
	waitForStatus(t, store, taskID, model.TaskWaitingInput, time.Second)

	pw.Unwire()

	if got := fa.denyCount(); got != 1 {
		t.Errorf("deny calls=%d on Unwire, want 1 (unblock pending)", got)
	}
	// Unwire 后回调应注销：再 emit 不应进入 wire（不会触发新的 waiting_input）。
	fa.emitPerm(permReq("req-U2", "Bash", "execute", standardOptions()))
	tt, _ := store.Get(taskID)
	if tt.Status == model.TaskWaitingInput && tt.CurrentDecision != nil && tt.CurrentDecision.ID == "req-U2" {
		t.Fatal("wire still active after Unwire (req-U2 created a new decision)")
	}
}

// TestWirePermission_UnwireIdempotent Unwire 多次调用不 panic。
func TestWirePermission_UnwireIdempotent(t *testing.T) {
	_, _, _, _, _, pw, _ := setupPermWire(t, time.Minute)
	pw.Unwire()
	pw.Unwire()
}

// TestWirePermission_NotifySkippedForNoChannel 无 IM 渠道的任务不触发 notify（仿 notifyWaitingInput 守卫）。
func TestWirePermission_NotifySkippedForNoChannel(t *testing.T) {
	bus := NewEventBus()
	store, err := NewTaskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tt, err := store.Create(&model.Task{ProjectID: "p", Prompt: "hi", Status: model.TaskRunning})
	if err != nil {
		t.Fatal(err)
	}
	fa := newFakePermAdapter()
	var notifyTexts []string
	pw := WirePermission(fa, bus, store, tt.ID, func(_ *model.Task, text string) {
		notifyTexts = append(notifyTexts, text)
	}, time.Minute, nil, nil)
	defer pw.Unwire()

	fa.emitPerm(permReq("req-NC", "Bash", "execute", standardOptions()))

	// 无 OriginChannel：notify 不应被调用；但 task 仍进 waiting_input（PWA 路径）。
	if len(notifyTexts) != 0 {
		t.Errorf("notify called %d times, want 0 (no IM channel)", len(notifyTexts))
	}
	got, _ := store.Get(tt.ID)
	if got.Status != model.TaskWaitingInput {
		t.Errorf("status=%q, want waiting_input (PWA path still works)", got.Status)
	}
}

// TestWirePermission_AutoApproveHit 免审名单命中（ToolKind=edit）→ 直接自动放行：
// adapter.Approve 被调（选中 allow_once）+ task 保持 running（不置 waiting_input、不建
// CurrentDecision）+ 无 waiting_input 的 task_updated + 无 IM 通知。
func TestWirePermission_AutoApproveHit(t *testing.T) {
	fa, _, sub, store, taskID, pw, notifyTexts := setupPermWireAuto(t, time.Minute, []string{"edit"})
	defer pw.Unwire()

	fa.emitPerm(permReq("req-AA", "Edit", "edit", standardOptions()))

	// adapter.Approve 应被调一次，选中 allow_once（o1）。
	if got := fa.approveCount(); got != 1 {
		t.Fatalf("approve calls=%d, want 1 (auto-approve)", got)
	}
	if _, opt, ok := fa.lastApprove(); !ok || opt != "o1" {
		t.Fatalf("approve optionID=%q, want o1 (allow_once)", opt)
	}
	if got := fa.denyCount(); got != 0 {
		t.Errorf("deny calls=%d, want 0", got)
	}

	// task 全程不被置 waiting_input（wire 不拥有状态迁移：harness 里任务初始为 pending，
	// 关键断言是自动放行不把它打断成 waiting_input）、不建 CurrentDecision。
	tt, ok := store.Get(taskID)
	if !ok || tt.Status == model.TaskWaitingInput {
		t.Fatalf("status=%q, want not waiting_input (auto-approve must not pause)", tt.Status)
	}
	if tt.CurrentDecision != nil {
		t.Errorf("CurrentDecision set: %+v (should not pause)", tt.CurrentDecision)
	}

	// 无 waiting_input 的 task_updated 事件。
	for _, ev := range drainEvents(sub, 80*time.Millisecond) {
		if ev.Type == "task_updated" && ev.Task != nil && ev.Task.Status == model.TaskWaitingInput {
			t.Errorf("unexpected task_updated(waiting_input): %+v", ev)
		}
	}

	// 无 IM 通知（自动放行不打扰用户）。
	if len(*notifyTexts) != 0 {
		t.Errorf("notify called %d times, want 0 (auto-approved)", len(*notifyTexts))
	}
}

// TestWirePermission_AutoApprovePicksAllowAlways 无 allow_once 时自动放行选 allow_always。
func TestWirePermission_AutoApprovePicksAllowAlways(t *testing.T) {
	fa, _, _, store, taskID, pw, _ := setupPermWireAuto(t, time.Minute, []string{"edit"})
	defer pw.Unwire()

	opts := []agent.PermissionOption{
		{ID: "oA", Name: "Allow Always", Kind: agent.PermissionOptionAllowAlways},
		{ID: "oR", Name: "Reject Once", Kind: agent.PermissionOptionRejectOnce},
	}
	fa.emitPerm(permReq("req-AB", "Edit", "edit", opts))

	if _, opt, ok := fa.lastApprove(); !ok || opt != "oA" {
		t.Fatalf("approve optionID=%q, want oA (allow_always)", opt)
	}
	tt, _ := store.Get(taskID)
	if tt.Status == model.TaskWaitingInput {
		t.Errorf("status=%q, want not waiting_input (auto-approve must not pause)", tt.Status)
	}
	if tt.CurrentDecision != nil {
		t.Errorf("CurrentDecision set: %+v", tt.CurrentDecision)
	}
}

// TestWirePermission_AutoApproveMiss 名单未命中（execute）→ 走正常人工审批（waiting_input + IM 通知）。
func TestWirePermission_AutoApproveMiss(t *testing.T) {
	fa, _, _, store, taskID, pw, notifyTexts := setupPermWireAuto(t, time.Minute, []string{"edit"})
	defer pw.Unwire()

	fa.emitPerm(permReq("req-M", "Bash", "execute", standardOptions()))
	waitForStatus(t, store, taskID, model.TaskWaitingInput, time.Second)

	if got := fa.approveCount(); got != 0 {
		t.Errorf("approve calls=%d, want 0 (not auto-approved)", got)
	}
	if len(*notifyTexts) == 0 {
		t.Error("IM notify not called (should go through manual review)")
	}
}

// TestWirePermission_AutoApproveNoAllowOption 名单命中但只有 reject 选项 → 无法自动放行，
// 回退人工审批（卡上可看到 reject 选项）。
func TestWirePermission_AutoApproveNoAllowOption(t *testing.T) {
	fa, _, _, store, taskID, pw, _ := setupPermWireAuto(t, time.Minute, []string{"edit"})
	defer pw.Unwire()

	opts := []agent.PermissionOption{
		{ID: "r1", Name: "Reject Once", Kind: agent.PermissionOptionRejectOnce},
	}
	fa.emitPerm(permReq("req-NA", "Edit", "edit", opts))
	waitForStatus(t, store, taskID, model.TaskWaitingInput, time.Second)

	if got := fa.approveCount(); got != 0 {
		t.Errorf("approve calls=%d, want 0 (no allow option)", got)
	}
	tt, _ := store.Get(taskID)
	if tt.CurrentDecision == nil || tt.CurrentDecision.ID != "req-NA" {
		t.Errorf("CurrentDecision=%+v, want req-NA", tt.CurrentDecision)
	}
}

// TestWirePermission_AutoApproveEmptyList 免审名单为空 → 编辑类也走人工审批（向后兼容）。
func TestWirePermission_AutoApproveEmptyList(t *testing.T) {
	fa, _, _, store, taskID, pw, _ := setupPermWire(t, time.Minute) // autoApprove=nil
	defer pw.Unwire()

	fa.emitPerm(permReq("req-E", "Edit", "edit", standardOptions()))
	waitForStatus(t, store, taskID, model.TaskWaitingInput, time.Second)

	if got := fa.approveCount(); got != 0 {
		t.Errorf("approve calls=%d, want 0 (empty allowlist)", got)
	}
}

// TestWirePermission_AutoApproveKeepsQueueClean 自动放行的请求不进 pending/queue：
// 其后的非免审请求仍正常成为当前决策展示，拒绝后任务正常回 running，无残留。
func TestWirePermission_AutoApproveKeepsQueueClean(t *testing.T) {
	fa, _, _, store, taskID, pw, _ := setupPermWireAuto(t, time.Minute, []string{"edit"})
	defer pw.Unwire()

	// 先自动放行一个 edit 请求（early return，不应污染 displayed/queue）。
	fa.emitPerm(permReq("req-A1", "Edit", "edit", standardOptions()))
	if got := fa.approveCount(); got != 1 {
		t.Fatalf("approve calls=%d, want 1", got)
	}

	// 再发一个 execute 请求：应正常成为当前决策。
	fa.emitPerm(permReq("req-A2", "Bash", "execute", standardOptions()))
	waitForStatus(t, store, taskID, model.TaskWaitingInput, time.Second)
	tt, _ := store.Get(taskID)
	if tt.CurrentDecision == nil || tt.CurrentDecision.ID != "req-A2" {
		t.Fatalf("CurrentDecision=%+v, want req-A2", tt.CurrentDecision)
	}

	// 拒绝 req-A2 后 task 回 running，无残留决策。
	if err := pw.Resolve("req-A2", "deny"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	waitForStatus(t, store, taskID, model.TaskRunning, time.Second)
	tt, _ = store.Get(taskID)
	if tt.CurrentDecision != nil {
		t.Errorf("CurrentDecision not cleared: %+v", tt.CurrentDecision)
	}
}

// TestWirePermission_BuildPermSummary 验证摘要构造的各分支。
func TestWirePermission_BuildPermSummary(t *testing.T) {
	cases := []struct {
		name string
		req  agent.PermissionRequest
		want string
	}{
		{"title 原样全文", permReq("r", "Bash", "execute", nil), "Bash"},
		{"长命令不截断", agent.PermissionRequest{ReqID: "r", ToolTitle: strings.Repeat("a", 500)}, strings.Repeat("a", 500)},
		{"title only", agent.PermissionRequest{ReqID: "r", ToolTitle: "Write"}, "Write"},
		{"kind only → 人读标签", agent.PermissionRequest{ReqID: "r", ToolKind: "execute"}, "执行命令"},
		// 不认识的字段（没有 command/description/...）才退到原始 JSON。
		{"raw input fallback", agent.PermissionRequest{ReqID: "r", RawInput: json.RawMessage(`{"cmd":"ls"}`)}, `{"cmd":"ls"}`},
		{"raw input truncated", agent.PermissionRequest{ReqID: "r", RawInput: json.RawMessage(strings.Repeat("a", 300))}, strings.Repeat("a", 200) + "…"},
		{"fallback id", agent.PermissionRequest{ReqID: "r", ToolCallID: "call-9"}, "call-9"},

		// ---- dsh 的现实：title 与 kind 都是空的，只有 RawInput ----
		// 此前会把这串 JSON 原样倒到卡片上（用户看到的是一堆转义符和字段名）。
		{
			"dsh：command + description → 说明前缀 + 命令",
			agent.PermissionRequest{ReqID: "r", RawInput: json.RawMessage(
				`{"command":"Get-ChildItem $p","description":"Check if task file exists"}`)},
			"Check if task file exists：Get-ChildItem $p",
		},
		{
			"dsh：只有 command（不发 description）",
			agent.PermissionRequest{ReqID: "r", RawInput: json.RawMessage(`{"command":"ls -la /tmp"}`)},
			"ls -la /tmp",
		},
		{
			"dsh：description 与 command 相同不重复拼",
			agent.PermissionRequest{ReqID: "r", RawInput: json.RawMessage(`{"command":"ls","description":"ls"}`)},
			"ls",
		},
		{
			"dsh：编辑类工具按路径",
			agent.PermissionRequest{ReqID: "r", RawInput: json.RawMessage(`{"file_path":"internal/core/a.go"}`)},
			"internal/core/a.go",
		},
		{
			"dsh：搜索类工具按 pattern",
			agent.PermissionRequest{ReqID: "r", RawInput: json.RawMessage(`{"pattern":"func main"}`)},
			"func main",
		},
		{
			"dsh：description 优先于 file_path",
			agent.PermissionRequest{ReqID: "r", RawInput: json.RawMessage(
				`{"description":"读取配置","file_path":"config.yaml"}`)},
			"读取配置",
		},
		{
			"dsh：字段值不是字符串时忽略（不打印 [object]）",
			agent.PermissionRequest{ReqID: "r", RawInput: json.RawMessage(`{"command":[1,2],"path":"a.go"}`)},
			"a.go",
		},
		{
			"dsh：空白字符串视作没有",
			agent.PermissionRequest{ReqID: "r", RawInput: json.RawMessage(`{"command":"   ","path":"b.go"}`)},
			"b.go",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildPermSummary(c.req)
			if got != c.want {
				t.Errorf("buildPermSummary=%q, want %q", got, c.want)
			}
		})
	}
}

// TestBuildPermSummary_DshRealPayload 用 dsh 实际发出的 rawInput 做回归。
//
// 取自会话 498eb8de 的 pwsh 工具调用：dsh 的 ACP 实现**不发 title、不发 kind**，
// 于是 buildPermSummary 一路走到最后的兜底分支。此前卡片上显示的就是这段 JSON
// （转义符 + 字段名 + 英文说明混在一起，用户根本看不出"要执行什么"）。
func TestBuildPermSummary_DshRealPayload(t *testing.T) {
	// 注意：这里刻意保留 PowerShell 的转义原文，模拟真实 payload。
	raw := `{"command":"$p=\"$env:USERPROFILE\\.pieqi\\tasks\"; Test-Path $p; Get-ChildItem $p -Filter \"*6164e2a2*\" | Select-Object FullName","description":"Check if task file exists","justification":"沙箱两次拒绝授权工作区 ACL","sandbox_permissions":"danger-full-access"}`

	got := buildPermSummary(agent.PermissionRequest{ReqID: "r", RawInput: json.RawMessage(raw)})

	// 必须露出"要执行什么"，而不是 JSON 结构。
	if !strings.Contains(got, "Check if task file exists") {
		t.Errorf("摘要丢了 description：%q", got)
	}
	if !strings.Contains(got, "Test-Path $p") {
		t.Errorf("摘要丢了 command：%q", got)
	}
	// 不该出现 JSON 的字段名与结构噪声。
	for _, bad := range []string{`"command"`, `"sandbox_permissions"`, `\"`, "{\""} {
		if strings.Contains(got, bad) {
			t.Errorf("摘要里仍有 JSON 噪声 %q：%q", bad, got)
		}
	}
	// justification 是给审计看的理由，不该顶替正文（它没有 command 具体）。
	if strings.HasPrefix(got, "沙箱两次拒绝") {
		t.Errorf("摘要不该以 justification 开头：%q", got)
	}
}

// TestPermLabel 验证审批卡标题：短 title 原样用，长/多行 title 退化成 kind 人读标签。
// 后者是 qodercli 的现实（title = 整条命令，实测最长 1109 字符），塞进标题栏会让卡片
// 标题与摘要重复渲染同一串文本。
func TestPermLabel(t *testing.T) {
	cases := []struct {
		name        string
		title, kind string
		want        string
	}{
		{"短 title 原样", "Bash", "execute", "Bash"},
		{"带路径的短 title", "Edit src/a.go", "edit", "Edit src/a.go"},
		{"空 title 用 kind 标签", "", "execute", "执行命令"},
		{"未知 kind 兜底", "", "brand_new_kind", "工具调用"},
		{"整条长命令退化成类别", strings.Repeat("git ", 20), "execute", "执行命令"},
		{"多行退化成类别（首行本身也长）", "python -c \"\n" + strings.Repeat("a", 100) + "\nprint(1)\"", "execute", "执行命令"},
		{"多行且首行短也退化（首行没有信息量）", "git status\n第二行无关", "execute", "执行命令"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := permLabel(c.title, c.kind); got != c.want {
				t.Errorf("permLabel=%q, want %q", got, c.want)
			}
		})
	}
}

// TestWirePermission_ApproveSessionPassesSameKindLater 用户点"同类免审"后：
// 本次照常放行（回给 agent 的是 allow_once），之后同 kind 不再弹卡；不同 kind 仍弹。
func TestWirePermission_ApproveSessionPassesSameKindLater(t *testing.T) {
	fa, _, _, store, taskID, pw, _ := setupPermWire(t, time.Minute)
	defer pw.Unwire()

	fa.emitPerm(permReq("req-1", "rm -rf build", "execute", standardOptions()))
	waitForStatus(t, store, taskID, model.TaskWaitingInput, time.Second)

	if err := pw.Resolve("req-1", "approve_session"); err != nil {
		t.Fatalf("resolve approve_session: %v", err)
	}
	waitForStatus(t, store, taskID, model.TaskRunning, time.Second)
	// 放行的必须是 allow_once —— 把 allow_always 透给 agent 会让它自己记住，
	// 之后连 RequestPermission 都不发，pieqi 的 L2 硬边界就形同虚设。
	if _, optionID, ok := fa.lastApprove(); !ok || optionID != "o1" {
		t.Fatalf("lastApprove option=%q ok=%v, want o1(allow_once)", optionID, ok)
	}
	if fa.approveCount() != 1 {
		t.Fatalf("approveCount=%d, want 1", fa.approveCount())
	}

	// 同 kind 第二次：不再弹卡，直接自动放行（emitPerm 同步走完回调，无需等待）。
	fa.emitPerm(permReq("req-2", "ls", "execute", standardOptions()))
	if fa.approveCount() != 2 {
		t.Fatalf("approveCount=%d, want 2（同类应免审）", fa.approveCount())
	}
	if tt, _ := store.Get(taskID); tt.Status != model.TaskRunning {
		t.Fatalf("status=%q, want running（同类不该再弹卡）", tt.Status)
	}

	// 不同 kind（delete = L3）仍要人批。
	fa.emitPerm(permReq("req-3", "drop table", "delete", standardOptions()))
	waitForStatus(t, store, taskID, model.TaskWaitingInput, time.Second)
	if tt, _ := store.Get(taskID); tt.CurrentDecision == nil || tt.CurrentDecision.ID != "req-3" {
		t.Fatalf("decision=%+v, want req-3 展示", tt.CurrentDecision)
	}
}

// TestWirePermission_ApproveSessionEmptyKindNotRemembered 空 kind 不记账：
// 记下它等于给以后所有"叫不出名字的操作"发通行证，白名单会悄悄变成通配符。
func TestWirePermission_ApproveSessionEmptyKindNotRemembered(t *testing.T) {
	fa, _, _, store, taskID, pw, _ := setupPermWire(t, time.Minute)
	defer pw.Unwire()

	fa.emitPerm(permReq("req-E1", "mystery tool", "", standardOptions()))
	waitForStatus(t, store, taskID, model.TaskWaitingInput, time.Second)
	if err := pw.Resolve("req-E1", "approve_session"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	waitForStatus(t, store, taskID, model.TaskRunning, time.Second)

	fa.emitPerm(permReq("req-E2", "another mystery", "", standardOptions()))
	waitForStatus(t, store, taskID, model.TaskWaitingInput, time.Second)
	if fa.approveCount() != 1 {
		t.Fatalf("approveCount=%d, want 1（空 kind 不该被记住）", fa.approveCount())
	}
}

// TestWirePermission_ApproveSessionNotSharedAcrossWires 免审记账是**会话级**的：
// 新任务（新 wire）从零开始，不会被上一个任务的"同类免审"带过去。
func TestWirePermission_ApproveSessionNotSharedAcrossWires(t *testing.T) {
	fa1, _, _, store1, id1, pw1, _ := setupPermWire(t, time.Minute)
	defer pw1.Unwire()
	fa1.emitPerm(permReq("r1", "ls", "execute", standardOptions()))
	waitForStatus(t, store1, id1, model.TaskWaitingInput, time.Second)
	if err := pw1.Resolve("r1", "approve_session"); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	fa2, _, _, store2, id2, pw2, _ := setupPermWire(t, time.Minute)
	defer pw2.Unwire()
	fa2.emitPerm(permReq("r2", "ls", "execute", standardOptions()))
	waitForStatus(t, store2, id2, model.TaskWaitingInput, time.Second)
	if fa2.approveCount() != 0 {
		t.Fatalf("第二个任务的 approveCount=%d, want 0（记账不跨任务）", fa2.approveCount())
	}
}

// waitForDecision 轮询 store 直到 task 的 CurrentDecision.ID 变为 wantID 或超时。
func waitForDecision(t *testing.T, store *TaskStore, taskID, wantID string, wait time.Duration) {
	t.Helper()
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if tt, ok := store.Get(taskID); ok && tt.CurrentDecision != nil && tt.CurrentDecision.ID == wantID {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	tt, _ := store.Get(taskID)
	if tt.CurrentDecision != nil {
		t.Fatalf("task decision=%q, want %q (timed out)", tt.CurrentDecision.ID, wantID)
	}
	t.Fatalf("task decision=nil, want %q (timed out)", wantID)
}

// TestWirePermission_ConcurrentQueuePromotes 并发两个审批请求：只展示第一个（CurrentDecision=req-A），
// 第二个进队；Resolve A 后自动提升 B 继续展示（task 仍 waiting_input）；Resolve B 后 task 回 running。
// 验证"无不可见悬置"——每个请求最终都会被依次展示并可批。
func TestWirePermission_ConcurrentQueuePromotes(t *testing.T) {
	fa, _, _, store, taskID, pw, notifyTexts := setupPermWire(t, time.Minute)
	defer pw.Unwire()

	fa.emitPerm(permReq("req-A", "Bash", "execute", standardOptions()))
	fa.emitPerm(permReq("req-B", "Edit", "edit", standardOptions()))

	waitForDecision(t, store, taskID, "req-A", time.Second)
	tt, _ := store.Get(taskID)
	if tt.Status != model.TaskWaitingInput {
		t.Fatalf("status=%q, want waiting_input (req-A shown)", tt.Status)
	}
	if cd := tt.CurrentDecision; cd == nil || cd.ID != "req-A" || cd.ToolName != "Bash" {
		t.Fatalf("CurrentDecision=%+v, want req-A/Bash", cd)
	}

	// Resolve A → 提升 B 继续展示，task 不应回到 running（避免不可见窗口）。
	if err := pw.Resolve("req-A", "approve"); err != nil {
		t.Fatalf("Resolve A: %v", err)
	}
	waitForDecision(t, store, taskID, "req-B", time.Second)
	tt, _ = store.Get(taskID)
	if tt.Status != model.TaskWaitingInput {
		t.Fatalf("status after resolving A=%q, want waiting_input (req-B promoted)", tt.Status)
	}
	if cd := tt.CurrentDecision; cd == nil || cd.ID != "req-B" || cd.ToolName != "Edit" {
		t.Fatalf("CurrentDecision=%+v, want req-B/Edit", cd)
	}

	// Resolve B → 队列空 → task 回 running，CurrentDecision 清空。
	if err := pw.Resolve("req-B", "approve"); err != nil {
		t.Fatalf("Resolve B: %v", err)
	}
	waitForStatus(t, store, taskID, model.TaskRunning, time.Second)
	tt, _ = store.Get(taskID)
	if tt.CurrentDecision != nil {
		t.Fatalf("CurrentDecision not cleared: %+v", tt.CurrentDecision)
	}

	// adapter.Approve 依次收到 A、B（各一次）。
	if got := fa.approveCount(); got != 2 {
		t.Fatalf("approve calls=%d, want 2", got)
	}
	if rid, _, ok := fa.lastApprove(); !ok || rid != "req-B" {
		t.Fatalf("last approve reqID=%q, want req-B", rid)
	}

	// IM 通知应为每张卡一次（A 展示 + B 提升），即 2 条审批提醒。
	needDecisions := 0
	for _, txt := range *notifyTexts {
		if strings.Contains(txt, "⚠️") {
			needDecisions++
		}
	}
	if needDecisions != 2 {
		t.Errorf("IM approval notify count=%d, want 2", needDecisions)
	}
}

// TestWirePermission_QueueTimeoutPromotesNext 展示中的请求超时 → Deny 它并提升队首下一个继续展示；
// 下一个仍可正常 Resolve。验证排队请求不会因前一个超时而丢失。
func TestWirePermission_QueueTimeoutPromotesNext(t *testing.T) {
	fa, _, _, store, taskID, pw, _ := setupPermWire(t, 50*time.Millisecond)
	defer pw.Unwire()

	fa.emitPerm(permReq("req-A", "Bash", "execute", standardOptions()))
	fa.emitPerm(permReq("req-B", "Write", "edit", standardOptions()))
	waitForDecision(t, store, taskID, "req-A", time.Second)

	// 等 A 超时：A 被 Deny，B 被提升为当前卡（task 不回 running）。
	waitForDecision(t, store, taskID, "req-B", time.Second)
	if got := fa.denyCount(); got != 1 {
		t.Fatalf("deny calls=%d after A timeout, want 1", got)
	}
	tt, _ := store.Get(taskID)
	if tt.Status != model.TaskWaitingInput {
		t.Fatalf("status=%q, want waiting_input (req-B promoted after A timeout)", tt.Status)
	}

	// B 仍可正常批准 → 回 running。
	if err := pw.Resolve("req-B", "approve"); err != nil {
		t.Fatalf("Resolve B: %v", err)
	}
	waitForStatus(t, store, taskID, model.TaskRunning, time.Second)
	if got := fa.approveCount(); got != 1 {
		t.Fatalf("approve calls=%d, want 1 (B)", got)
	}
	if got := fa.denyCount(); got != 1 {
		t.Fatalf("deny calls=%d, want 1 (only A timed out)", got)
	}
}

// TestWirePermission_QueueDenyPromotesNext 拒绝展示中的请求 → 提升队首下一个；拒绝下一个后 task 回 running。
// 无 reject 选项时走 adapter.Deny（→ Cancelled），验证 deny 路径在排队语义下同样正确推进。
func TestWirePermission_QueueDenyPromotesNext(t *testing.T) {
	fa, _, _, store, taskID, pw, _ := setupPermWire(t, time.Minute)
	defer pw.Unwire()

	opts := []agent.PermissionOption{
		{ID: "o1", Name: "Allow Once", Kind: agent.PermissionOptionAllowOnce},
	} // 无 reject 选项 → deny 走 adapter.Deny
	fa.emitPerm(permReq("req-A", "Bash", "execute", opts))
	fa.emitPerm(permReq("req-B", "Edit", "edit", opts))
	waitForDecision(t, store, taskID, "req-A", time.Second)

	if err := pw.Resolve("req-A", "deny"); err != nil {
		t.Fatalf("Resolve A deny: %v", err)
	}
	waitForDecision(t, store, taskID, "req-B", time.Second)
	if got := fa.denyCount(); got != 1 {
		t.Fatalf("deny calls=%d after denying A, want 1", got)
	}
	if got := fa.approveCount(); got != 0 {
		t.Fatalf("approve calls=%d, want 0 (deny path)", got)
	}

	if err := pw.Resolve("req-B", "deny"); err != nil {
		t.Fatalf("Resolve B deny: %v", err)
	}
	waitForStatus(t, store, taskID, model.TaskRunning, time.Second)
	tt, _ := store.Get(taskID)
	if tt.CurrentDecision != nil {
		t.Fatalf("CurrentDecision not cleared: %+v", tt.CurrentDecision)
	}
	if got := fa.denyCount(); got != 2 {
		t.Fatalf("deny calls=%d, want 2 (A and B)", got)
	}
}

// TestWirePermission_PersistFailureStillShowsCard 落盘失败**不得**退化成静默 Deny。
//
// 回归对象：setWaitingApproval 原本写的是 `if err != nil || !applied { return nil, false }`，
// 把「落盘失败」和「任务不存在/已终态」混成同一件事。于是 show() 返回 false，
// onPermissionRequest 走它那条「task 不存在或已终态」的兜底分支：
// 用户看不到审批卡、工具被静默 Deny，而且此时**内存改动其实已经生效**，
// 留下一个永远没人能 Resolve 的 waiting_input。
//
// 这里的落盘失败是构造出来的：把落点占成一个同名目录，rename 到它上面必然失败。
func TestWirePermission_PersistFailureStillShowsCard(t *testing.T) {
	fa, _, sub, store, taskID, pw, _ := setupPermWire(t, time.Hour)
	defer pw.Unwire()

	// 让落盘必然失败：把落点上的文件换成同名目录，rename 到它上面必定失败。
	// （不能直接 MkdirAll —— 同名文件还在时 Windows 报 ERROR_PATH_NOT_FOUND。）
	path := filepath.Join(store.tasksDir, taskID+".json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}

	// 自检：确认「落盘失败」这个前提真的成立了，否则下面的断言全是空转；
	// 顺带钉住 Update 的返回契约 —— 落盘失败也必须连同已生效的快照一起返回，
	// 返回 nil 正是当初被误判成「任务不存在」的起因。
	snap, err := store.Update(taskID, func(t *model.Task) bool { t.Title = "probe"; return true })
	if err == nil {
		t.Fatal("注入失败：落盘本该失败却成功了")
	}
	if snap == nil {
		t.Fatal("落盘失败时 Update 仍须返回已生效的快照（返回 nil 会被误判成任务不存在）")
	}
	if snap.Title != "probe" {
		t.Fatalf("快照应包含已生效的改动，Title = %q", snap.Title)
	}

	fa.emitPerm(permReq("req-persist-fail", "Bash", "execute", standardOptions()))

	// 卡片必须照常展示：任务进 waiting_input 且带着这条决策。
	waitForStatus(t, store, taskID, model.TaskWaitingInput, 3*time.Second)
	got, _ := store.Get(taskID)
	if got.CurrentDecision == nil || got.CurrentDecision.ID != "req-persist-fail" {
		t.Fatalf("CurrentDecision = %+v, want id req-persist-fail", got.CurrentDecision)
	}

	// 前端要收到带卡片的 task_updated（否则 PWA 上根本没有审批入口）。
	tt := findTaskUpdated(t, drainEvents(sub, 200*time.Millisecond))
	if tt.CurrentDecision == nil || tt.CurrentDecision.ID != "req-persist-fail" {
		t.Fatalf("task_updated 未携带审批卡: %+v", tt.CurrentDecision)
	}

	// 核心断言：绝不能被静默拒绝。
	if n := fa.denyCount(); n != 0 {
		t.Errorf("adapter.Deny 被调用了 %d 次；落盘失败不得当成「不该展示」而静默拒绝", n)
	}

	// 而且这张卡是真能用的（用户点批准 → 正常放行），证明不是个死状态。
	if err := pw.Resolve("req-persist-fail", "approve"); err != nil {
		t.Fatalf("Resolve approve: %v", err)
	}
	if n := fa.approveCount(); n != 1 {
		t.Errorf("approve calls = %d, want 1", n)
	}
}
