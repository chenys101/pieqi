package core

import (
	"context"
	"path/filepath"
	"testing"

	"pieqi/internal/model"

	"go.uber.org/zap"
)

func TestEnsureTaskWorktree_DisabledUsesProjectRoot(t *testing.T) {
	store := newTestTaskStore(t)
	tr := NewTaskRunner(zap.NewNop(), store, nil, NewEventBus(), nil, "", "bypassPermissions", false, "", 0, nil, 0, 0)
	tr.SetUseWorktrees(false)

	proj := filepath.Join("D:", "code", "go", "demo") // 不必真实存在：本路径只写入 Task
	task, err := store.Create(&model.Task{
		ID: "t-no-wt", ProjectID: "p", ProjectPath: proj, Prompt: "x", Status: model.TaskPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := tr.ensureTaskWorktree(context.Background(), task, &model.Project{ID: "p", RepoPath: proj})
	if err != nil {
		t.Fatal(err)
	}
	if got.WorktreePath != proj {
		t.Fatalf("WorktreePath=%q, want project root %q", got.WorktreePath, proj)
	}
}

func TestEnsureTaskWorktree_SuggestDotWorktrees(t *testing.T) {
	if _, err := filepath.Abs("."); err != nil {
		t.Fatal(err)
	}
	// 路径计算不落盘；Create 才调 git —— 这里只测 WorktreePathFor
	p, err := WorktreePathFor("", &model.Project{ID: "p", RepoPath: `D:\code\go\demo`}, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(`D:\code\go\demo`, ".worktrees", "task-1")
	if p != want {
		t.Fatalf("got %q want %q", p, want)
	}
}
