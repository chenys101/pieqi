package core

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"pieqi/internal/agent"
	"pieqi/internal/model"
)

// fakeDeltaAdapter 测试用 AgentAdapter：只关心 OnContentDelta 回调的注册与手动触发，
// 其余方法返回零值/nil。模拟 ACP SessionUpdate 到达时触发已注册回调。
type fakeDeltaAdapter struct {
	onDelta agent.ContentDeltaFunc
	done    chan struct{}
}

func newFakeDeltaAdapter() *fakeDeltaAdapter {
	return &fakeDeltaAdapter{done: make(chan struct{})}
}

func (f *fakeDeltaAdapter) NewSession(context.Context, agent.SessionConfig) (string, error) {
	return "sess", nil
}
func (f *fakeDeltaAdapter) RealSessionID(sessionID string) string            { return sessionID }
func (f *fakeDeltaAdapter) SendPrompt(context.Context, string, string) error { return nil }
func (f *fakeDeltaAdapter) OnContentDelta(fn agent.ContentDeltaFunc)         { f.onDelta = fn }
func (f *fakeDeltaAdapter) OnPermissionRequest(agent.PermissionRequestFunc)  {}
func (f *fakeDeltaAdapter) OnToolCallUpdate(agent.ToolCallUpdateFunc)        {}
func (f *fakeDeltaAdapter) Approve(context.Context, string, string) error    { return nil }
func (f *fakeDeltaAdapter) Deny(context.Context, string) error               { return nil }
func (f *fakeDeltaAdapter) RespondPermission(context.Context, string, bool, string) error {
	return nil
}
func (f *fakeDeltaAdapter) InjectToolResult(context.Context, string, string, string, bool) error {
	return nil
}
func (f *fakeDeltaAdapter) Cancel(context.Context, string) error { return nil }
func (f *fakeDeltaAdapter) Close(context.Context) error          { return nil }
func (f *fakeDeltaAdapter) Done() <-chan struct{}                { return f.done }

// emitDelta 手动触发已注册的 OnContentDelta 回调（模拟 ACP SessionUpdate 到达）。
func (f *fakeDeltaAdapter) emitDelta(d agent.ContentDelta) {
	if f.onDelta != nil {
		f.onDelta(d)
	}
}

