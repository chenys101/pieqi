package core

import (
	"testing"
	"time"

	"pieqi/internal/agent"
	"pieqi/internal/model"
)

// fakeUsageAdapter 测试用 AgentAdapter，额外实现 UsageReporter（可选能力）。
type fakeUsageAdapter struct {
	*fakeDeltaAdapter
	onUsage agent.UsageUpdateFunc
}

func newFakeUsageAdapter() *fakeUsageAdapter {
	return &fakeUsageAdapter{fakeDeltaAdapter: newFakeDeltaAdapter()}
}

func (f *fakeUsageAdapter) OnUsageUpdate(fn agent.UsageUpdateFunc) { f.onUsage = fn }

// emitUsage 手动触发已注册的 OnUsageUpdate 回调（模拟 ACP usage_update 到达）。
func (f *fakeUsageAdapter) emitUsage(u agent.UsageInfo) {
	if f.onUsage != nil {
		f.onUsage(u)
	}
}

// setupUsageWire 构造用量 wire 的测试环境。
func setupUsageWire(t *testing.T) (*fakeUsageAdapter, *Subscription, *TaskStore, string, *UsageHandle) {
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
	fa := newFakeUsageAdapter()
	h := WireUsage(fa, bus, store, tt.ID)
	return fa, sub, store, tt.ID, h
}

// TestWireUsage_PersistsAndPublishesLightweight 验证用量上报的两件事：
// 覆盖式持久化到 task.Usage，且只推**轻量** task_usage（不带完整 Task）。
func TestWireUsage_PersistsAndPublishesLightweight(t *testing.T) {
	fa, sub, store, taskID, _ := setupUsageWire(t)

	fa.emitUsage(agent.UsageInfo{SessionID: "s", Used: 1234, Size: 200000})

	// 1. 持久化：task.Usage 有值。
	task, ok := store.Get(taskID)
	if !ok {
		t.Fatal("task not found")
	}
	if task.Usage == nil {
		t.Fatal("task.Usage is nil, want persisted snapshot")
	}
	if task.Usage.Used != 1234 || task.Usage.Size != 200000 {
		t.Errorf("task.Usage=%+v want Used=1234 Size=200000", task.Usage)
	}
	if task.Usage.At.IsZero() {
		t.Error("task.Usage.At is zero, want capture time")
	}

	// 2. 事件：只推 task_usage，且不带完整 Task（带上就会触发前端全量重绘，
	//    而 usage_update 一轮内会到多次 —— 那正是 agent_stream 记着的坑）。
	evs := drainEvents(sub, 100*time.Millisecond)
	if len(evs) != 1 {
		t.Fatalf("got %d events, want 1: %+v", len(evs), evs)
	}
	ev := evs[0]
	if ev.Type != EventTaskUsage {
		t.Fatalf("event type=%q, want %q", ev.Type, EventTaskUsage)
	}
	if ev.Task != nil {
		t.Fatalf("event carried full task, want nil (usage-only): %+v", ev)
	}
	if ev.Usage == nil {
		t.Fatal("event usage payload nil")
	}
	if ev.Usage.Used != 1234 || ev.Usage.Size != 200000 {
		t.Errorf("payload=%+v want Used=1234 Size=200000", ev.Usage)
	}
	if ev.TaskID != taskID {
		t.Errorf("task_id=%q want %q", ev.TaskID, taskID)
	}
	assertNoTaskUpdated(t, evs)
}

// TestWireUsage_OverwritesNotAccumulates 验证连续上报是**覆盖**而非累加。
//
// 判据：usage_update 表达的是"此刻上下文多满"这一瞬时量，不是流量计。
// 累加会让进度条一路涨到 400%，而用户真正想知道的是"还剩多少余量"。
func TestWireUsage_OverwritesNotAccumulates(t *testing.T) {
	fa, _, store, taskID, _ := setupUsageWire(t)

	fa.emitUsage(agent.UsageInfo{Used: 100, Size: 1000})
	fa.emitUsage(agent.UsageInfo{Used: 300, Size: 1000})
	fa.emitUsage(agent.UsageInfo{Used: 250, Size: 1000})

	task, _ := store.Get(taskID)
	if task.Usage == nil {
		t.Fatal("task.Usage nil")
	}
	if task.Usage.Used != 250 {
		t.Errorf("Used=%d want 250 (latest snapshot, not a sum)", task.Usage.Used)
	}
}

