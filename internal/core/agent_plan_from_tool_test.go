package core

import (
	"encoding/json"
	"testing"

	"pieqi/internal/agent"
	"pieqi/internal/model"
)

func TestApplyTodoToolAsPlan_ReplacesSnapshotWithoutEventPlan(t *testing.T) {
	task := &model.Task{ID: "t1"}
	in := json.RawMessage(`{"todos":[
		{"content":"Inspect","status":"completed"},
		{"content":"Edit","status":"in_progress"},
		{"content":"Verify","status":"pending"}
	]}`)
	if !ApplyTodoToolAsPlan(task, "todo_write", in) {
		t.Fatal("expected change")
	}
	if task.Plan == nil || len(task.Plan.Entries) != 3 {
		t.Fatalf("Plan=%v", task.Plan)
	}
	if task.Plan.Entries[0].Status != model.PlanCompleted ||
		task.Plan.Entries[1].Status != model.PlanInProgress ||
		task.Plan.Entries[2].Status != model.PlanPending {
		t.Fatalf("statuses=%+v", task.Plan.Entries)
	}
	// 抬升路径故意不写 EventPlan。
	if len(task.Events) != 0 {
		t.Fatalf("Events=%d want 0", len(task.Events))
	}
}

func TestApplyTodoToolAsPlan_IgnoresNonTodoAndEmpty(t *testing.T) {
	task := &model.Task{ID: "t1", Plan: &model.PlanPayload{
		Entries: []model.PlanEntry{{Content: "keep", Status: model.PlanPending, Priority: model.PlanPriorityMedium}},
	}}
	if ApplyTodoToolAsPlan(task, "Bash", json.RawMessage(`{"todos":[{"content":"x","status":"pending"}]}`)) {
		t.Fatal("Bash must not lift")
	}
	if ApplyTodoToolAsPlan(task, "todo_write", json.RawMessage(`{"todos":[]}`)) {
		t.Fatal("empty todos must not overwrite")
	}
	if ApplyTodoToolAsPlan(task, "TodoWrite", nil) {
		t.Fatal("nil input must not overwrite")
	}
	if task.Plan == nil || task.Plan.Entries[0].Content != "keep" {
		t.Fatalf("Plan mutated: %+v", task.Plan)
	}
}

func TestApplyTodoToolAsPlan_NameVariants(t *testing.T) {
	for _, name := range []string{"TodoWrite", "todo_write", "write_todos", "TodoWrite foo"} {
		task := &model.Task{}
		in := json.RawMessage(`{"todos":[{"content":"A","status":"done"}]}`)
		if !ApplyTodoToolAsPlan(task, name, in) {
			t.Fatalf("%q: expected change", name)
		}
		if task.Plan.Entries[0].Status != model.PlanCompleted {
			t.Fatalf("%q status=%q", name, task.Plan.Entries[0].Status)
		}
	}
}

func TestApplyToolCallUpdate_LiftsTodoWriteIntoTaskPlan(t *testing.T) {
	bus := NewEventBus()
	store := newTestTaskStore(t)
	tt, err := store.Create(&model.Task{ProjectID: "p", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	fa := &fakePermAdapter{}
	h := WireToolCall(fa, bus, store, tt.ID)
	defer h.Unwire()

	fa.emitToolCall(agent.ToolCallUpdateInfo{
		IsNew:      true,
		ToolCallID: "c1",
		Title:      "todo_write",
		RawInput:   json.RawMessage(`{"todos":[{"content":"One","status":"in_progress"},{"content":"Two","status":"pending"}]}`),
	})
	// WireToolCall 合并缓冲：强制 flush。
	h.Flush()

	got, ok := store.Get(tt.ID)
	if !ok || got.Plan == nil || len(got.Plan.Entries) != 2 {
		t.Fatalf("Task.Plan=%v", got.Plan)
	}
	planEv := 0
	toolEv := 0
	for _, ev := range got.Events {
		switch ev.Type {
		case model.EventPlan:
			planEv++
		case model.EventToolUse:
			toolEv++
		}
	}
	if toolEv != 1 {
		t.Fatalf("tool_use=%d want 1", toolEv)
	}
	if planEv != 0 {
		t.Fatalf("EventPlan=%d want 0 (lift must not duplicate)", planEv)
	}
}

func TestApplyToolCallUpdate_LateInputAlsoLiftsPlan(t *testing.T) {
	bus := NewEventBus()
	store := newTestTaskStore(t)
	tt, err := store.Create(&model.Task{ProjectID: "p", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	fa := &fakePermAdapter{}
	h := WireToolCall(fa, bus, store, tt.ID)
	defer h.Unwire()

	// 先空壳开工通知。
	fa.emitToolCall(agent.ToolCallUpdateInfo{
		IsNew: true, ToolCallID: "c2", Title: "todo_write",
	})
	h.Flush()
	// 入参后到（非 IsNew 的中间态会不落事件，但 RawInput 应仍抬升）。
	fa.emitToolCall(agent.ToolCallUpdateInfo{
		ToolCallID: "c2",
		Title:      "todo_write",
		Status:     "in_progress",
		RawInput:   json.RawMessage(`{"todos":[{"content":"Late","status":"pending"}]}`),
	})
	h.Flush()

	got, ok := store.Get(tt.ID)
	if !ok || got.Plan == nil || len(got.Plan.Entries) != 1 || got.Plan.Entries[0].Content != "Late" {
		t.Fatalf("Task.Plan=%v", got.Plan)
	}
}
