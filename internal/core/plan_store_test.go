package core

import (
	"testing"

	"pieqi/internal/model"
)

func TestPublishPlan_ImmutableAndPin(t *testing.T) {
	ts := newTestTaskStore(t)
	rs := NewRequirementStore(ts.Backend())
	rs.SetTaskStore(ts)
	req, err := rs.Create(&model.Requirement{Title: "pe", ProjectPath: gitProjectPath(t)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = rs.SavePlanDraft(req.ID, &model.PlanDraft{
		Stage: model.PlanStageEngineering,
		Nodes: []model.NodeRevision{{NodeID: "n1", Title: "impl", Role: model.RoleBackend}},
	})
	if err != nil {
		t.Fatal(err)
	}
	rev, err := rs.PublishPlan(req.ID, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if !rev.Immutable || rev.Revision != 1 {
		t.Fatalf("rev=%+v", rev)
	}
	_, err = rs.SavePlanDraft(req.ID, &model.PlanDraft{
		Stage: model.PlanStageEngineering,
		Nodes: []model.NodeRevision{{NodeID: "n1", Title: "changed", Role: model.RoleBackend}},
	})
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := rs.PinnedRevision(req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pinned.Nodes[0].Title != "impl" {
		t.Fatalf("revision mutated: %q", pinned.Nodes[0].Title)
	}
}

func TestPublishPlan_BlockedByOpenQuestion(t *testing.T) {
	ts := newTestTaskStore(t)
	rs := NewRequirementStore(ts.Backend())
	req, err := rs.Create(&model.Requirement{Title: "pe", ProjectPath: gitProjectPath(t)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = rs.SavePlanDraft(req.ID, &model.PlanDraft{
		Nodes: []model.NodeRevision{{NodeID: "n1", Title: "x", Role: model.RoleBackend}},
		Assertions: []model.PlanAssertion{{
			Kind: model.AssertOpenQuestion, Text: "?", BlocksPublish: true,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rs.PublishPlan(req.ID, ""); err == nil {
		t.Fatal("expected block")
	}
}

func TestMaterializePinnedPlan_SetsNodeID(t *testing.T) {
	ts := newTestTaskStore(t)
	rs := NewRequirementStore(ts.Backend())
	rs.SetTaskStore(ts)
	req, err := rs.Create(&model.Requirement{
		Title: "pe", ProjectPath: gitProjectPath(t),
		Acceptance: []model.AcceptanceItem{{ID: "ac1", Text: "ok", Verifiable: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = rs.SavePlanDraft(req.ID, &model.PlanDraft{
		Stage: model.PlanStageEngineering,
		Nodes: []model.NodeRevision{
			{NodeID: "design", Title: "design", Role: model.RoleDesign, CoversAC: []string{"ac1"}},
			{NodeID: "impl", Title: "impl", Role: model.RoleBackend, DependsOnNodes: []string{"design"}, CoversAC: []string{"ac1"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rs.PublishPlan(req.ID, "t"); err != nil {
		t.Fatal(err)
	}
	out, err := rs.MaterializePinnedPlan(req.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Tasks) != 2 {
		t.Fatalf("tasks=%d", len(out.Tasks))
	}
	byNode := map[string]*model.Task{}
	for _, tk := range out.Tasks {
		byNode[tk.NodeID] = tk
	}
	if byNode["impl"] == nil || len(byNode["impl"].DependsOn) != 1 || byNode["impl"].DependsOn[0] != byNode["design"].ID {
		t.Fatalf("deps broken: %+v", byNode)
	}
}

func TestMaterialize_RequiresPin(t *testing.T) {
	ts := newTestTaskStore(t)
	rs := NewRequirementStore(ts.Backend())
	rs.SetTaskStore(ts)
	req, err := rs.Create(&model.Requirement{Title: "pe", ProjectPath: gitProjectPath(t)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rs.MaterializePinnedPlan(req.ID, false); err == nil {
		t.Fatal("expected error")
	}
}