// TestWireUsage_ZeroSizeIgnored 验证 Size<=0 的上报既不持久化也不推事件。
func TestWireUsage_ZeroSizeIgnored(t *testing.T) {
	fa, sub, store, taskID, _ := setupUsageWire(t)

	fa.emitUsage(agent.UsageInfo{Used: 999, Size: 0})

	task, _ := store.Get(taskID)
	if task.Usage != nil {
		t.Errorf("task.Usage=%+v want nil for Size=0", task.Usage)
	}
	if evs := drainEvents(sub, 80*time.Millisecond); len(evs) != 0 {
		t.Errorf("got %d events, want 0 for Size=0: %+v", len(evs), evs)
	}
}

// TestWireUsage_CostFlagsPropagate 验证成本标记透传到事件载荷。
// HasCost=false 必须一路传到前端 —— 否则 UI 分不清"没报成本"与"成本为 0"。
func TestWireUsage_CostFlagsPropagate(t *testing.T) {
	fa, sub, _, _, _ := setupUsageWire(t)

	fa.emitUsage(agent.UsageInfo{Used: 1, Size: 100, CostUSD: 0.5, HasCost: true})
	evs := drainEvents(sub, 100*time.Millisecond)
	if len(evs) != 1 || evs[0].Usage == nil {
		t.Fatalf("events=%+v want 1 with usage payload", evs)
	}
	if !evs[0].Usage.HasCost || evs[0].Usage.CostUSD != 0.5 {
		t.Errorf("payload=%+v want HasCost=true CostUSD=0.5", evs[0].Usage)
	}
}

// TestWireUsage_UnsupportedAdapterReturnsNil 验证不上报用量的 adapter
// （未实现 UsageReporter）得到 nil 句柄，且 Unwire 对 nil 安全。
//
// 判据：claude 桥 / print 路径就是这一类。这里若返回非 nil 或 panic，
// 就等于给"不报用量"这个正常情形引入了故障。
func TestWireUsage_UnsupportedAdapterReturnsNil(t *testing.T) {
	bus := NewEventBus()
	store, err := NewTaskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tt, err := store.Create(&model.Task{ProjectID: "p", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	// fakeDeltaAdapter 不实现 UsageReporter。
	h := WireUsage(newFakeDeltaAdapter(), bus, store, tt.ID)
	if h != nil {
		t.Fatalf("handle=%+v want nil for adapter without UsageReporter", h)
	}
	h.Unwire() // 必须 nil-safe（TaskRunner.onAgentSessionClosed 会无条件调用）
}

// TestUsageHandle_UnwireStopsDelivery 验证 Unwire 后迟到的上报不再写入。
// 这是"任务已收尾、别再改它"的那道闸。
func TestUsageHandle_UnwireStopsDelivery(t *testing.T) {
	fa, _, store, taskID, h := setupUsageWire(t)

	fa.emitUsage(agent.UsageInfo{Used: 1, Size: 100})
	h.Unwire()
	fa.emitUsage(agent.UsageInfo{Used: 2, Size: 100})

	task, _ := store.Get(taskID)
	if task.Usage == nil || task.Usage.Used != 1 {
		t.Fatalf("task.Usage=%+v want Used=1 (post-Unwire update must be dropped)", task.Usage)
	}
	h.Unwire() // 幂等
}

// TestWireUsage_NilTaskSafety 验证对不存在的 task 上报不 panic。
// 任务被删除后 agent 仍可能推一条收尾用量，这条路径必须静默。
func TestWireUsage_NilTaskSafety(t *testing.T) {
	bus := NewEventBus()
	store, err := NewTaskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fa := newFakeUsageAdapter()
	// 不创建 task：store 里没有这个 id。
	h := WireUsage(fa, bus, store, "no-such-task")
	fa.emitUsage(agent.UsageInfo{Used: 1, Size: 100}) // 不得 panic
	h.Unwire()
}
