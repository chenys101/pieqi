package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"pieqi/internal/agent"
	"pieqi/internal/model"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestShouldDiscardWorktreeOnSessionClose(t *testing.T) {
	failed := &model.Task{Status: model.TaskFailed}
	completed := &model.Task{Status: model.TaskCompleted}
	cancelled := &model.Task{Status: model.TaskCancelled}

	cases := []struct {
		name   string
		task   *model.Task
		reason string
		want   bool
	}{
		{"reaper failed keep", failed, agent.SessionCloseReaper, false},
		{"reaper completed keep", completed, agent.SessionCloseReaper, false},
		{"reaper cancelled discard", cancelled, agent.SessionCloseReaper, true},
		{"cancel failed discard", failed, agent.SessionCloseCancel, true},
		{"delete completed discard", completed, agent.SessionCloseDelete, true},
		{"nil task discard", nil, agent.SessionCloseReaper, true},
		{"close other failed keep", failed, agent.SessionCloseOther, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldDiscardWorktreeOnSessionClose(tc.task, tc.reason); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

// TestOnAgentSessionClosed_ReaperKeepsFailedWorktree 验收 1：
// failTask 态 + reaper Close → worktree 目录与分支仍在。
func TestOnAgentSessionClosed_ReaperKeepsFailedWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	repo := initRepo(t, dir, "main")
	wm := NewWorktreeManager(zap.NewNop(), "")
	project := &model.Project{ID: "p", RepoPath: repo}
	ctx := context.Background()
	wtPath, err := wm.Create(ctx, project, "task-keep")
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(wtPath, "uncommitted.txt")
	if err := os.WriteFile(marker, []byte("keep-me"), 0644); err != nil {
		t.Fatal(err)
	}

	core, logs := observer.New(zapcore.InfoLevel)
	store := newTestTaskStore(t)
	tr := NewTaskRunner(zap.New(core), store, wm, NewEventBus(), nil, "", "bypassPermissions", true, "", 0, nil, 0, 0)
	task, err := store.Create(&model.Task{
		ID: "task-keep", ProjectID: "p", ProjectPath: repo,
		WorktreePath: wtPath, Prompt: "x", Status: model.TaskFailed,
		Error: "account quota exhausted",
	})
	if err != nil {
		t.Fatal(err)
	}

	tr.onAgentSessionClosed(task.ID, agent.SessionCloseReaper)

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("worktree should survive reaper close: %v", err)
	}
	if !branchExists(t, repo, "task/task-keep") {
		t.Fatal("branch should survive reaper close")
	}
	foundSkip := false
	for _, e := range logs.All() {
		if e.Message == "skip worktree cleanup: task still resumable" {
			foundSkip = true
			m := e.ContextMap()
			if m["task"] != task.ID || m["trigger"] != agent.SessionCloseReaper {
				t.Fatalf("skip log fields=%v", m)
			}
		}
		if e.Message == "worktree cleaned" {
			t.Fatal("reaper must not Cleanup resumable failed task")
		}
	}
	if !foundSkip {
		t.Fatal("expected skip worktree cleanup INFO with trigger=reaper")
	}
}

// TestOnAgentSessionClosed_CancelCleansWorktree 取消触发 Cleanup，且 INFO 带 trigger=cancel。
func TestOnAgentSessionClosed_CancelCleansWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	repo := initRepo(t, dir, "main")
	wm := NewWorktreeManager(zap.NewNop(), "")
	project := &model.Project{ID: "p", RepoPath: repo}
	ctx := context.Background()
	wtPath, err := wm.Create(ctx, project, "task-cancel")
	if err != nil {
		t.Fatal(err)
	}

	core, logs := observer.New(zapcore.InfoLevel)
	store := newTestTaskStore(t)
	tr := NewTaskRunner(zap.New(core), store, wm, NewEventBus(), nil, "", "bypassPermissions", true, "", 0, nil, 0, 0)
	task, err := store.Create(&model.Task{
		ID: "task-cancel", ProjectID: "p", ProjectPath: repo,
		WorktreePath: wtPath, Prompt: "x", Status: model.TaskCancelled,
	})
	if err != nil {
		t.Fatal(err)
	}

	tr.onAgentSessionClosed(task.ID, agent.SessionCloseCancel)

	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Fatal("worktree should be removed after cancel cleanup")
	}
	found := false
	for _, e := range logs.All() {
		if e.Message == "worktree cleaned" {
			found = true
			m := e.ContextMap()
			if m["task"] != task.ID || m["trigger"] != agent.SessionCloseCancel || m["path"] != wtPath {
				t.Fatalf("cleanup log fields=%v", m)
			}
		}
	}
	if !found {
		t.Fatal("expected worktree cleaned INFO with trigger=cancel")
	}
}

func TestEnsureTaskWorktree_MissingDirRecreates(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	repo := initRepo(t, dir, "main")
	wm := NewWorktreeManager(zap.NewNop(), "")
	project := &model.Project{ID: "p", RepoPath: repo}
	ctx := context.Background()
	wtPath, err := wm.Create(ctx, project, "task-re")
	if err != nil {
		t.Fatal(err)
	}
	// 模拟旧 bug / 手工删目录：清掉磁盘 worktree，但任务仍登记路径。
	if err := wm.Cleanup(ctx, project, "task-re", wtPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Fatal("precondition: dir should be gone")
	}

	store := newTestTaskStore(t)
	tr := NewTaskRunner(zap.NewNop(), store, wm, NewEventBus(), nil, "", "bypassPermissions", false, "", 0, nil, 0, 0)
	tr.SetUseWorktrees(true)
	task, err := store.Create(&model.Task{
		ID: "task-re", ProjectID: "p", ProjectPath: repo,
		WorktreePath: wtPath, Prompt: "x", Status: model.TaskFailed,
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := tr.ensureTaskWorktree(ctx, task, project)
	if err != nil {
		t.Fatalf("ensure recreate: %v", err)
	}
	if !worktreeDirPresent(got.WorktreePath) {
		t.Fatalf("recreated path missing: %s", got.WorktreePath)
	}
}

func TestEnsureTaskWorktree_MissingProjectRootErrors(t *testing.T) {
	store := newTestTaskStore(t)
	tr := NewTaskRunner(zap.NewNop(), store, nil, NewEventBus(), nil, "", "bypassPermissions", false, "", 0, nil, 0, 0)
	tr.SetUseWorktrees(false)
	missing := filepath.Join(t.TempDir(), "gone")
	task, err := store.Create(&model.Task{
		ID: "t-miss", ProjectID: "p", ProjectPath: missing,
		WorktreePath: missing, Prompt: "x", Status: model.TaskFailed,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = tr.ensureTaskWorktree(context.Background(), task, &model.Project{ID: "p", RepoPath: missing})
	if err == nil {
		t.Fatal("want explicit error for missing project cwd")
	}
}
