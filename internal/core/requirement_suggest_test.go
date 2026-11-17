package core

import (
	"errors"
	"strings"
	"testing"

	"pieqi/internal/model"
)

func TestIsSplitSuggestTask_LegacyPrefix(t *testing.T) {
	if !IsSplitSuggestTask(&model.Task{Title: "[pieqi-split] old"}) {
		t.Fatal("legacy title prefix should match")
	}
	if !IsSplitSuggestTask(&model.Task{Title: "x", Skills: []string{"pieqi.split_suggest"}}) {
		t.Fatal("legacy skill should match")
	}
	if IsSplitSuggestTask(&model.Task{Title: "normal"}) {
		t.Fatal("plain title must not match")
	}
}

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

func TestParseSuggestOutcome_Kinds(t *testing.T) {
	t.Run("array", func(t *testing.T) {
		out, err := ParseSuggestOutcome(`[{"temp_id":"a","prompt":"p"}]`)
		if err != nil || out.Kind != SuggestParseProposal || len(out.Nodes) != 1 {
			t.Fatalf("%v %+v", err, out)
		}
	})
	t.Run("kind_proposal", func(t *testing.T) {
		out, err := ParseSuggestOutcome(`{"kind":"proposal","nodes":[{"temp_id":"a","prompt":"p"}]}`)
		if err != nil || out.Kind != SuggestParseProposal || out.Nodes[0].TempID != "a" {
			t.Fatalf("%v %+v", err, out)
		}
	})
	t.Run("kind_blocked", func(t *testing.T) {
		raw := `{"kind":"blocked","summary":"不清","reasons":["无AC"],"questions":["AC是什么？"],"missing":["acceptance"]}`
		out, err := ParseSuggestOutcome(raw)
		if err != nil || out.Kind != SuggestParseBlocked || out.Block == nil {
			t.Fatalf("%v %+v", err, out)
		}
		if out.Block.Summary != "不清" || len(out.Block.Missing) != 1 {
			t.Fatalf("%+v", out.Block)
		}
	})
	t.Run("illegal", func(t *testing.T) {
		if _, err := ParseSuggestOutcome(`not json`); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("empty", func(t *testing.T) {
		if _, err := ParseSuggestOutcome("   "); err == nil {
			t.Fatal("expected error")
		}
	})
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

func TestStartSplitSuggest_RequiresBranch(t *testing.T) {
	ts := newTestTaskStore(t)
	rs := NewRequirementStore(ts.Backend())
	rs.SetTaskStore(ts)
	req, _ := rs.Create(&model.Requirement{
		Title: "feat login", ProjectPath: t.TempDir(),
		Acceptance: []model.AcceptanceItem{{ID: "ac1", Text: "login", Verifiable: true}},
	})
	_, err := rs.StartSplitSuggest(req.ID, "qoder", "Qwen3.8-Flash")
	var want *ErrIntegrationBranchRequired
	if !errors.As(err, &want) {
		t.Fatalf("want ErrIntegrationBranchRequired, got %v", err)
	}
	// 无 task 被创建
	for _, task := range ts.List() {
		if task.RequirementID == req.ID {
			t.Fatalf("unexpected task %+v", task)
		}
	}
}

func TestStartSplitSuggest_WorktreeIsProjectPath(t *testing.T) {
	ts := newTestTaskStore(t)
	rs := NewRequirementStore(ts.Backend())
	rs.SetTaskStore(ts)
	dir := t.TempDir()
	req, _ := rs.Create(&model.Requirement{
		Title: "feat login", ProjectPath: dir, IntegrationBranch: "main",
		Acceptance: []model.AcceptanceItem{{ID: "ac1", Text: "login", Verifiable: true}},
	})
	task, err := rs.StartSplitSuggest(req.ID, "qoder", "")
	if err != nil {
		t.Fatal(err)
	}
	if task.WorktreePath != dir || task.WorktreePath != task.ProjectPath {
		t.Fatalf("WorktreePath=%q ProjectPath=%q", task.WorktreePath, task.ProjectPath)
	}
}

func TestIngestBlocked_NoProposal_ThenPutProposalClearsBlock(t *testing.T) {
	ts := newTestTaskStore(t)
	rs := NewRequirementStore(ts.Backend())
	rs.SetTaskStore(ts)
	req, _ := rs.Create(&model.Requirement{
		Title: "feat", ProjectPath: t.TempDir(), IntegrationBranch: "main",
		Acceptance: []model.AcceptanceItem{{ID: "ac1", Text: "a", Verifiable: true}},
	})
	err := rs.IngestSuggestOutcome(req.ID, &SuggestParseResult{
		Kind: SuggestParseBlocked,
		Block: &model.SplitBlock{
			Summary: "不清",
			Missing: []string{"acceptance"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rs.GetProposal(req.ID); ok {
		t.Fatal("blocked must not create proposal")
	}
	out := rs.GetSuggestOutcome(req.ID)
	if out.Kind != model.SuggestOutcomeBlocked || out.Block == nil {
		t.Fatalf("%+v", out)
	}
	_, err = rs.PutProposal(req.ID, []model.ProposedTaskNode{
		{TempID: "n1", Prompt: "do", CoversAC: []string{"ac1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rs.GetBlock(req.ID); ok {
		t.Fatal("PutProposal should clear block")
	}
	out = rs.GetSuggestOutcome(req.ID)
	if out.Kind != model.SuggestOutcomeProposal {
		t.Fatalf("%+v", out)
	}
}

func TestStartSplitSuggest_AndIngest(t *testing.T) {
	ts := newTestTaskStore(t)
	rs := NewRequirementStore(ts.Backend())
	rs.SetTaskStore(ts)
	req, _ := rs.Create(&model.Requirement{
		Title: "feat login", ProjectPath: t.TempDir(), IntegrationBranch: "main",
		Acceptance: []model.AcceptanceItem{{ID: "ac1", Text: "login", Verifiable: true}},
	})
	task, err := rs.StartSplitSuggest(req.ID, "qoder", "Qwen3.8-Flash")
	if err != nil {
		t.Fatal(err)
	}
	if !IsSplitSuggestTask(task) {
		t.Fatal("marker missing")
	}
	if !strings.HasPrefix(task.Title, "[split]") {
		t.Fatalf("title=%q, want [split] prefix", task.Title)
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

func TestTryIngest_Blocked(t *testing.T) {
	ts := newTestTaskStore(t)
	rs := NewRequirementStore(ts.Backend())
	rs.SetTaskStore(ts)
	req, _ := rs.Create(&model.Requirement{
		Title: "feat", ProjectPath: t.TempDir(), IntegrationBranch: "main",
	})
	task, err := rs.StartSplitSuggest(req.ID, "qoder", "")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = ts.Update(task.ID, func(x *model.Task) bool {
		x.Status = model.TaskCompleted
		x.Output = `{"kind":"blocked","summary":"缺AC","missing":["acceptance"]}`
		return true
	})
	done, _ := ts.Get(task.ID)
	rs.OnTaskCompleted(done)
	if _, ok := rs.GetProposal(req.ID); ok {
		t.Fatal("must not put proposal")
	}
	b, ok := rs.GetBlock(req.ID)
	if !ok || b.Summary != "缺AC" {
		t.Fatalf("block=%v ok=%v", b, ok)
	}
}
