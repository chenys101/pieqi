// outcome_test.go + evidence_test.go P1 Outcome / Evidence 单测。
package core

import (
	"encoding/json"
	"strings"
	"testing"

	"pieqi/internal/model"
)

func outcomeFixture() *model.Task {
	return &model.Task{
		ID: "t1", Status: model.TaskCompleted,
		Events: []model.TaskEvent{
			{Seq: 1, Type: model.EventUser, Text: "go"},
			{Seq: 2, Type: model.EventToolResult, ToolName: "Bash", ToolUseID: "x", Result: "exit 1", IsError: true},
			{Seq: 3, Type: model.EventRewind, Input: json.RawMessage(`{"to_turn":1,"restored":["a.go"]}`)},
		},
	}
}

func TestDeriveOutcome_StatusRules(t *testing.T) {
	task := outcomeFixture()

	// 无 failed check → completed
	o := DeriveOutcome(task, nil, nil, nil)
	if o.Status != OutcomeCompleted {
		t.Fatalf("completed rule: %+v", o.Status)
	}

	// 有 failed check → partial
	failed := []Check{{ID: "c1", Name: "npm build", Status: CheckFailed, ExitCode: 1}}
	o = DeriveOutcome(task, nil, failed, nil)
	if o.Status != OutcomePartial {
		t.Fatalf("partial rule: %+v", o.Status)
	}

	// task failed → failed（failed check 不再改变结论）
	task2 := outcomeFixture()
	task2.Status = model.TaskFailed
	task2.Error = "boom"
	o = DeriveOutcome(task2, nil, failed, nil)
	if o.Status != OutcomeFailed {
		t.Fatalf("failed rule: %+v", o.Status)
	}
}

func TestDeriveOutcome_IssuesAndRewinds(t *testing.T) {
	task := outcomeFixture()
	failed := []Check{{ID: "c1", Name: "npm build", Status: CheckFailed, ExitCode: 1}}
	o := DeriveOutcome(task, nil, failed, nil)

	// issues：failed check + 末轮 is_error
	joined := strings.Join(o.Issues, "|")
	if !strings.Contains(joined, "npm build") || !strings.Contains(joined, "Bash") {
		t.Fatalf("issues: %v", o.Issues)
	}
	// rewinds：rewind 事件载荷
	if len(o.Rewinds) != 1 || o.Rewinds[0].ToTurn != 1 || len(o.Rewinds[0].Restored) != 1 {
		t.Fatalf("rewinds: %+v", o.Rewinds)
	}
}

func TestBuildEvidence_TaskScope(t *testing.T) {
	task := outcomeFixture()
	changes := []FileChange{
		{Path: "a.go", Operation: "modify", Turn: 1, Additions: 10, Deletions: 2},
		{Path: "b.go", Operation: "create", Turn: 1, Additions: 5},
	}
	checks := []Check{
		{ID: "c1", Name: "npm test", Status: CheckSuccess},
		{ID: "c2", Name: "npm build", Status: CheckFailed, ExitCode: 1, Output: "error TS2304: cannot find name"},
	}
	ev := BuildEvidence(task, changes, checks, &FeedbackPreview{State: PreviewRunning}, ScopeTask, 0)

	if ev.Changes.Files != 2 || ev.Changes.Additions != 15 || ev.Changes.Deletions != 2 {
		t.Fatalf("changes: %+v", ev.Changes)
	}
	if len(ev.DiffBrief) != 2 || !strings.Contains(ev.DiffBrief[0], "a.go") {
		t.Fatalf("diffBrief: %v", ev.DiffBrief)
	}
	if len(ev.Checks) != 2 || ev.Errors != 1 {
		t.Fatalf("checks/errors: %+v %d", ev.Checks, ev.Errors)
	}

	// prompt 组装：含指令 / 变更 / 通过 / 失败摘要 / 预览 ready
	prompt := EvidencePrompt("修复 build 失败", ev, checks)
	for _, want := range []string{"修复 build 失败", "a.go", "检查通过: npm test", "检查未通过: npm build", "cannot find name", "ready"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

// TestEvidencePrompt_Visual P2 §6：Continue prompt 携带视觉证据引用
// （截图 URL + console 错误摘要 + 网络失败明细；Agent 是否看图由 provider 能力决定）。
func TestEvidencePrompt_Visual(t *testing.T) {
	ev := Evidence{
		TaskID: "t1",
		Screenshots: []string{
			"/api/tasks/t1/preview/screenshots/shot1.png",
			"/api/tasks/t1/preview/screenshots/shot2.png",
		},
		Console: &ConsoleSummary{
			Errors: 2, Warnings: 1,
			Entries: []ConsoleEntry{
				{Level: "error", Text: "Uncaught TypeError: btn is null"},
				{Level: "warn", Text: "deprecated API"},
			},
		},
		Network: &NetworkSummary{
			Failures: 1,
			Entries: []NetworkEntry{
				{URL: "http://127.0.0.1:5173/api/user", Method: "GET", Status: 500},
			},
		},
	}
	prompt := EvidencePrompt("顶部按钮错位", ev, nil)
	for _, want := range []string{
		"顶部按钮错位",
		"截图证据: /api/tasks/t1/preview/screenshots/shot1.png",
		"截图证据: /api/tasks/t1/preview/screenshots/shot2.png",
		"2 errors / 1 warnings",
		"[error] Uncaught TypeError: btn is null",
		"页面网络失败: 1 个请求",
		"GET http://127.0.0.1:5173/api/user (status=500)",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildEvidence_TurnScope(t *testing.T) {
	task := outcomeFixture()
	changes := []FileChange{
		{Path: "a.go", Operation: "modify", Turn: 1, Additions: 10},
		{Path: "b.go", Operation: "create", Turn: 2, Additions: 5},
	}
	checks := []Check{
		{ID: "c1", Name: "npm test", Turn: 1, Status: CheckSuccess, Origin: "agent"},
		{ID: "c2", Name: "npm lint", Turn: 2, Status: CheckSuccess, Origin: "agent"},
	}
	ev := BuildEvidence(task, changes, checks, nil, ScopeTurn, 1)
	if ev.Changes.Files != 1 || len(ev.DiffBrief) != 1 || len(ev.Checks) != 1 {
		t.Fatalf("turn scope: %+v", ev)
	}
	if ev.Checks[0].Name != "npm test" {
		t.Fatalf("turn filter checks: %+v", ev.Checks)
	}
}
