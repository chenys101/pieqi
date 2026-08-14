package core

import (
	"encoding/json"
	"testing"
	"time"

	"pieqi/internal/agent"
	"pieqi/internal/model"
)

// setupToolCallWire 构造一套 wired 环境：fake adapter + bus + 订阅 + store + 一个 running task。
func setupToolCallWire(t *testing.T) (*fakePermAdapter, *EventBus, *Subscription, *TaskStore, string, *ToolCallHandle) {
	t.Helper()
	bus := NewEventBus()
	sub := bus.Subscribe(64)
	store, err := NewTaskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tt, err := store.Create(&model.Task{ProjectID: "p", Prompt: "hi", Status: model.TaskRunning})
	if err != nil {
		t.Fatal(err)
	}
	fa := newFakePermAdapter()
	h := WireToolCall(fa, bus, store, tt.ID)
	return fa, bus, sub, store, tt.ID, h
}

// lastEvent 返回 task 的最后一个事件（已断言非空）。
func lastEvent(t *testing.T, store *TaskStore, taskID string) model.TaskEvent {
	t.Helper()
	tt, ok := store.Get(taskID)
	if !ok {
		t.Fatal("task missing")
	}
	if len(tt.Events) == 0 {
		t.Fatal("no events")
	}
	return tt.Events[len(tt.Events)-1]
}

// TestWireToolCall_NewAppendsToolUse ToolCall(IsNew=true) → 追加 EventToolUse 事件 + task_updated。
func TestWireToolCall_NewAppendsToolUse(t *testing.T) {
	fa, _, sub, store, taskID, h := setupToolCallWire(t)
	defer h.Unwire()

	fa.emitToolCall(agent.ToolCallUpdateInfo{
		SessionID:  "sess",
		ToolCallID: "tc-1",
		Title:      "Bash",
		Status:     "pending",
		Kind:       "execute",
		IsNew:      true,
		RawInput:   json.RawMessage(`{"command":"ls -la"}`),
	})

	evs := drainEvents(sub, 80*time.Millisecond)
	sawUpdate := false
	for _, ev := range evs {
		if ev.Type == "task_updated" && ev.Task != nil {
			sawUpdate = true
		}
	}
	if !sawUpdate {
		t.Fatalf("no task_updated emitted: %+v", evs)
	}

	tt, _ := store.Get(taskID)
	if len(tt.Events) != 1 {
		t.Fatalf("events len=%d, want 1: %+v", len(tt.Events), tt.Events)
	}
	ev := tt.Events[0]
	if ev.Type != model.EventToolUse {
		t.Errorf("event type=%q, want tool_use", ev.Type)
	}
	if ev.ToolName != "Bash" {
		t.Errorf("event ToolName=%q, want Bash", ev.ToolName)
	}
	if ev.ToolUseID != "tc-1" {
		t.Errorf("event ToolUseID=%q, want tc-1", ev.ToolUseID)
	}
	if ev.Seq != 1 {
		t.Errorf("event Seq=%d, want 1", ev.Seq)
	}
	// Input 由 RawInput 填充（行为与 Phase 1 一致）。
	if string(ev.Input) != `{"command":"ls -la"}` {
		t.Errorf("event Input=%q want {\"command\":\"ls -la\"}", string(ev.Input))
	}
}

// TestWireToolCall_UpdateCompleted ToolCallUpdate(completed) → 追加 EventToolResult，IsError=false。
func TestWireToolCall_UpdateCompleted(t *testing.T) {
	fa, _, _, store, taskID, h := setupToolCallWire(t)
	defer h.Unwire()

	// 先有 tool_use（提供 ToolName 供回查补全），再 completed。
	fa.emitToolCall(agent.ToolCallUpdateInfo{
		ToolCallID: "tc-2", Title: "Read", Status: "pending", IsNew: true,
	})
	fa.emitToolCall(agent.ToolCallUpdateInfo{
		ToolCallID: "tc-2", Status: "completed", IsNew: false, // 无 Title，应回查补全 "Read"
		RawOutput: json.RawMessage(`{"content":"hello"}`),
	})

	tt, _ := store.Get(taskID)
	if len(tt.Events) != 2 {
		t.Fatalf("events len=%d, want 2: %+v", len(tt.Events), tt.Events)
	}
	ev := tt.Events[1]
	if ev.Type != model.EventToolResult {
		t.Errorf("event type=%q, want tool_result", ev.Type)
	}
	if ev.IsError {
		t.Errorf("IsError=true, want false for completed")
	}
	if ev.ToolUseID != "tc-2" {
		t.Errorf("ToolUseID=%q, want tc-2", ev.ToolUseID)
	}
	// 回查补全：completed 未带 Title，ToolName 应取自前面 tool_use 的 "Read"。
	if ev.ToolName != "Read" {
		t.Errorf("ToolName=%q, want Read (looked up from tool_use)", ev.ToolName)
	}
	if ev.Seq != 2 {
		t.Errorf("Seq=%d, want 2", ev.Seq)
	}
	// Result 由 RawOutput 填充（转 string，行为与 Phase 1 一致）。
	if ev.Result != `{"content":"hello"}` {
		t.Errorf("Result=%q want {\"content\":\"hello\"}", ev.Result)
	}
}

