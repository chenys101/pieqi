package core

import (
	"testing"

	"pieqi/internal/model"
)

func TestExpand_DependsOnExistingTask(t *testing.T) {
	ts := newTestTaskStore(t)
	rs := NewRequirementStore(ts.Backend())
	rs.SetTaskStore(ts)

	req, err := rs.Create(&model.Requirement{
		Title:       "feat",
		ProjectPath: t.TempDir(),
		Acceptance:  []model.AcceptanceItem{{ID: "ac1", Text: "done"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := rs.Confirm(req.ID, []model.ProposedTaskNode{
		{TempID: "design", Prompt: "design", CoversAC: []string{"ac1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	designID := res.Tasks[0].ID
	_, _ = ts.Update(designID, func(x *model.Task) bool {
		x.Status = model.TaskCompleted
		return true
	})

	exp, err := rs.Expand(req.ID, []model.ProposedTaskNode{
		{TempID: "fe", Prompt: "frontend", DependsOn: []string{designID}, CoversAC: []string{"ac1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(exp.Tasks) != 1 {
		t.Fatalf("tasks=%d", len(exp.Tasks))
	}
	if len(exp.Tasks[0].DependsOn) != 1 || exp.Tasks[0].DependsOn[0] != designID {
		t.Fatalf("depends_on=%v", exp.Tasks[0].DependsOn)
	}
	r, _ := rs.Get(req.ID)
	found := false
	for _, ev := range r.Events {
		if ev.Type == model.ReqEventExpand {
			found = true
		}
	}
	if !found {
		t.Fatal("missing expand event")
	}
}
