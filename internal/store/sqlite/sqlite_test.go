package sqlite_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"pieqi/internal/model"
	"pieqi/internal/store/sqlite"
)

func TestOpenUpsertGetTask(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now()
	task := &model.Task{
		ID: "t1", Status: model.TaskPending, Prompt: "hi",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := db.UpsertTask(task); err != nil {
		t.Fatal(err)
	}
	got, ok, err := db.GetTask("t1")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.Prompt != "hi" {
		t.Fatalf("prompt=%q", got.Prompt)
	}
}

func TestMigrateJSONTasks(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	task := model.Task{
		ID: "legacy-1", Status: model.TaskCompleted, Prompt: "from json",
		CreatedAt: now, UpdatedAt: now,
	}
	raw, _ := json.Marshal(task)
	if err := os.WriteFile(filepath.Join(dir, "legacy-1.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	db, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	res, err := db.MigrateJSONTasks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported != 1 {
		t.Fatalf("imported=%d want 1", res.Imported)
	}
	if _, err := os.Stat(filepath.Join(dir, "legacy-1.json")); !os.IsNotExist(err) {
		t.Fatal("json should be archived")
	}
	got, ok, err := db.GetTask("legacy-1")
	if err != nil || !ok || got.Prompt != "from json" {
		t.Fatalf("got=%v ok=%v err=%v", got, ok, err)
	}
}

func TestRequirementCRUD(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now()
	r := &model.Requirement{
		ID: "r1", Title: "demo", ProjectPath: `D:\proj`, Status: model.RequirementDraft,
		Acceptance: []model.AcceptanceItem{{ID: "ac1", Text: "works", Verifiable: true}},
		CreatedAt:  now, UpdatedAt: now,
	}
	if err := db.UpsertRequirement(r); err != nil {
		t.Fatal(err)
	}
	got, ok, err := db.GetRequirement("r1")
	if err != nil || !ok {
		t.Fatalf("get: %v %v", ok, err)
	}
	if got.Title != "demo" || len(got.Acceptance) != 1 {
		t.Fatalf("got=%+v", got)
	}
	if err := db.DeleteRequirement("r1"); err != nil {
		t.Fatal(err)
	}
	_, ok, _ = db.GetRequirement("r1")
	if ok {
		t.Fatal("deleted requirement still present")
	}
}
