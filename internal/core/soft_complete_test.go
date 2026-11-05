package core

import (
	"strings"
	"testing"
	"time"

	"pieqi/internal/model"
)

// V2.0.1 ac1：软完成收紧——有产出≠completed；未决审批 / 悬挂 tool_use → failed。
// 文案前缀钉死；choice 路径 B 不被改写。

func TestToolHungWithoutResult(t *testing.T) {
	cases := []struct {
		name   string
		events []model.TaskEvent
		want   bool
	}{
		{"empty", nil, false},
		{"text only", []model.TaskEvent{{Type: model.EventText, Text: "hi"}}, false},
		{"tool_use then result", []model.TaskEvent{
			{Type: model.EventToolUse, ToolUseID: "c1"},
			{Type: model.EventToolResult, ToolUseID: "c1"},
		}, false},
		{"hung tool_use", []model.TaskEvent{
			{Type: model.EventToolUse, ToolUseID: "c1"},
		}, true},
		{"hung then trailing text", []model.TaskEvent{
			{Type: model.EventToolUse, ToolUseID: "c1"},
			{Type: model.EventText, Text: "still talking"},
			{Type: model.EventThinking, Text: "..."},
		}, true},
		{"matched then new hung", []model.TaskEvent{
			{Type: model.EventToolUse, ToolUseID: "c1"},
			{Type: model.EventToolResult, ToolUseID: "c1"},
			{Type: model.EventToolUse, ToolUseID: "c2"},
		}, true},
		{"last is result", []model.TaskEvent{
			{Type: model.EventToolUse, ToolUseID: "c1"},
			{Type: model.EventText, Text: "x"},
			{Type: model.EventToolResult, ToolUseID: "c1"},
			{Type: model.EventText, Text: "done"},
		}, false},
		{"empty tool use id counts as hung", []model.TaskEvent{
			{Type: model.EventToolUse},
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := toolHungWithoutResult(tc.events); got != tc.want {
				t.Fatalf("toolHungWithoutResult=%v, want %v", got, tc.want)
			}
		})
	}
}