// setupWire 构造一套 wired 环境：fake adapter + bus + 订阅 + store + 一个 pending task。
func setupWire(t *testing.T) (*fakeDeltaAdapter, *EventBus, *Subscription, *TaskStore, string, *DeltaHandle) {
	t.Helper()
	bus := NewEventBus()
	sub := bus.Subscribe(64)
	store, err := NewTaskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tt, err := store.Create(&model.Task{ProjectID: "p", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	fa := newFakeDeltaAdapter()
	h := WireContentDelta(fa, bus, store, tt.ID)
	return fa, bus, sub, store, tt.ID, h
}

// drainEvents 读取订阅 channel 中的全部事件：每个事件最多等 wait，超时无新事件即返回。
func drainEvents(sub *Subscription, wait time.Duration) []Event {
	var out []Event
	for {
		select {
		case ev := <-sub.Chan():
			out = append(out, ev)
		case <-time.After(wait):
			return out
		}
	}
}

// assertNoTaskUpdated 断言事件列表中无 task_updated（验证安静持久化：只推 task_delta）。
func assertNoTaskUpdated(t *testing.T, evs []Event) {
	t.Helper()
	for _, ev := range evs {
		if ev.Type == "task_updated" || ev.Type == "task_completed" {
			t.Fatalf("unexpected full-task event %q (quiet persistence broken): %+v", ev.Type, ev)
		}
	}
}

// assertDeltas 断言事件列表均为 task_delta，且 Delta.Text/IsThought 按序匹配期望。
func assertDeltas(t *testing.T, evs []Event, wantText []string, wantThought []bool) {
	t.Helper()
	if len(evs) != len(wantText) {
		t.Fatalf("got %d events, want %d: %+v", len(evs), len(wantText), evs)
	}
	for i, ev := range evs {
		if ev.Type != EventTaskDelta {
			t.Fatalf("event %d type=%q, want %q", i, ev.Type, EventTaskDelta)
		}
		if ev.Task != nil {
			t.Fatalf("event %d carried full task, want nil (delta-only)", i)
		}
		if ev.Delta == nil {
			t.Fatalf("event %d delta nil", i)
		}
		if ev.Delta.Text != wantText[i] {
			t.Errorf("event %d delta.Text=%q, want %q", i, ev.Delta.Text, wantText[i])
		}
		if ev.Delta.IsThought != wantThought[i] {
			t.Errorf("event %d delta.IsThought=%v, want %v", i, ev.Delta.IsThought, wantThought[i])
		}
	}
}

// setupWireFast 与 setupWire 相同，但把落盘合并窗口设为 0（每个增量立刻落盘）。
//
// 为什么这些测试要用 0 窗口：它们验的是**持久化语义**（合并边界、Output 累积），
// 不是合并窗口本身。默认 200ms 窗口会让 t.Fatalf 读到"还没落盘"的中间态，
// 从而把正确实现误判为失败。窗口行为由 TestWireContentDelta_*Flush* 那几条单独覆盖。
func setupWireFast(t *testing.T) (*fakeDeltaAdapter, *EventBus, *Subscription, *TaskStore, string, *DeltaHandle) {
	t.Helper()
	fa, bus, sub, store, taskID, h := setupWire(t)
	h.wire.flushWindow = 0
	return fa, bus, sub, store, taskID, h
}

// flushAndGet 显式落盘后读取任务快照（避免依赖窗口计时，消除测试抖动）。
func flushAndGet(t *testing.T, store *TaskStore, h *DeltaHandle, taskID string) *model.Task {
	t.Helper()
	h.Flush()
	tt, ok := store.Get(taskID)
	if !ok {
		t.Fatal("task missing")
	}
	return tt
}

// TestWireContentDelta_TextDelta text 增量累积到同一 event + output，只推 task_delta。
func TestWireContentDelta_TextDelta(t *testing.T) {
	fa, _, sub, store, taskID, h := setupWireFast(t)

	fa.emitDelta(agent.ContentDelta{Text: "Hel", IsThought: false})
	fa.emitDelta(agent.ContentDelta{Text: "lo", IsThought: false})

	evs := drainEvents(sub, 60*time.Millisecond)
	assertDeltas(t, evs, []string{"Hel", "lo"}, []bool{false, false})
	assertNoTaskUpdated(t, evs)

	tt := flushAndGet(t, store, h, taskID)
	if len(tt.Events) != 1 {
		t.Fatalf("events=%+v, want 1 text event", tt.Events)
	}
	if tt.Events[0].Type != model.EventText || tt.Events[0].Text != "Hello" {
		t.Fatalf("event[0]=%+v, want text 'Hello'", tt.Events[0])
	}
	if tt.Output != "Hello" {
		t.Fatalf("output=%q, want 'Hello'", tt.Output)
	}
}

// TestWireContentDelta_ThoughtDelta thought 增量累积到 thinking event，不计入 output。
func TestWireContentDelta_ThoughtDelta(t *testing.T) {
	fa, _, sub, store, taskID, h := setupWireFast(t)

	fa.emitDelta(agent.ContentDelta{Text: "思", IsThought: true})
	fa.emitDelta(agent.ContentDelta{Text: "考", IsThought: true})

	evs := drainEvents(sub, 60*time.Millisecond)
	assertDeltas(t, evs, []string{"思", "考"}, []bool{true, true})
	assertNoTaskUpdated(t, evs)

	tt := flushAndGet(t, store, h, taskID)
	if len(tt.Events) != 1 {
		t.Fatalf("events=%+v, want 1 thinking event", tt.Events)
	}
	if tt.Events[0].Type != model.EventThinking || tt.Events[0].Text != "思考" {
		t.Fatalf("event[0]=%+v, want thinking '思考'", tt.Events[0])
	}
	if tt.Output != "" {
		t.Fatalf("output=%q, want empty (thought must not append to output)", tt.Output)
	}
}

// TestWireContentDelta_TypeSwitch 连续类型切换：text→thinking→text 应新建 3 个 event，
// output 只累积 text 增量。
func TestWireContentDelta_TypeSwitch(t *testing.T) {
	fa, _, sub, store, taskID, h := setupWireFast(t)

	fa.emitDelta(agent.ContentDelta{Text: "a", IsThought: false}) // text event 1
	fa.emitDelta(agent.ContentDelta{Text: "b", IsThought: true})  // 切换 → 新 thinking event
	fa.emitDelta(agent.ContentDelta{Text: "c", IsThought: false}) // 切换 → 新 text event

	evs := drainEvents(sub, 60*time.Millisecond)
	assertDeltas(t, evs, []string{"a", "b", "c"}, []bool{false, true, false})
	assertNoTaskUpdated(t, evs)

	tt := flushAndGet(t, store, h, taskID)
	if len(tt.Events) != 3 {
		t.Fatalf("events len=%d, want 3: %+v", len(tt.Events), tt.Events)
	}
	want := []struct {
		typ  model.TaskEventType
		text string
	}{
		{model.EventText, "a"},
		{model.EventThinking, "b"},
		{model.EventText, "c"},
	}
	for i, w := range want {
		if tt.Events[i].Type != w.typ || tt.Events[i].Text != w.text {
			t.Errorf("event[%d]=%+v, want %v %q", i, tt.Events[i], w.typ, w.text)
		}
	}
	if tt.Output != "ac" {
		t.Fatalf("output=%q, want 'ac' (only text deltas)", tt.Output)
	}
}

// TestWireContentDelta_ContinuousMerge 连续同类型增量合并到同一 event（不新建）。
func TestWireContentDelta_ContinuousMerge(t *testing.T) {
	fa, _, sub, store, taskID, h := setupWireFast(t)

	for _, s := range []string{"1", "2", "3", "4"} {
		fa.emitDelta(agent.ContentDelta{Text: s, IsThought: false})
	}

	evs := drainEvents(sub, 60*time.Millisecond)
	assertDeltas(t, evs, []string{"1", "2", "3", "4"}, []bool{false, false, false, false})

	tt := flushAndGet(t, store, h, taskID)
	if len(tt.Events) != 1 || tt.Events[0].Text != "1234" {
		t.Fatalf("events=%+v, want single text '1234'", tt.Events)
	}
}

// TestWireContentDelta_EmptyTextIgnored 空文本增量不持久化也不推送。
func TestWireContentDelta_EmptyTextIgnored(t *testing.T) {
	fa, _, sub, store, taskID, _ := setupWire(t)

	fa.emitDelta(agent.ContentDelta{Text: "", IsThought: false})

	evs := drainEvents(sub, 60*time.Millisecond)
	if len(evs) != 0 {
		t.Fatalf("expected no events for empty delta, got %+v", evs)
	}
	tt, _ := store.Get(taskID)
	if len(tt.Events) != 0 || tt.Output != "" {
		t.Fatalf("store mutated by empty delta: %+v", tt)
	}
}

// TestWireContentDelta_Unwire 拆卸后回调不再触发：无新事件、store 不变。
func TestWireContentDelta_Unwire(t *testing.T) {
	fa, _, sub, store, taskID, h := setupWireFast(t)

	fa.emitDelta(agent.ContentDelta{Text: "before"})
	_ = drainEvents(sub, 60*time.Millisecond)

	h.Unwire()
	fa.emitDelta(agent.ContentDelta{Text: "after"})

	evs := drainEvents(sub, 60*time.Millisecond)
	for _, ev := range evs {
		if ev.Type == EventTaskDelta && ev.Delta != nil && ev.Delta.Text == "after" {
			t.Fatal("delta published after Unwire")
		}
	}
	tt, _ := store.Get(taskID)
	if len(tt.Events) != 1 || tt.Events[0].Text != "before" {
		t.Fatalf("store changed after Unwire: %+v", tt.Events)
	}
}

// TestWireContentDelta_UnwireFlushesPending Unwire 必须先落盘再拆回调。
//
// 这是合并窗口引入的**新失效模式**：窗口内的文本还在内存里，若 Unwire 只拆回调
// 不落盘，任务结束时最后 200ms 的正文会**永久丢失**（内存没了、磁盘也没写）。
// 用户看到的是"回答结尾被截断"，而日志里一切正常 —— 典型的静默数据丢失。
func TestWireContentDelta_UnwireFlushesPending(t *testing.T) {
	fa, _, _, store, taskID, h := setupWire(t) // 默认 200ms 窗口：不 Unwire 就不会落盘

	fa.emitDelta(agent.ContentDelta{Text: "最后一段", IsThought: false})

	// 立刻断言：窗口未到，此刻**不该**已落盘（否则这条测试没在测 Unwire）
	if tt, _ := store.Get(taskID); len(tt.Events) != 0 {
		t.Fatalf("窗口未到就不该落盘，实得 %+v", tt.Events)
	}

	h.Unwire()

	tt, _ := store.Get(taskID)
	if len(tt.Events) != 1 || tt.Events[0].Text != "最后一段" {
		t.Fatalf("Unwire 未落盘残留缓冲，events=%+v（正文丢失）", tt.Events)
	}
	if tt.Output != "最后一段" {
		t.Fatalf("output=%q, want '最后一段'", tt.Output)
	}
}

// TestWireContentDelta_WindowFlushes 窗口到期自动落盘（无需显式 Flush）。
func TestWireContentDelta_WindowFlushes(t *testing.T) {
	fa, _, _, store, taskID, _ := setupWire(t) // 默认 200ms 窗口

	fa.emitDelta(agent.ContentDelta{Text: "auto", IsThought: false})

	// 轮询等窗口生效：给足余量（200ms 窗口 + 落盘耗时），避免把慢机器判成失败。
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if tt, _ := store.Get(taskID); len(tt.Events) == 1 && tt.Events[0].Text == "auto" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	tt, _ := store.Get(taskID)
	t.Fatalf("窗口到期后未自动落盘，events=%+v", tt.Events)
}

// TestWireContentDelta_FlushCoalescesWrites 合并窗口必须真的**减少写盘次数**。
//
// 这是修复 3 的全部意义所在：原先每个增量一次 store.Update（全局写锁内 marshal
// 整个 task + 写文件 + rename，代价 O(事件总数)），任务越长越慢，进而拖住
// WS 快照与连接。这条测试用"落盘次数"这个可观测量把收益钉住 ——
// 只断言内容正确是不够的，那样即使合并失效（退化成逐个落盘）也照样通过。
func TestWireContentDelta_FlushCoalescesWrites(t *testing.T) {
	fa, _, _, _, _, h := setupWire(t) // 默认 200ms 窗口

	const n = 50
	for i := 0; i < n; i++ {
		fa.emitDelta(agent.ContentDelta{Text: "x", IsThought: false})
	}
	// 全部增量都在同一窗口内 → 只应有一次落盘
	h.Flush()

	if got := h.wire.flushCount(); got != 1 {
		t.Fatalf("落盘次数 = %d, want 1（%d 个增量应合并为一次写盘）", got, n)
	}

	tt, _ := h.wire.store.Get(h.wire.taskID)
	if len(tt.Events) != 1 || tt.Events[0].Text != strings.Repeat("x", n) {
		t.Fatalf("合并后内容不对: events=%+v, output=%q", tt.Events, tt.Output)
	}
}

// TestWireContentDelta_FlushIsIdempotent 重复 Flush 不应产生重复内容。
func TestWireContentDelta_FlushIsIdempotent(t *testing.T) {
	fa, _, _, store, taskID, h := setupWire(t)

	fa.emitDelta(agent.ContentDelta{Text: "abc", IsThought: false})
	h.Flush()
	h.Flush()
	h.Flush()

	tt, _ := store.Get(taskID)
	if len(tt.Events) != 1 || tt.Events[0].Text != "abc" {
		t.Fatalf("重复 Flush 产生了重复内容: events=%+v", tt.Events)
	}
	if tt.Output != "abc" {
		t.Fatalf("output=%q, want 'abc'", tt.Output)
	}
}

// TestWireContentDelta_ConcurrentFlushNoLoss 并发写增量与 Flush 时，**内容既不丢也不重**。
//
// 为什么要专门测这个：合并窗口把"写"拆成了"入缓冲"（agent goroutine）与"落盘"
// （窗口定时器/轮末 Unwire，可能在别的 goroutine），这是我引入的真实并发点。
// 本机无法开 -race（需 cgo），所以用"总量守恒"这条与实现无关的性质来守：
// 只要最终 Output 与事件文本都恰好等于写入的总量，就没有丢写或重复写。
func TestWireContentDelta_ConcurrentFlushNoLoss(t *testing.T) {
	fa, _, _, store, taskID, h := setupWireFast(t)

	const writers = 4
	const perWriter = 50
	var wg sync.WaitGroup
	// 每写一个增量就穿插一次 Flush，制造"边写边刷"的交错
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				fa.emitDelta(agent.ContentDelta{Text: "x"})
				h.Flush()
			}
		}()
	}
	wg.Wait()
	h.Flush()

	tt, _ := store.Get(taskID)
	want := writers * perWriter
	if got := utf8.RuneCountInString(tt.Output); got != want {
		t.Fatalf("output 字符数 = %d, want %d（丢写或重复写）", got, want)
	}
	var evText int
	for _, ev := range tt.Events {
		if ev.Type == model.EventText {
			evText += utf8.RuneCountInString(ev.Text)
		}
	}
	if evText != want {
		t.Fatalf("事件文本总字符数 = %d, want %d（丢写或重复写）", evText, want)
	}
}

