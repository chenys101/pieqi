package core

import (
	"testing"

	"pieqi/internal/model"
)

func TestOpenIssue_BlockerCreatesFix(t *testing.T) {
	ts := newTestTaskStore(t)
	rs := NewRequirementStore(ts.Backend())
	rs.SetTaskStore(ts)

	req, _ := rs.Create(&model.Requirement{
		Title: "r", ProjectPath: t.TempDir(),
		Acceptance: []model.AcceptanceItem{{ID: "ac1", Text: "ok", Verifiable: true}},
	})
	res, err := rs.OpenIssue(req.ID, model.Issue{
		Title: "broken", Severity: model.SeverityBlocker, CoversAC: []string{"ac1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.FixTask == nil || res.FixTask.Role != model.RoleFix {
		t.Fatalf("fix=%v", res.FixTask)
	}
	if res.Issue.Status != model.IssueFixing || res.Issue.FixTaskID != res.FixTask.ID {
		t.Fatalf("issue=%+v", res.Issue)
	}
}

func TestConfirmFix_Major(t *testing.T) {
	ts := newTestTaskStore(t)
	rs := NewRequirementStore(ts.Backend())
	rs.SetTaskStore(ts)
	req, _ := rs.Create(&model.Requirement{Title: "r", ProjectPath: t.TempDir()})
	res, err := rs.OpenIssue(req.ID, model.Issue{
		Title: "nits", Severity: model.SeverityMajor, CoversAC: []string{"ac1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.FixTask != nil || res.Issue.Status != model.IssueOpen {
		t.Fatal("major must stay open without fix")
	}
	got, err := rs.ConfirmFix(req.ID, res.Issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.FixTask == nil || got.Issue.Status != model.IssueFixing {
		t.Fatalf("after confirm: %+v", got)
	}
}

func TestFixComplete_SpawnsReverify_ThenResolve(t *testing.T) {
	ts := newTestTaskStore(t)
	rs := NewRequirementStore(ts.Backend())
	rs.SetTaskStore(ts)
	req, _ := rs.Create(&model.Requirement{
		Title: "r", ProjectPath: t.TempDir(),
		Acceptance: []model.AcceptanceItem{{ID: "ac1", Text: "ok"}},
	})
	opened, _ := rs.OpenIssue(req.ID, model.Issue{
		Title: "bug", Severity: model.SeverityBlocker, CoversAC: []string{"ac1"},
	})
	fixID := opened.FixTask.ID
	_, _ = ts.Update(fixID, func(x *model.Task) bool {
		x.Status = model.TaskCompleted
		return true
	})
	fix, _ := ts.Get(fixID)
	rs.OnTaskCompleted(fix)

	r, _ := rs.Get(req.ID)
	var reverifyID string
	for _, iss := range r.Issues {
		if iss.ID == opened.Issue.ID {
			reverifyID = iss.ReverifyTaskID
		}
	}
	if reverifyID == "" {
		t.Fatal("expected reverify task")
	}
	_, _ = ts.Update(reverifyID, func(x *model.Task) bool {
		x.Status = model.TaskCompleted
		return true
	})
	rev, _ := ts.Get(reverifyID)
	rs.OnTaskCompleted(rev)

	r, _ = rs.Get(req.ID)
	if r.Issues[0].Status != model.IssueResolved {
		t.Fatalf("status=%s", r.Issues[0].Status)
	}
}

func TestMarkDone_RequiresGreenAC(t *testing.T) {
	ts := newTestTaskStore(t)
	rs := NewRequirementStore(ts.Backend())
	rs.SetTaskStore(ts)
	req, _ := rs.Create(&model.Requirement{
		Title: "r", ProjectPath: t.TempDir(),
		Acceptance: []model.AcceptanceItem{{ID: "ac1", Text: "ok"}},
	})
	if _, err := rs.MarkDone(req.ID); err == nil {
		t.Fatal("pending AC should block done")
	}
	_, _ = rs.HumanVerify(req.ID, "ac1", true, "")
	got, err := rs.MarkDone(req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.RequirementDone {
		t.Fatalf("status=%s", got.Status)
	}
}

func TestDeriveRequirementOutcome_OpenIssueRedsAC(t *testing.T) {
	req := &model.Requirement{
		ID: "r1",
		Acceptance: []model.AcceptanceItem{{ID: "ac1", Text: "a"}},
		Issues: []model.Issue{{
			ID: "i1", Status: model.IssueOpen, CoversAC: []string{"ac1"},
		}},
	}
	out := DeriveRequirementOutcome(req, nil)
	if out.Acceptance[0].State != model.ACRed || out.CanMarkDone {
		t.Fatalf("%+v", out)
	}
}

// TestFixReverify_RetargetsDownstreamDeps：源测 failed 永钉；终验 depends_on 源测时，
// spawnReverify 须改挂到重验票，重验绿后 DepsSatisfied 才真（假绿门禁）。
func TestFixReverify_RetargetsDownstreamDeps(t *testing.T) {
	ts := newTestTaskStore(t)
	rs := NewRequirementStore(ts.Backend())
	rs.SetTaskStore(ts)
	req, _ := rs.Create(&model.Requirement{
		Title: "r", ProjectPath: t.TempDir(),
		Acceptance: []model.AcceptanceItem{{ID: "ac1", Text: "ok"}},
	})

	src, _ := ts.Create(&model.Task{
		Prompt: "src test", Status: model.TaskFailed, Role: model.RoleTest,
		RequirementID: req.ID, CoversAC: []string{"ac1"},
	})
	final, _ := ts.Create(&model.Task{
		Prompt: "final verify", Status: model.TaskPlanned, Role: model.RoleTest,
		RequirementID: req.ID, DependsOn: []string{src.ID},
	})
	if DepsSatisfied(ts, final) {
		t.Fatal("failed source must block downstream")
	}

	opened, err := rs.OpenIssue(req.ID, model.Issue{
		Title: "bug", Severity: model.SeverityBlocker, CoversAC: []string{"ac1"},
		SourceTaskID: src.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	fixID := opened.FixTask.ID
	_, _ = ts.Update(fixID, func(x *model.Task) bool {
		x.Status = model.TaskCompleted
		return true
	})
	fix, _ := ts.Get(fixID)
	rs.OnTaskCompleted(fix)

	r, _ := rs.Get(req.ID)
	reverifyID := r.Issues[0].ReverifyTaskID
	if reverifyID == "" {
		t.Fatal("expected reverify")
	}
	final2, _ := ts.Get(final.ID)
	if !dependsOnContains(final2.DependsOn, reverifyID) {
		t.Fatalf("depends_on=%v want retarget to reverify %s", final2.DependsOn, reverifyID)
	}
	if dependsOnContains(final2.DependsOn, src.ID) {
		t.Fatalf("source %s must be removed from depends_on", src.ID)
	}
	if DepsSatisfied(ts, final2) {
		t.Fatal("fake-green: reverify not completed yet")
	}

	_, _ = ts.Update(reverifyID, func(x *model.Task) bool {
		x.Status = model.TaskCompleted
		return true
	})
	rev, _ := ts.Get(reverifyID)
	rs.OnTaskCompleted(rev)

	r, _ = rs.Get(req.ID)
	if r.Issues[0].Status != model.IssueResolved {
		t.Fatalf("issue status=%s", r.Issues[0].Status)
	}
	final3, _ := ts.Get(final.ID)
	if !DepsSatisfied(ts, final3) {
		t.Fatal("after reverify green, downstream deps must be satisfied")
	}
}

func TestRepairReverifyDeps_IdempotentOldGraph(t *testing.T) {
	ts := newTestTaskStore(t)
	rs := NewRequirementStore(ts.Backend())
	rs.SetTaskStore(ts)
	req, _ := rs.Create(&model.Requirement{Title: "r", ProjectPath: t.TempDir()})

	src, _ := ts.Create(&model.Task{
		Prompt: "src", Status: model.TaskFailed, Role: model.RoleTest, RequirementID: req.ID,
	})
	rev, _ := ts.Create(&model.Task{
		Prompt: "reverify", Status: model.TaskCompleted, Role: model.RoleTest, RequirementID: req.ID,
	})
	final, _ := ts.Create(&model.Task{
		Prompt: "final", Status: model.TaskPlanned, Role: model.RoleTest,
		RequirementID: req.ID, DependsOn: []string{src.ID},
	})
	_, _ = rs.Update(req.ID, func(r *model.Requirement) bool {
		r.Issues = []model.Issue{{
			ID: "i1", Title: "bug", Status: model.IssueResolved,
			SourceTaskID: src.ID, ReverifyTaskID: rev.ID,
		}}
		return true
	})

	if n := rs.RepairReverifyDeps(req.ID); n != 1 {
		t.Fatalf("repair n=%d want 1", n)
	}
	if n := rs.RepairReverifyDeps(req.ID); n != 0 {
		t.Fatalf("idempotent n=%d want 0", n)
	}
	got, _ := ts.Get(final.ID)
	if !dependsOnContains(got.DependsOn, rev.ID) || dependsOnContains(got.DependsOn, src.ID) {
		t.Fatalf("depends_on=%v", got.DependsOn)
	}
	if !DepsSatisfied(ts, got) {
		t.Fatal("repaired edge to completed reverify should satisfy")
	}
}
