package workflow

import (
	"strings"
	"testing"

	"pieqi/internal/model"
)

func TestEmbeddedRegistry(t *testing.T) {
	r, err := NewRegistry("", IDPlanSerial)
	if err != nil {
		t.Fatal(err)
	}
	if r.DefaultID() != IDPlanSerial {
		t.Fatalf("default=%s", r.DefaultID())
	}
	list := r.List()
	if len(list) < 3 {
		t.Fatalf("want >=3 packs, got %d", len(list))
	}
	p, err := r.Get(IDPlanSerial)
	if err != nil {
		t.Fatal(err)
	}
	if p.Gates.MaxParallelImplement != 1 {
		t.Fatalf("parallel=%d", p.Gates.MaxParallelImplement)
	}
	d, err := r.Detail(IDSingle)
	if err != nil || d.SplitInstructions == "" {
		t.Fatalf("detail=%+v err=%v", d, err)
	}
}

// TestEmbeddedDoD_NoPIEQIHomeAsUniversal：外项目不得把 PIEQI_HOME 当隔离通项。
func TestEmbeddedDoD_NoPIEQIHomeAsUniversal(t *testing.T) {
	r, err := NewRegistry("", IDPlanSerial)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{IDSingle, IDPlanSerial, IDDAG} {
		p, err := r.Get(id)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if strings.Contains(p.DoDTest, "pieqi delivery") {
			t.Fatalf("%s: dod_test section still named pieqi delivery", id)
		}
		if !strings.Contains(p.DoDTest, "## delivery (test)") {
			t.Fatalf("%s: dod_test missing neutral ## delivery (test)", id)
		}
		if !strings.Contains(p.DoDTest, "isolated") {
			t.Fatalf("%s: dod_test must keep isolation discipline", id)
		}
		if !strings.Contains(p.DoDTest, "target project") {
			t.Fatalf("%s: dod_test must defer port/data dirs to target project", id)
		}
		for _, banned := range []string{
			"PIEQI_HOME", "PIEQI_SERVER_PORT", ":3000", "~/.pieqi",
			"If this repo is pieqi", "pieqi service",
		} {
			if strings.Contains(p.DoDTest, banned) || strings.Contains(p.DoDImplement, banned) {
				t.Fatalf("%s: DoD must not contain project-specific %q", id, banned)
			}
		}
	}
}

func TestValidateProposalGates_Parallel(t *testing.T) {
	p := &Pack{
		ID: "t",
		Gates: Gates{
			MaxNodes:             5,
			MaxParallelImplement: 1,
			ForbidFeBeHardSplit:  true,
		},
	}
	nodes := []model.ProposedTaskNode{
		{TempID: "a", Prompt: "a", Role: model.RoleOther},
		{TempID: "b", Prompt: "b", Role: model.RoleOther},
	}
	if err := ValidateProposalGates(p, nodes); err == nil {
		t.Fatal("want parallel error")
	}
	nodes[1].DependsOn = []string{"a"}
	if err := ValidateProposalGates(p, nodes); err != nil {
		t.Fatal(err)
	}
}

func TestValidateProposalGates_FeBe(t *testing.T) {
	p := &Pack{ID: "t", Gates: Gates{ForbidFeBeHardSplit: true, MaxNodes: 10, MaxParallelImplement: 2}}
	nodes := []model.ProposedTaskNode{
		{TempID: "f", Prompt: "f", Role: model.RoleFrontend},
		{TempID: "b", Prompt: "b", Role: model.RoleBackend, DependsOn: []string{"f"}},
	}
	if err := ValidateProposalGates(p, nodes); err == nil {
		t.Fatal("want FE/BE error")
	}
}

func TestValidateProposalGates_ForceSkipMaxNodes(t *testing.T) {
	p := &Pack{ID: "t", Gates: Gates{MaxNodes: 1, MaxParallelImplement: 1}}
	nodes := []model.ProposedTaskNode{
		{TempID: "a", Prompt: "a", Role: model.RoleOther},
		{TempID: "b", Prompt: "b", Role: model.RoleTest, DependsOn: []string{"a"}},
	}
	if err := ValidateProposalGates(p, nodes); err == nil {
		t.Fatal("want max_nodes error")
	}
	if err := ValidateProposalGatesOpts(p, nodes, true); err != nil {
		t.Fatal(err)
	}
}
