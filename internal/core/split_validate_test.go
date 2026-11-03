package core

import (
	"strings"
	"testing"

	"pieqi/internal/model"
)

func TestValidateSplitProposal_OK(t *testing.T) {
	req := &model.Requirement{
		Acceptance: []model.AcceptanceItem{
			{ID: "ac1", Text: "a", Verifiable: true},
			{ID: "ac2", Text: "b", Verifiable: true, Deferred: true},
		},
	}
	p := &model.SplitProposal{Nodes: []model.ProposedTaskNode{
		{TempID: "n1", Prompt: "design", Role: model.RoleDesign, CoversAC: []string{"ac1"}},
		{TempID: "n2", Prompt: "impl", Role: model.RoleFrontend, DependsOn: []string{"n1"}, CoversAC: []string{"ac1"}},
	}}
	if err := ValidateSplitProposal(req, p); err != nil {
		t.Fatal(err)
	}
}

func TestValidateSplitProposal_UncoveredAC(t *testing.T) {
	req := &model.Requirement{
		Acceptance: []model.AcceptanceItem{{ID: "ac1", Text: "a", Verifiable: true}},
	}
	p := &model.SplitProposal{Nodes: []model.ProposedTaskNode{
		{TempID: "n1", Prompt: "x"},
	}}
	err := ValidateSplitProposal(req, p)
	if err == nil || !strings.Contains(err.Error(), "uncovered") {
		t.Fatalf("err=%v, want uncovered", err)
	}
}

func TestValidateSplitProposal_Cycle(t *testing.T) {
	p := &model.SplitProposal{Nodes: []model.ProposedTaskNode{
		{TempID: "a", Prompt: "a", DependsOn: []string{"b"}},
		{TempID: "b", Prompt: "b", DependsOn: []string{"a"}},
	}}
	err := ValidateSplitProposal(nil, p)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("err=%v, want cycle", err)
	}
}

func TestValidateSplitProposal_UnknownDep(t *testing.T) {
	p := &model.SplitProposal{Nodes: []model.ProposedTaskNode{
		{TempID: "a", Prompt: "a", DependsOn: []string{"missing"}},
	}}
	err := ValidateSplitProposal(nil, p)
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("err=%v, want unknown", err)
	}
}

func TestValidateSplitProposal_ZeroACOK(t *testing.T) {
	req := &model.Requirement{}
	p := &model.SplitProposal{Nodes: []model.ProposedTaskNode{
		{TempID: "a", Prompt: "only one"},
	}}
	if err := ValidateSplitProposal(req, p); err != nil {
		t.Fatal(err)
	}
}
