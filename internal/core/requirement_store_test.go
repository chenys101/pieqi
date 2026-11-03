package core

import (
	"testing"

	"pieqi/internal/model"
)

func TestRequirementStore_CRUD(t *testing.T) {
	ts := newTestTaskStore(t)

	rs := NewRequirementStore(ts.Backend())
	created, err := rs.Create(&model.Requirement{
		Title:       "need auth",
		ProjectPath: t.TempDir(),
		Acceptance:  []model.AcceptanceItem{{ID: "ac1", Text: "login works", Verifiable: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Status != model.RequirementDraft {
		t.Fatalf("created=%+v", created)
	}

	got, ok := rs.Get(created.ID)
	if !ok || got.Title != "need auth" {
		t.Fatalf("get=%v ok=%v", got, ok)
	}

	updated, err := rs.Update(created.ID, func(r *model.Requirement) bool {
		r.Status = model.RequirementActive
		return true
	})
	if err != nil || updated.Status != model.RequirementActive {
		t.Fatalf("update=%v err=%v", updated, err)
	}

	if err := rs.Delete(created.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := rs.Get(created.ID); ok {
		t.Fatal("still present after delete")
	}
}

func TestRequirementStore_RequiresProjectPath(t *testing.T) {
	ts := newTestTaskStore(t)
	rs := NewRequirementStore(ts.Backend())
	if _, err := rs.Create(&model.Requirement{Title: "x"}); err == nil {
		t.Fatal("expected error without project_path")
	}
}
