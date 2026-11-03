package core

import (
	"testing"

	"pieqi/internal/model"
)

func TestPromote_OrphanTask(t *testing.T) {
	ts := newTestTaskStore(t)
	rs := NewRequirementStore(ts.Backend())
	rs.SetTaskStore(ts)

	req, err := rs.Create(&model.Requirement{Title: "box", ProjectPath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	orphan, _ := ts.Create(&model.Task{Prompt: "already running work"})
	_, _ = ts.Update(orphan.ID, func(x *model.Task) bool {
		x.Status = model.TaskCompleted
		return true
	})

	got, err := rs.Promote(req.ID, orphan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RequirementID != req.ID {
		t.Fatalf("requirement_id=%q", got.RequirementID)
	}
	// Timeline 未因 promote 被清空
	if len(got.Events) != 0 && got.Prompt != "already running work" {
		t.Fatal("timeline/prompt should be preserved")
	}
	r, _ := rs.Get(req.ID)
	if r.Status != model.RequirementActive {
		t.Fatalf("status=%s want active", r.Status)
	}
}
