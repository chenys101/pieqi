package core

import (
	"encoding/json"
	"strings"
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

// TestWireToolCall_BackfillInputMergesInsteadOfDuplicate 回归（2026-10-21）：
// 「审批卡上只有工具名、没有命令」的那条链路。
//
// dsh 的 tool_call 先来一条**没有任何入参**的（标题也可能缺），入参随后才在
// tool_call_update 里到。旧实现无条件追加，于是同一调用落了两条 tool_use：
// 第二条入参为空。用户在时间线/审批卡上看到的就是"有个 pwsh，但看不见命令"。
//
// 判据：补全后**仍然只有一条 tool_use**，且它的 Input 含有迟到的命令。
// 这条一旦回退，反馈面板的文件变更也会跟着空（派生层按 Input 取路径）。
func TestWireToolCall_BackfillInputMergesInsteadOfDuplicate(t *testing.T) {
	fa, _, _, store, taskID, h := setupToolCallWire(t)
	defer h.Unwire()

	// ① 开工：只有 id 与工具名，入参为空（dsh 常见形态）。
	fa.emitToolCall(agent.ToolCallUpdateInfo{
		ToolCallID: "tc-late", Title: "pwsh", Status: "in_progress", IsNew: true,
	})
	// ② 入参后到，同样以 IsNew 形态通告（ACP 适配器在 freshInput 时补发）。
	fa.emitToolCall(agent.ToolCallUpdateInfo{
		ToolCallID: "tc-late", Title: "pwsh", Status: "in_progress", IsNew: true,
		RawInput: json.RawMessage(`{"command":"git status --short","description":"查看改动"}`),
	})

	tt, _ := store.Get(taskID)
	var toolUses []model.TaskEvent
	for _, ev := range tt.Events {
		if ev.Type == model.EventToolUse {
			toolUses = append(toolUses, ev)
		}
	}
	if len(toolUses) != 1 {
		t.Fatalf("tool_use 条数=%d，want 1（迟到的入参必须合并进已有那条）: %+v", len(toolUses), toolUses)
	}
	if !strings.Contains(string(toolUses[0].Input), "git status --short") {
		t.Fatalf("tool_use.Input 未补全: %s", string(toolUses[0].Input))
	}
	if toolUses[0].ToolName != "pwsh" {
		t.Errorf("ToolName=%q，want pwsh", toolUses[0].ToolName)
	}
}

// TestWireToolCall_StatusUpdateCarriesLateInput 状态更新（IsNew=false）带的入参同样要落进事件。
//
// 这一条与上一条是同一缺陷的两条到达路径：ACP 适配器把 update 里的入参既喂审批备忘录、
// 也以 IsNew 形态补发一次；这里验证事件层在只收到 IsNew=false 时**也不会追加空壳**，
// 且拿到入参后不丢命令。
func TestWireToolCall_StatusUpdateCarriesLateInput(t *testing.T) {
	fa, _, _, store, taskID, h := setupToolCallWire(t)
	defer h.Unwire()

	fa.emitToolCall(agent.ToolCallUpdateInfo{
		ToolCallID: "tc-st", Title: "edit", Status: "in_progress", IsNew: true,
	})
	// 完成态，带 RawInput（某些 agent 只在 completed 里给入参）
	fa.emitToolCall(agent.ToolCallUpdateInfo{
		ToolCallID: "tc-st", Title: "edit", Status: "completed", IsNew: false,
		RawInput:  json.RawMessage(`{"file_path":"C:\\repo\\a.go"}`),
		RawOutput: json.RawMessage(`{"ok":true}`),
	})

	tt, _ := store.Get(taskID)
	useCount, resultCount := 0, 0
	for _, ev := range tt.Events {
		switch ev.Type {
		case model.EventToolUse:
			useCount++
		case model.EventToolResult:
			resultCount++
		}
	}
	if useCount != 1 || resultCount != 1 {
		t.Fatalf("tool_use=%d tool_result=%d，want 1/1: %+v", useCount, resultCount, tt.Events)
	}
	// 工具事件必须带上入参：派生层（FileChange / Check）全按 Input 取路径与命令，
	// 丢了入参就等于"这一轮没改过文件"。事件只补不删，所以这里看的是合并后的那条。
	var useArgs string
	for _, ev := range tt.Events {
		if ev.Type == model.EventToolUse {
			useArgs = string(ev.Input)
		}
	}
	if !strings.Contains(useArgs, "a.go") {
		t.Fatalf("tool_use.Input=%q，want 含 a.go（状态更新里的入参必须落到事件上）", useArgs)
	}
	// 注：这里**不断言** DeriveFileChanges 能派生出这条路径 —— 派生层对 `edit`
	// 目前直接返回空（见 feedback.go 的 toolFileOperations），是另一条缺陷，
	// 由 feedback_test.go 的用例单独守。
}
