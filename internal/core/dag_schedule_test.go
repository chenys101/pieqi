package core

import (
	"testing"

	"pieqi/internal/model"
)

func TestDepsSatisfied_Empty(t *testing.T) {
	s := newTestTaskStore(t)
	tk, _ := s.Create(&model.Task{Prompt: "solo"})
	if !DepsSatisfied(s, tk) {
		t.Fatal("no deps should be satisfied")
	}
}

func TestActivatePlanned_BlockedUntilDepsComplete(t *testing.T) {
	s := newTestTaskStore(t)
	a, _ := s.Create(&model.Task{Prompt: "a", Status: model.TaskPlanned, RequirementID: "r1"})
	b, _ := s.Create(&model.Task{
		Prompt: "b", Status: model.TaskPlanned, RequirementID: "r1",
		DependsOn: []string{a.ID},
	})

	if _, err := s.ActivatePlanned(b.ID); err == nil {
		t.Fatal("expected deps not satisfied")
	}
	_, _ = s.Update(a.ID, func(x *model.Task) bool {
		x.Status = model.TaskCompleted
		return true
	})
	got, err := s.ActivatePlanned(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.TaskPending {
		t.Fatalf("status=%s want pending", got.Status)
	}
}

func TestReadyPlannedTasks(t *testing.T) {
	s := newTestTaskStore(t)
	a, _ := s.Create(&model.Task{Prompt: "a", Status: model.TaskPlanned, RequirementID: "r1"})
	_, _ = s.Create(&model.Task{
		Prompt: "b", Status: model.TaskPlanned, RequirementID: "r1",
		DependsOn: []string{a.ID},
	})
	ready := ReadyPlannedTasks(s, "r1")
	if len(ready) != 1 || ready[0].ID != a.ID {
		t.Fatalf("ready=%v want only a", ready)
	}
}
