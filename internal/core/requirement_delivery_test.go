package core

import (
	"strings"
	"testing"

	"pieqi/internal/model"
)

func TestAppendDeliveryDoD_ImplementAndTest(t *testing.T) {
	be := AppendDeliveryDoD(model.RoleBackend, "做后端")
	if !strings.Contains(be, "仅单元") && !strings.Contains(be, "单元/组件") {
		t.Fatalf("backend DoD missing unit-test rule: %s", be)
	}
	if !strings.Contains(be, "禁止起被测服务") {
		t.Fatalf("implement DoD should ban SUT service generically: %s", be)
	}
	te := AppendDeliveryDoD(model.RoleTest, "做功能测")
	if !strings.Contains(te, "隔离") || !strings.Contains(te, "需求分支") {
		t.Fatalf("test DoD missing isolation/branch: %s", te)
	}
	if !strings.Contains(te, "该项目自己的隔离运行环境") {
		t.Fatalf("test DoD should require project-owned isolation: %s", te)
	}
	// PIEQI_* 仅作「若本仓库即 pieqi」特例，不得写成无条件通项（括号「pieqi：独立…」）。
	if strings.Contains(te, "（pieqi：独立 PIEQI_HOME") {
		t.Fatalf("test DoD must not treat PIEQI_HOME as unconditional example: %s", te)
	}
	if !strings.Contains(te, "若本仓库即 pieqi") || !strings.Contains(te, "PIEQI_HOME") {
		t.Fatalf("pieqi self-test still needs conditional PIEQI_HOME guidance: %s", te)
	}
	// 幂等（新标记）
	again := AppendDeliveryDoD(model.RoleBackend, be)
	if again != be {
		t.Fatal("AppendDeliveryDoD should be idempotent")
	}
	// 幂等（旧标记）
	legacy := "旧票\n\n## pieqi 交付纪律（test 票 · 必须遵守）\n旧文案"
	if got := AppendDeliveryDoD(model.RoleTest, legacy); got != legacy {
		t.Fatalf("legacy marker should skip re-inject: %q", got)
	}
	legacyEN := "old\n\n## pieqi delivery (test)\nold copy"
	if got := AppendDeliveryDoD(model.RoleTest, legacyEN); got != legacyEN {
		t.Fatalf("legacy EN marker should skip re-inject: %q", got)
	}
	design := AppendDeliveryDoD(model.RoleDesign, "只设计")
	if design != "只设计" {
		t.Fatalf("design should not append DoD: %q", design)
	}
}

func TestCheckTestDependsOn(t *testing.T) {
	err := checkTestDependsOn([]model.ProposedTaskNode{
		{TempID: "be", Role: model.RoleBackend, Prompt: "x"},
		{TempID: "te", Role: model.RoleTest, Prompt: "y"},
	})
	if err == nil {
		t.Fatal("expected error for test without depends_on")
	}
	err = checkTestDependsOn([]model.ProposedTaskNode{
		{TempID: "be", Role: model.RoleBackend, Prompt: "x"},
		{TempID: "te", Role: model.RoleTest, Prompt: "y", DependsOn: []string{"be"}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestConfirmReplace_NeedsForceWhenProgressed(t *testing.T) {
	ts := newTestTaskStore(t)
	rs := NewRequirementStore(ts.Backend())
	rs.SetTaskStore(ts)
	req, err := rs.Create(&model.Requirement{
		Title: "feat", ProjectPath: gitProjectPath(t),
		Acceptance: []model.AcceptanceItem{{ID: "ac1", Text: "works", Verifiable: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := rs.Confirm(req.ID, []model.ProposedTaskNode{
		{TempID: "old", Prompt: "old", CoversAC: []string{"ac1"}, Role: model.RoleBackend},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first.Tasks[0].Prompt, "交付纪律") {
		t.Fatalf("confirm should inject DoD: %s", first.Tasks[0].Prompt)
	}
	// 推进到 completed
	if _, err := ts.Update(first.Tasks[0].ID, func(tk *model.Task) bool {
		tk.Status = model.TaskCompleted
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if !rs.HasProgressedWork(req.ID) {
		t.Fatal("expected progressed work")
	}
	_, err = rs.ConfirmReplace(req.ID, []model.ProposedTaskNode{
		{TempID: "n", Prompt: "new", CoversAC: []string{"ac1"}},
	}, false)
	if err == nil {
		t.Fatal("expected replace without force to fail")
	}
	out, err := rs.ConfirmReplace(req.ID, []model.ProposedTaskNode{
		{TempID: "n", Prompt: "new", CoversAC: []string{"ac1"}},
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if out.Abandoned < 1 {
		t.Fatalf("abandoned=%d", out.Abandoned)
	}
}

func TestOnTaskFailed_TestOpensBlockerIssue(t *testing.T) {
	ts := newTestTaskStore(t)
	rs := NewRequirementStore(ts.Backend())
	rs.SetTaskStore(ts)
	req, err := rs.Create(&model.Requirement{
		Title: "feat", ProjectPath: gitProjectPath(t),
		Acceptance: []model.AcceptanceItem{{ID: "ac1", Text: "works", Verifiable: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := rs.Confirm(req.ID, []model.ProposedTaskNode{
		{TempID: "be", Prompt: "impl", Role: model.RoleBackend, CoversAC: []string{"ac1"}},
		{TempID: "te", Prompt: "test", Role: model.RoleTest, CoversAC: []string{"ac1"}, DependsOn: []string{"be"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var testID string
	for _, tk := range out.Tasks {
		if tk.Role == model.RoleTest {
			testID = tk.ID
		}
	}
	if testID == "" {
		t.Fatal("no test task")
	}
	failed, err := ts.Update(testID, func(tk *model.Task) bool {
		tk.Status = model.TaskFailed
		tk.Error = "functional check red"
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	rs.OnTaskFailed(failed)
	r, _ := rs.Get(req.ID)
	if len(r.Issues) != 1 {
		t.Fatalf("issues=%d want 1", len(r.Issues))
	}
	if r.Issues[0].Severity != model.SeverityBlocker {
		t.Fatalf("severity=%s", r.Issues[0].Severity)
	}
	if r.Issues[0].FixTaskID == "" {
		t.Fatal("blocker should create fix task")
	}
	if r.Issues[0].SourceTaskID != testID {
		t.Fatalf("source=%s", r.Issues[0].SourceTaskID)
	}
	// 幂等
	rs.OnTaskFailed(failed)
	r, _ = rs.Get(req.ID)
	if len(r.Issues) != 1 {
		t.Fatalf("duplicate issues: %d", len(r.Issues))
	}
}
