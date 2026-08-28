package core

import (
	"context"
	"encoding/json"
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
// task 带 IM 来源渠道，便于验证 notify 回调。
func setupPermWire(t *testing.T, timeout time.Duration) (*fakePermAdapter, *EventBus, *Subscription, *TaskStore, string, *PermissionWire, *[]string) {
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
	pw := WirePermission(fa, bus, store, tt.ID, notify, timeout)
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
	if cd.ToolName != "Bash" {
		t.Errorf("decision.ToolName=%q, want Bash", cd.ToolName)
	}
	if !strings.Contains(cd.Summary, "Bash") || !strings.Contains(cd.Summary, "execute") {
		t.Errorf("decision.Summary=%q, want contain Bash+execute", cd.Summary)
	}
	wantOpts := []string{"approve", "deny"}
	if len(cd.Options) != 2 || cd.Options[0] != wantOpts[0] || cd.Options[1] != wantOpts[1] {
		t.Errorf("decision.Options=%v, want %v", cd.Options, wantOpts)
	}

	// IM notify 应被调用一次，文案含「需要决策」与摘要。
	if len(*notifyTexts) == 0 {
		t.Fatal("IM notify not called")
	}
	if !strings.Contains((*notifyTexts)[0], "需要决策") {
		t.Errorf("notify text=%q, want contain '需要决策'", (*notifyTexts)[0])
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
	}, time.Minute)
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

// TestWirePermission_BuildPermSummary 验证摘要构造的各分支。
func TestWirePermission_BuildPermSummary(t *testing.T) {
	cases := []struct {
		name string
		req  agent.PermissionRequest
		want string
	}{
		{"title+kind", permReq("r", "Bash", "execute", nil), "Bash (execute)"},
		{"title only", agent.PermissionRequest{ReqID: "r", ToolTitle: "Write"}, "Write"},
		{"kind only", agent.PermissionRequest{ReqID: "r", ToolKind: "execute"}, "execute"},
		{"raw input fallback", agent.PermissionRequest{ReqID: "r", RawInput: json.RawMessage(`{"cmd":"ls"}`)}, `{"cmd":"ls"}`},
		{"raw input truncated", agent.PermissionRequest{ReqID: "r", RawInput: json.RawMessage(strings.Repeat("a", 300))}, strings.Repeat("a", 200) + "…"},
		{"fallback id", agent.PermissionRequest{ReqID: "r", ToolCallID: "call-9"}, "call-9"},
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

	// IM 通知应为每张卡一次（A 展示 + B 提升），即 2 条「需要决策」。
	needDecisions := 0
	for _, txt := range *notifyTexts {
		if strings.Contains(txt, "需要决策") {
			needDecisions++
		}
	}
	if needDecisions != 2 {
		t.Errorf("IM '需要决策' notify count=%d, want 2", needDecisions)
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
