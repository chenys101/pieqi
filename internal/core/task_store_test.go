package core

import (
	"testing"
	"time"

	"claude-bridge/internal/model"
)

func TestTaskStore_CreateGet(t *testing.T) {
	s, err := NewTaskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tt, err := s.Create(&model.Task{
		Source:      model.SourceHTTP,
		ProjectID:   "cb",
		ProjectPath: "G:/repo",
		Prompt:      "fix bug",
	})
	if err != nil {
		t.Fatal(err)
	}
	if tt.ID == "" || tt.ClaudeSessionID == "" {
		t.Fatal("Create should assign ID and ClaudeSessionID")
	}
	if tt.Status != model.TaskPending {
		t.Fatalf("status = %s, want pending", tt.Status)
	}

	got, ok := s.Get(tt.ID)
	if !ok {
		t.Fatal("Get failed after Create")
	}
	if got.Prompt != "fix bug" {
		t.Fatalf("prompt = %q", got.Prompt)
	}
	// Get returns a copy: mutating it must not affect store
	got.Prompt = "mutated"
	got2, _ := s.Get(tt.ID)
	if got2.Prompt != "fix bug" {
		t.Fatal("Get did not return a copy")
	}
}

func TestTaskStore_Update(t *testing.T) {
	s, _ := NewTaskStore(t.TempDir())
	tt, _ := s.Create(&model.Task{ProjectID: "cb", Prompt: "p"})

	updated, err := s.Update(tt.ID, func(t *model.Task) bool {
		t.Status = model.TaskRunning
		t.WorktreePath = "/wt"
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != model.TaskRunning || updated.WorktreePath != "/wt" {
		t.Fatalf("update not applied: %+v", updated)
	}
	if updated.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt should be set after change")
	}

	// mutator returning false = no change, no error
	_, err = s.Update(tt.ID, func(t *model.Task) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
}

func TestTaskStore_UpdateNotFound(t *testing.T) {
	s, _ := NewTaskStore(t.TempDir())
	_, err := s.Update("nope", func(t *model.Task) bool { return true })
	if err == nil {
		t.Fatal("Update on missing task should error")
	}
}

func TestTaskStore_Delete(t *testing.T) {
	s, _ := NewTaskStore(t.TempDir())
	tt, _ := s.Create(&model.Task{ProjectID: "cb"})
	if err := s.Delete(tt.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get(tt.ID); ok {
		t.Fatal("task should be gone after Delete")
	}
	if err := s.Delete(tt.ID); err == nil {
		t.Fatal("double Delete should error")
	}
}

func TestTaskStore_ListSortedByCreated(t *testing.T) {
	s, _ := NewTaskStore(t.TempDir())
	a, _ := s.Create(&model.Task{ProjectID: "cb"})
	time.Sleep(time.Millisecond)
	b, _ := s.Create(&model.Task{ProjectID: "cb"})
	time.Sleep(time.Millisecond)
	c, _ := s.Create(&model.Task{ProjectID: "cb"})

	list := s.List()
	if len(list) != 3 {
		t.Fatalf("len = %d", len(list))
	}
	if list[0].ID != a.ID || list[1].ID != b.ID || list[2].ID != c.ID {
		t.Fatal("List not sorted by CreatedAt")
	}
}

func TestTaskStore_RestoreRecoversAndMarksOrphans(t *testing.T) {
	dir := t.TempDir()
	s1, _ := NewTaskStore(dir)

	// 一个完成的、一个 running 的（模拟重启前还在跑）
	done, _ := s1.Create(&model.Task{ProjectID: "cb", Prompt: "done"})
	_, _ = s1.Update(done.ID, func(t *model.Task) bool { t.Status = model.TaskCompleted; return true })

	running, _ := s1.Create(&model.Task{ProjectID: "cb", Prompt: "wip"})
	_, _ = s1.Update(running.ID, func(t *model.Task) bool {
		t.Status = model.TaskRunning
		t.WorktreePath = "/wt"
		return true
	})
	waiting, _ := s1.Create(&model.Task{ProjectID: "cb", Prompt: "stuck"})
	_, _ = s1.Update(waiting.ID, func(t *model.Task) bool {
		t.Status = model.TaskWaitingInput
		t.CurrentDecision = &model.Decision{ID: "d1", ToolName: "Bash"}
		return true
	})

	// 重新打开同一个目录，模拟重启
	s2, err := NewTaskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	list := s2.List()
	if len(list) != 3 {
		t.Fatalf("recovered len = %d, want 3", len(list))
	}
	for _, tt := range list {
		if tt.Status == model.TaskRunning || tt.Status == model.TaskWaitingInput {
			t.Fatalf("orphan task %s should be marked failed, got %s", tt.ID, tt.Status)
		}
		if tt.Status == model.TaskFailed && tt.Error == "" {
			t.Fatalf("failed orphan %s should have error message", tt.ID)
		}
	}
	// 完成的那个应仍是 completed
	for _, tt := range list {
		if tt.ID == done.ID && tt.Status != model.TaskCompleted {
			t.Fatalf("completed task should survive restart, got %s", tt.Status)
		}
	}
}

func TestTaskStore_CreateFillsDefaults(t *testing.T) {
	s, _ := NewTaskStore(t.TempDir())
	tt, err := s.Create(&model.Task{})
	if err != nil {
		t.Fatal(err)
	}
	if tt.ID == "" {
		t.Fatal("ID should default to uuid")
	}
	if tt.ClaudeSessionID == "" {
		t.Fatal("ClaudeSessionID should default to uuid")
	}
	if tt.CreatedAt.IsZero() {
		t.Fatal("CreatedAt should be set")
	}
}
