package core

import (
	"testing"

	"pieqi/internal/model"
)

func TestDeriveRequirementOutcome_TicketGreenNeedsEvidence(t *testing.T) {
	req := &model.Requirement{
		ID: "r1",
		Acceptance: []model.AcceptanceItem{
			{ID: "ac1", Text: "a"},
		},
	}
	tasks := []*model.Task{{
		ID: "t1", RequirementID: "r1", Status: model.TaskCompleted, CoversAC: []string{"ac1"},
	}}
	out := DeriveRequirementOutcome(req, tasks)
	if out.CanMarkDone {
		t.Fatal("ticket green alone must not can_mark_done")
	}
	if len(out.MissingEvidence) != 1 || out.MissingEvidence[0] != "ac1" {
		t.Fatalf("missing=%v", out.MissingEvidence)
	}
	if out.Acceptance[0].State != model.ACPending {
		t.Fatalf("state=%s", out.Acceptance[0].State)
	}
}

func TestDeriveRequirementOutcome_HumanPassIsEvidence(t *testing.T) {
	req := &model.Requirement{
		ID: "r1",
		Acceptance: []model.AcceptanceItem{
			{ID: "ac1", Text: "a", HumanResult: "pass"},
		},
	}
	out := DeriveRequirementOutcome(req, nil)
	if !out.CanMarkDone || !out.Acceptance[0].HasEvidence {
		t.Fatalf("%+v", out)
	}
}

func TestDeriveRequirementOutcome_ZeroACStillDone(t *testing.T) {
	req := &model.Requirement{ID: "r1"}
	out := DeriveRequirementOutcome(req, nil)
	if !out.CanMarkDone {
		t.Fatal("zero AC with no open issues should can_mark_done")
	}
}

func TestContentFingerprint_Stable(t *testing.T) {
	a := ContentFingerprint(`D:\x`, "t", "b")
	b := ContentFingerprint(`D:\x`, "t", "b")
	c := ContentFingerprint(`D:\x`, "t", "b2")
	if a != b || a == c {
		t.Fatalf("a=%s b=%s c=%s", a, b, c)
	}
}
