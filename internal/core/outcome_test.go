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
	// Files 与 DiffBrief 必须同序同量：前端概览的文件清单就是拿它挂「打开」入口的，
	// 少一条 = 那个文件看得见点不开（这条断言防的正是"只维护了 diff_brief"）。
	if len(ev.Files) != len(ev.DiffBrief) {
		t.Fatalf("files/diffBrief 数量不一致: %d vs %d", len(ev.Files), len(ev.DiffBrief))
	}
	if ev.Files[0].Path != "a.go" || ev.Files[0].Operation != "modify" || ev.Files[0].Additions != 10 || ev.Files[0].Deletions != 2 {
		t.Fatalf("files[0]: %+v", ev.Files[0])
	}
	if ev.Files[0].Open != "" {
		t.Fatalf("core 不应填 Open（那是 api 层的知识）: %+v", ev.Files[0])
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

// TestEvidencePrompt_NoVisualResidue 视觉采集层已整体移除（ADR-0012 截图、ADR-0013 console/network），
// prompt 里**不得**再出现任何视觉证据痕迹。
//
// 为什么写成负向断言而不是直接删掉旧测试：这三段输出（截图 URL / 页面 Console / 页面网络失败）
// 在历史上是**逐个**加进来的，删的时候很容易漏一段、或日后被"顺手加回来"。
// 用一条测试把"整个视觉层不出现在 prompt 里"钉住，比删掉测试更能防回归。
func TestEvidencePrompt_NoVisualResidue(t *testing.T) {
	ev := Evidence{TaskID: "t1"}
	prompt := EvidencePrompt("顶部按钮错位", ev, nil)

	if !strings.Contains(prompt, "顶部按钮错位") {
		t.Fatalf("instruction 应出现在 prompt 里:\n%s", prompt)
	}
	for _, banned := range []string{"截图", "/screenshots/", "页面 Console", "页面网络失败", "console", "network"} {
		if strings.Contains(prompt, banned) {
			t.Fatalf("prompt 不应含视觉证据痕迹 %q（该层已移除）:\n%s", banned, prompt)
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
