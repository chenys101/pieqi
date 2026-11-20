package core

import (
	"testing"

	"pieqi/internal/model"
)

func TestCanPublish_BlocksOpenQuestion(t *testing.T) {
	d := &model.PlanDraft{
		Assertions: []model.PlanAssertion{{
			Kind: model.AssertOpenQuestion, Text: "?", BlocksPublish: true,
		}},
		Nodes: []model.NodeRevision{{NodeID: "n1", Title: "x", Role: model.RoleBackend}},
	}
	if err := CanPublish(d); err == nil {
		t.Fatal("expected block")
	}
}

func TestCanPublish_BlocksAssumptionWithoutRisk(t *testing.T) {
	d := &model.PlanDraft{
		Assertions: []model.PlanAssertion{{
			Kind: model.AssertAssumption, Text: "msg", BlocksPublish: true,
		}},
		Nodes: []model.NodeRevision{{NodeID: "n1", Title: "x", Role: model.RoleBackend}},
	}
	if err := CanPublish(d); err == nil {
		t.Fatal("expected block")
	}
}

func TestCanPublish_OK_WithAcceptedRiskAndNodes(t *testing.T) {
	d := &model.PlanDraft{
		Assertions: []model.PlanAssertion{{
			Kind: model.AssertAssumption, Text: "…", AcceptedRisk: true, AcceptedBy: "u1", BlocksPublish: false,
		}},
		Nodes: []model.NodeRevision{{NodeID: "n1", Title: "x", Role: model.RoleBackend}},
	}
	if err := CanPublish(d); err != nil {
		t.Fatal(err)
	}
}

func TestCanPublish_RejectsUnknownDep(t *testing.T) {
	d := &model.PlanDraft{
		Nodes: []model.NodeRevision{{
			NodeID: "n1", Title: "x", Role: model.RoleTest, DependsOnNodes: []string{"missing"},
		}},
	}
	if err := CanPublish(d); err == nil {
		t.Fatal("expected block")
	}
}
