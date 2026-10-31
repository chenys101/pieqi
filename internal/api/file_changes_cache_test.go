package api

import (
	"encoding/json"
	"testing"

	"pieqi/internal/config"
	"pieqi/internal/core"
	"pieqi/internal/model"

	"go.uber.org/zap"
)

func TestCachedFileChanges_ReusesUntilEventsChange(t *testing.T) {
	store, _ := core.NewTaskStore(t.TempDir())
	bus := core.NewEventBus()
	hooks := core.NewHookService(0)
	wm := core.NewWorktreeManager(zap.NewNop(), t.TempDir())
	runner := core.NewTaskRunner(zap.NewNop(), store, wm, bus, hooks, "", "bypassPermissions", false, "", 0, nil, 0, 0, "main")
	srv := NewServer(&config.Config{}, store, runner, hooks, bus, nil, nil)

	wt := t.TempDir()
	task, _ := store.Create(&model.Task{
		ProjectID: "c", ProjectPath: wt, WorktreePath: wt,
		Prompt: "p", Status: model.TaskCompleted,
		Events: []model.TaskEvent{
			{Seq: 1, Type: model.EventUser, Text: "t1"},
			{Seq: 2, Type: model.EventToolUse, ToolName: "Write", ToolUseID: "w1",
				Input: json.RawMessage(`{"file_path":"a.go","content":"x"}`)},
		},
	})

	first := srv.cachedFileChanges(task)
	if len(first) != 1 || first[0].Path != "a.go" {
		t.Fatalf("first = %+v", first)
	}
	// 同指针 = 命中缓存（未改 events）
	second := srv.cachedFileChanges(task)
	if &first[0] != &second[0] {
		t.Fatal("expected cached slice reuse")
	}

	updated, _ := store.Update(task.ID, func(tk *model.Task) bool {
		tk.Events = append(tk.Events,
			model.TaskEvent{Seq: 3, Type: model.EventToolUse, ToolName: "Write", ToolUseID: "w2",
				Input: json.RawMessage(`{"file_path":"b.go","content":"y"}`)},
		)
		return true
	})
	third := srv.cachedFileChanges(updated)
	if len(third) != 2 {
		t.Fatalf("after mutate want 2 paths, got %+v", third)
	}
}