// TestWireToolCall_UpdateFailed ToolCallUpdate(failed) → 追加 EventToolResult，IsError=true。
func TestWireToolCall_UpdateFailed(t *testing.T) {
	fa, _, _, store, taskID, h := setupToolCallWire(t)
	defer h.Unwire()

	fa.emitToolCall(agent.ToolCallUpdateInfo{
		ToolCallID: "tc-3", Title: "Write", Status: "pending", IsNew: true,
	})
	fa.emitToolCall(agent.ToolCallUpdateInfo{
		ToolCallID: "tc-3", Title: "Write", Status: "failed", IsNew: false,
		RawOutput: json.RawMessage(`{"error":"denied"}`),
	})

	ev := lastEvent(t, store, taskID)
	if ev.Type != model.EventToolResult {
		t.Errorf("event type=%q, want tool_result", ev.Type)
	}
	if !ev.IsError {
		t.Errorf("IsError=false, want true for failed")
	}
	if ev.ToolName != "Write" {
		t.Errorf("ToolName=%q, want Write", ev.ToolName)
	}
	// failed 也带 Result（取自 RawOutput）。
	if ev.Result != `{"error":"denied"}` {
		t.Errorf("Result=%q want {\"error\":\"denied\"}", ev.Result)
	}
}

// TestWireToolCall_IntermediateStatusIgnored pending/in_progress 等中间状态变更不产生事件。
func TestWireToolCall_IntermediateStatusIgnored(t *testing.T) {
	fa, _, _, store, taskID, h := setupToolCallWire(t)
	defer h.Unwire()

	fa.emitToolCall(agent.ToolCallUpdateInfo{
		ToolCallID: "tc-4", Title: "Bash", Status: "pending", IsNew: true,
	})
	// in_progress 更新：应被忽略，不追加事件。
	fa.emitToolCall(agent.ToolCallUpdateInfo{
		ToolCallID: "tc-4", Status: "in_progress", IsNew: false,
	})

	tt, _ := store.Get(taskID)
	if len(tt.Events) != 1 {
		t.Fatalf("events len=%d, want 1 (in_progress ignored): %+v", len(tt.Events), tt.Events)
	}
	if tt.Events[0].Type != model.EventToolUse {
		t.Errorf("event[0] type=%q, want tool_use", tt.Events[0].Type)
	}
}

// TestWireToolCall_UnwireStopsCallback Unwire 后回调不再触发：无新事件、store 不变。
func TestWireToolCall_UnwireStopsCallback(t *testing.T) {
	fa, _, sub, store, taskID, h := setupToolCallWire(t)

	fa.emitToolCall(agent.ToolCallUpdateInfo{
		ToolCallID: "tc-5", Title: "Bash", Status: "pending", IsNew: true,
	})
	_ = drainEvents(sub, 60*time.Millisecond)

	h.Unwire()
	fa.emitToolCall(agent.ToolCallUpdateInfo{
		ToolCallID: "tc-6", Title: "Write", Status: "pending", IsNew: true,
	})

	tt, _ := store.Get(taskID)
	if len(tt.Events) != 1 || tt.Events[0].ToolUseID != "tc-5" {
		t.Fatalf("store changed after Unwire: %+v", tt.Events)
	}
}

// TestWireToolCall_UnwireIdempotent Unwire 多次调用不 panic。
func TestWireToolCall_UnwireIdempotent(t *testing.T) {
	_, _, _, _, _, h := setupToolCallWire(t)
	h.Unwire()
	h.Unwire()
}