// TestWireContentDelta_UnwireIdempotent Unwire 多次调用不 panic。
func TestWireContentDelta_UnwireIdempotent(t *testing.T) {
	_, _, _, _, _, h := setupWire(t)
	h.Unwire()
	h.Unwire() // 幂等，不应 panic
}

// TestWireContentDelta_NoTaskUpdated 综合断言：多轮 text/thought 增量全程不发 task_updated。
func TestWireContentDelta_NoTaskUpdated(t *testing.T) {
	fa, _, sub, _, _, _ := setupWire(t)

	fa.emitDelta(agent.ContentDelta{Text: "x", IsThought: false})
	fa.emitDelta(agent.ContentDelta{Text: "y", IsThought: true})
	fa.emitDelta(agent.ContentDelta{Text: "z", IsThought: false})

	evs := drainEvents(sub, 80*time.Millisecond)
	if len(evs) != 3 {
		t.Fatalf("got %d events, want 3: %+v", len(evs), evs)
	}
	assertNoTaskUpdated(t, evs)
	// 每个 task_delta 必须只带 Delta、不带完整 Task
	for i, ev := range evs {
		if ev.Task != nil {
			t.Fatalf("event %d carried full task: %+v", i, ev)
		}
		if ev.Delta == nil {
			t.Fatalf("event %d missing delta", i)
		}
	}
}
