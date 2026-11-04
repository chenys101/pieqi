package core

import (
	"testing"

	"pieqi/internal/model"
)

func TestRequirementConfirm_CreatesPlannedTasks(t *testing.T) {
	ts := newTestTaskStore(t)
	rs := NewRequirementStore(ts.Backend())
	rs.SetTaskStore(ts)

	req, err := rs.Create(&model.Requirement{
		Title:       "feat",
		ProjectPath: gitProjectPath(t),
		Acceptance:  []model.AcceptanceItem{{ID: "ac1", Text: "works", Verifiable: true}},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = rs.PutProposal(req.ID, []model.ProposedTaskNode{
		{TempID: "d", Prompt: "design it", Role: model.RoleDesign, CoversAC: []string{"ac1"}},
		{TempID: "f", Prompt: "build ui", Role: model.RoleFrontend, DependsOn: []string{"d"}, CoversAC: []string{"ac1"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	out, err := rs.Confirm(req.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Requirement.GraphVersion != 1 {
		t.Fatalf("graph_version=%d want 1", out.Requirement.GraphVersion)
	}
	if out.Requirement.Status != model.RequirementActive {
		t.Fatalf("status=%s", out.Requirement.Status)
	}
	if len(out.Tasks) != 2 {
		t.Fatalf("tasks=%d", len(out.Tasks))
	}
	byRole := map[model.TaskRole]*model.Task{}
	for _, tk := range out.Tasks {
		if tk.Status != model.TaskPlanned {
			t.Fatalf("status=%s want planned", tk.Status)
		}
		if tk.RequirementID != req.ID {
			t.Fatal("requirement_id not set")
		}
		byRole[tk.Role] = tk
	}
	fe := byRole[model.RoleFrontend]
	design := byRole[model.RoleDesign]
	if fe == nil || design == nil {
		t.Fatal("missing roles")
	}
	if len(fe.DependsOn) != 1 || fe.DependsOn[0] != design.ID {
		t.Fatalf("depends_on=%v want [%s]", fe.DependsOn, design.ID)
	}
	if _, ok := rs.GetProposal(req.ID); ok {
		t.Fatal("proposal should be discarded after confirm")
	}
}

func TestRequirementConfirm_WritesAgentModel(t *testing.T) {
	ts := newTestTaskStore(t)
	rs := NewRequirementStore(ts.Backend())
	rs.SetTaskStore(ts)
	req, _ := rs.Create(&model.Requirement{
		Title: "x", ProjectPath: gitProjectPath(t),
		Acceptance: []model.AcceptanceItem{{ID: "ac1", Text: "a", Verifiable: true}},
	})
	out, err := rs.Confirm(req.ID, []model.ProposedTaskNode{
		{TempID: "n1", Prompt: "p", CoversAC: []string{"ac1"}, Agent: "qoder", Model: "Qwen3.8-Flash"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Tasks) != 1 {
		t.Fatalf("tasks=%d", len(out.Tasks))
	}
	if out.Tasks[0].Agent != "qoder" || out.Tasks[0].Model != "Qwen3.8-Flash" {
		t.Fatalf("agent/model=%q/%q", out.Tasks[0].Agent, out.Tasks[0].Model)
	}
}

func TestRequirementConfirm_RejectsBadProposal(t *testing.T) {
	ts := newTestTaskStore(t)
	rs := NewRequirementStore(ts.Backend())
	rs.SetTaskStore(ts)
	req, _ := rs.Create(&model.Requirement{
		Title: "x", ProjectPath: gitProjectPath(t),
		Acceptance: []model.AcceptanceItem{{ID: "ac1", Text: "a", Verifiable: true}},
	})
	_, err := rs.Confirm(req.ID, []model.ProposedTaskNode{
		{TempID: "n1", Prompt: "no cover"},
	})
	if err == nil {
		t.Fatal("expected uncover error")
	}
}

func TestRequirementConfirmReplace_AbandonsOldWorkTasks(t *testing.T) {
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
		{TempID: "old", Prompt: "old work", CoversAC: []string{"ac1"}, Agent: "qoder"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Tasks) != 1 {
		t.Fatalf("first tasks=%d", len(first.Tasks))
	}
	oldID := first.Tasks[0].ID

	out, err := rs.ConfirmReplace(req.ID, []model.ProposedTaskNode{
		{TempID: "n1", Prompt: "new work", CoversAC: []string{"ac1"}, Agent: "claude"},
		{TempID: "n2", Prompt: "new work 2", CoversAC: []string{"ac1"}, DependsOn: []string{"n1"}},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if out.Abandoned != 1 {
		t.Fatalf("abandoned=%d want 1", out.Abandoned)
	}
	if len(out.Tasks) != 2 {
		t.Fatalf("new tasks=%d want 2", len(out.Tasks))
	}
	old, ok := ts.Get(oldID)
	if !ok {
		t.Fatal("old task missing")
	}
	if old.Status != model.TaskCancelled {
		t.Fatalf("old status=%s want cancelled", old.Status)
	}
	if old.Error != "abandoned: requirement re-split" {
		t.Fatalf("old error=%q", old.Error)
	}
	// 拆分会话本身若存在则不应被算进 abandoned（此处无 suggest task）。
	for _, tk := range out.Tasks {
		if tk.Status != model.TaskPlanned {
			t.Fatalf("new task status=%s", tk.Status)
		}
		if tk.ID == oldID {
			t.Fatal("new task reused old id")
		}
	}

	// 再次覆盖：只废弃本轮 active，不计历史 abandoned。
	out2, err := rs.ConfirmReplace(req.ID, []model.ProposedTaskNode{
		{TempID: "z", Prompt: "third", CoversAC: []string{"ac1"}},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if out2.Abandoned != 2 {
		t.Fatalf("second replace abandoned=%d want 2", out2.Abandoned)
	}
}