// TestCompleteAfterTurn_PendingPermissionFails：wire 仍有未 settle pending → failed + approval unresolved。
func TestCompleteAfterTurn_PendingPermissionFails(t *testing.T) {
	tr, store, bus, _ := newTestTaskRunner(t)
	task, err := store.Create(&model.Task{ID: "t-soft-perm", Prompt: "p", Output: "partial"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, _ = store.Update(task.ID, func(x *model.Task) bool {
		x.Status = model.TaskRunning
		x.Events = append(x.Events, model.TaskEvent{Type: model.EventText, Text: "partial", At: time.Now()})
		return true
	})

	fa := newFakePermAdapter()
	ph := WirePermission(fa, bus, store, task.ID, nil, time.Minute, nil, nil)
	defer ph.Unwire()
	registerTestWires(t, tr, task.ID, ph)
	ph.mu.Lock()
	ph.pending["req-hang"] = &permPending{reqID: "req-hang"}
	ph.mu.Unlock()

	tr.completeAfterTurn(task.ID, "partial")

	got, _ := store.Get(task.ID)
	if got.Status != model.TaskFailed {
		t.Fatalf("status=%s, want failed（有 pending 不得 completed）", got.Status)
	}
	if !strings.Contains(got.Error, errApprovalUnresolved) {
		t.Fatalf("Error=%q, want contain %q", got.Error, errApprovalUnresolved)
	}
	if !strings.Contains(got.Error, "req-hang") {
		t.Fatalf("Error=%q, want attach reqID", got.Error)
	}
}

// TestCompleteAfterTurn_HungToolUseFails：末条相关为 tool_use 无匹配 result → failed。
func TestCompleteAfterTurn_HungToolUseFails(t *testing.T) {
	tr, store, _, _ := newTestTaskRunner(t)
	task, err := store.Create(&model.Task{ID: "t-soft-hung", Prompt: "p", Output: "thinking aloud"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, _ = store.Update(task.ID, func(x *model.Task) bool {
		x.Status = model.TaskRunning
		x.Events = []model.TaskEvent{
			{Type: model.EventToolUse, ToolName: "Bash", ToolUseID: "call-094b", At: time.Now()},
			{Type: model.EventText, Text: "still going", At: time.Now()},
		}
		return true
	})

	tr.completeAfterTurn(task.ID, "thinking aloud")

	got, _ := store.Get(task.ID)
	if got.Status != model.TaskFailed {
		t.Fatalf("status=%s, want failed", got.Status)
	}
	if got.Error != errToolHungWithoutResult {
		t.Fatalf("Error=%q, want exact %q", got.Error, errToolHungWithoutResult)
	}
}

// TestCompleteAfterTurn_TestReportFail：test 票正文声明总评 FAIL → failed，便于 OnTaskFailed 开 Issue。
func TestCompleteAfterTurn_TestReportFail(t *testing.T) {
	tr, store, _, _ := newTestTaskRunner(t)
	task, err := store.Create(&model.Task{
		ID: "t-soft-test-fail", Prompt: "e2e", Role: model.RoleTest, Output: "partial",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	report := "**总评：FAIL**（隔离实例）\nac4 未通过"
	_, _ = store.Update(task.ID, func(x *model.Task) bool {
		x.Status = model.TaskRunning
		x.Events = []model.TaskEvent{{Type: model.EventText, Text: report, At: time.Now()}}
		return true
	})
	tr.completeAfterTurn(task.ID, report)
	got, _ := store.Get(task.ID)
	if got.Status != model.TaskFailed {
		t.Fatalf("status=%s error=%q, want failed", got.Status, got.Error)
	}
	if got.Error != errTestReportFail {
		t.Fatalf("Error=%q, want %q", got.Error, errTestReportFail)
	}
}

// TestCompleteAfterTurn_NormalCompletes：无悬挂、无 pending → 仍 completed。
func TestCompleteAfterTurn_NormalCompletes(t *testing.T) {
	tr, store, bus, _ := newTestTaskRunner(t)
	task, err := store.Create(&model.Task{ID: "t-soft-ok", Prompt: "p", Output: "done"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, _ = store.Update(task.ID, func(x *model.Task) bool {
		x.Status = model.TaskRunning
		x.Events = []model.TaskEvent{
			{Type: model.EventToolUse, ToolUseID: "c1", At: time.Now()},
			{Type: model.EventToolResult, ToolUseID: "c1", Result: "ok", At: time.Now()},
			{Type: model.EventText, Text: "done", At: time.Now()},
		}
		return true
	})

	// wire 存在但 pending 空 —— 不得误伤正常完成。
	fa := newFakePermAdapter()
	ph := WirePermission(fa, bus, store, task.ID, nil, time.Minute, nil, nil)
	defer ph.Unwire()
	registerTestWires(t, tr, task.ID, ph)

	tr.completeAfterTurn(task.ID, "done")

	got, _ := store.Get(task.ID)
	if got.Status != model.TaskCompleted {
		t.Fatalf("status=%s error=%q, want completed", got.Status, got.Error)
	}
}

// TestCompleteAfterTurn_PendingTakesPriorityOverHung：两者同时存在时优先 approval unresolved。
func TestCompleteAfterTurn_PendingTakesPriorityOverHung(t *testing.T) {
	tr, store, bus, _ := newTestTaskRunner(t)
	task, err := store.Create(&model.Task{ID: "t-soft-both", Prompt: "p", Output: "x"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, _ = store.Update(task.ID, func(x *model.Task) bool {
		x.Status = model.TaskRunning
		x.Events = []model.TaskEvent{
			{Type: model.EventToolUse, ToolUseID: "hung", At: time.Now()},
		}
		return true
	})
	fa := newFakePermAdapter()
	ph := WirePermission(fa, bus, store, task.ID, nil, time.Minute, nil, nil)
	defer ph.Unwire()
	registerTestWires(t, tr, task.ID, ph)
	ph.mu.Lock()
	ph.pending["req-first"] = &permPending{reqID: "req-first"}
	ph.mu.Unlock()

	tr.completeAfterTurn(task.ID, "x")

	got, _ := store.Get(task.ID)
	if got.Status != model.TaskFailed {
		t.Fatalf("status=%s, want failed", got.Status)
	}
	if !strings.Contains(got.Error, errApprovalUnresolved) {
		t.Fatalf("Error=%q, want approval unresolved（优先于 hung tool）", got.Error)
	}
}

// TestCompleteAfterTurn_ChoicePathBUnchanged：已 waiting_input(choice) 时轮末入口不得改写。
func TestCompleteAfterTurn_ChoicePathBUnchanged(t *testing.T) {
	tr, store, bus, _ := newTestTaskRunner(t)
	task, err := store.Create(&model.Task{ID: "t-soft-choice", Prompt: "p", Output: "ask"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, _ = store.Update(task.ID, func(x *model.Task) bool {
		x.Status = model.TaskWaitingInput
		x.CurrentDecision = &model.Decision{
			ID: "c1", Kind: model.DecisionKindChoice,
			Summary: "用哪种？", Options: []string{"A", "B"},
			CreatedAt: time.Now(),
		}
		// 故意塞一条悬挂 tool_use + 挂 pending，验证 choice 守卫优先、不被软完成收紧改写。
		x.Events = []model.TaskEvent{
			{Type: model.EventToolUse, ToolUseID: "should-not-matter", At: time.Now()},
		}
		return true
	})
	fa := newFakePermAdapter()
	ph := WirePermission(fa, bus, store, task.ID, nil, time.Minute, nil, nil)
	defer ph.Unwire()
	registerTestWires(t, tr, task.ID, ph)
	ph.mu.Lock()
	ph.pending["req-ignored"] = &permPending{reqID: "req-ignored"}
	ph.mu.Unlock()

	tr.completeAfterTurn(task.ID, "ask")

	got, _ := store.Get(task.ID)
	if got.Status != model.TaskWaitingInput {
		t.Fatalf("status=%s, want waiting_input（choice 路径 B 不变）", got.Status)
	}
	if got.CurrentDecision == nil || got.CurrentDecision.Kind != model.DecisionKindChoice {
		t.Fatalf("decision=%+v, want kind=choice", got.CurrentDecision)
	}
	if got.Error != "" {
		t.Fatalf("Error=%q, choice 不得被 failTask 写 Error", got.Error)
	}
}

// TestCompleteAfterTurn_ParseStreamNormalStillCompletes：print result 路径经 completeAfterTurn 仍 completed。
func TestCompleteAfterTurn_ParseStreamNormalStillCompletes(t *testing.T) {
	tr, store, _, _ := newTestTaskRunner(t)
	task, _ := store.Create(&model.Task{ProjectID: "soft-stream", Prompt: "p"})

	stream := strings.Join([]string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hello"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"hello world","session_id":"s1","num_turns":1}`,
	}, "\n")
	tr.parseStream(task.ID, strings.NewReader(stream))

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if t2, _ := store.Get(task.ID); t2 != nil && t2.Status == model.TaskCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	got, _ := store.Get(task.ID)
	if got.Status != model.TaskCompleted {
		t.Fatalf("status=%s error=%q, want completed", got.Status, got.Error)
	}
}

// TestCompleteAfterTurn_PlanDoesNotGate：plan 未完成不得阻止正常 completed（硬否决项）。
func TestCompleteAfterTurn_PlanDoesNotGate(t *testing.T) {
	tr, store, _, _ := newTestTaskRunner(t)
	task, err := store.Create(&model.Task{ID: "t-soft-plan", Prompt: "p", Output: "round done"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, _ = store.Update(task.ID, func(x *model.Task) bool {
		x.Status = model.TaskRunning
		x.Plan = &model.PlanPayload{
			Entries: []model.PlanEntry{
				{Content: "still open", Status: model.PlanInProgress},
			},
		}
		x.Events = []model.TaskEvent{
			{Type: model.EventText, Text: "round done", At: time.Now()},
		}
		return true
	})

	tr.completeAfterTurn(task.ID, "round done")

	got, _ := store.Get(task.ID)
	if got.Status != model.TaskCompleted {
		t.Fatalf("status=%s error=%q: plan 不作闸，应 completed", got.Status, got.Error)
	}
}
