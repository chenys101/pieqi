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
		ProjectPath: t.TempDir(),
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

func TestRequirementConfirm_RejectsBadProposal(t *testing.T) {
	ts := newTestTaskStore(t)
	rs := NewRequirementStore(ts.Backend())
	rs.SetTaskStore(ts)
	req, _ := rs.Create(&model.Requirement{
		Title: "x", ProjectPath: t.TempDir(),
		Acceptance: []model.AcceptanceItem{{ID: "ac1", Text: "a", Verifiable: true}},
	})
	_, err := rs.Confirm(req.ID, []model.ProposedTaskNode{
		{TempID: "n1", Prompt: "no cover"},
	})
	if err == nil {
		t.Fatal("expected uncover error")
	}
}
