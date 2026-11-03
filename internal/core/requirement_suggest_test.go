package core

import (
	"testing"

	"pieqi/internal/model"
)

func TestParseSplitSuggestion_Fence(t *testing.T) {
	text := "如下：\n```json\n[{\"temp_id\":\"a\",\"prompt\":\"do a\",\"covers_ac\":[\"ac1\"]}]\n```\n"
	nodes, err := ParseSplitSuggestion(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].TempID != "a" {
		t.Fatalf("%+v", nodes)
	}
}

func TestParseSplitSuggestion_RawArray(t *testing.T) {
	nodes, err := ParseSplitSuggestion(`[{"temp_id":"x","prompt":"p"}]`)
	if err != nil || len(nodes) != 1 {
		t.Fatalf("%v %v", nodes, err)
	}
}

func TestEnsureProposalCoversAC(t *testing.T) {
	req := &model.Requirement{Acceptance: []model.AcceptanceItem{
		{ID: "ac1", Text: "a"}, {ID: "ac2", Text: "b"},
	}}
	nodes := []model.ProposedTaskNode{{TempID: "n1", Prompt: "p", CoversAC: []string{"ac1"}}}
	got := ensureProposalCoversAC(req, nodes)
	if len(got[0].CoversAC) != 2 {
		t.Fatalf("covers=%v", got[0].CoversAC)
	}
}

func TestStartSplitSuggest_AndIngest(t *testing.T) {
	ts := newTestTaskStore(t)
	rs := NewRequirementStore(ts.Backend())
	rs.SetTaskStore(ts)
	req, _ := rs.Create(&model.Requirement{
		Title: "feat login", ProjectPath: t.TempDir(),
		Acceptance: []model.AcceptanceItem{{ID: "ac1", Text: "login", Verifiable: true}},
	})
	task, err := rs.StartSplitSuggest(req.ID, "qoder", "Qwen3.8-Flash")
	if err != nil {
		t.Fatal(err)
	}
	if !IsSplitSuggestTask(task) {
		t.Fatal("marker missing")
	}
	if task.Agent != "qoder" || task.Model != "Qwen3.8-Flash" {
		t.Fatalf("agent/model = %q/%q", task.Agent, task.Model)
	}
	_, _ = ts.Update(task.ID, func(x *model.Task) bool {
		x.Status = model.TaskCompleted
		x.Output = `[{"temp_id":"design","title":"设计","prompt":"写方案","role":"design","covers_ac":["ac1"]}]`
		return true
	})
	done, _ := ts.Get(task.ID)
	rs.OnTaskCompleted(done)
	p, ok := rs.GetProposal(req.ID)
	if !ok || len(p.Nodes) != 1 {
		t.Fatalf("proposal=%v ok=%v", p, ok)
	}
}
