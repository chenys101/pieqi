package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"pieqi/internal/model"
	"go.uber.org/zap"
)

// initRepo 在 dir 建一个 git repo，含一次 commit，返回 repo 路径。
func initRepo(t *testing.T, dir, branchName string) string {
	t.Helper()
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(repo, 0755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "init", "-b", branchName)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "add", "README.md")
	// git commit 需要身份
	mustGit(t, repo, "-c", "user.email=t@t.com", "-c", "user.name=t", "commit", "-m", "init")
	return repo
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

func TestWorktreeManager_CreateAndCleanup(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	repo := initRepo(t, dir, "main")
	wtBase := filepath.Join(dir, "worktrees")

	wm := NewWorktreeManager(zap.NewNop(), wtBase)
	project := &model.Project{ID: "cb", RepoPath: repo, BaseBranch: "main"}

	ctx := context.Background()
	wtPath, err := wm.Create(ctx, project, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wtPath); os.IsNotExist(err) {
		t.Fatalf("worktree path not created: %s", wtPath)
	}
	if !branchExists(t, repo, "pieqi/task-1") {
		t.Fatal("branch pieqi/task-1 not found")
	}
	if _, err := os.Stat(filepath.Join(wtPath, "README.md")); err != nil {
		t.Fatal("worktree missing README.md")
	}

	if err := wm.Cleanup(ctx, project, "task-1", wtPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Fatal("worktree dir should be removed after cleanup")
	}
	if branchExists(t, repo, "pieqi/task-1") {
		t.Fatal("branch should be deleted after cleanup")
	}
}

func TestWorktreeManager_CleanupIdempotent(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	repo := initRepo(t, dir, "main")
	wm := NewWorktreeManager(zap.NewNop(), filepath.Join(dir, "wt"))
	project := &model.Project{ID: "cb", RepoPath: repo, BaseBranch: "main"}
	ctx := context.Background()

	wtPath, _ := wm.Create(ctx, project, "task-2")
	wm.Cleanup(ctx, project, "task-2", wtPath)

	if err := wm.Cleanup(ctx, project, "task-2", wtPath); err != nil {
		t.Fatalf("double cleanup should be safe, got: %v", err)
	}
}

func TestWorktreeManager_ConcurrentCreatesSerialize(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	repo := initRepo(t, dir, "main")
	wm := NewWorktreeManager(zap.NewNop(), filepath.Join(dir, "wt"))
	project := &model.Project{ID: "cb", RepoPath: repo, BaseBranch: "main"}
	ctx := context.Background()

	const n = 5
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := wm.Create(ctx, project, "task-c-"+strconv.Itoa(i)); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent create failed: %v", err)
	}
	for i := 0; i < n; i++ {
		if !branchExists(t, repo, "pieqi/task-c-"+strconv.Itoa(i)) {
			t.Fatalf("branch pieqi/task-c-%d missing after concurrent creates", i)
		}
	}
}

func TestWorktreeManager_CreateBadBaseBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	repo := initRepo(t, dir, "main")
	wm := NewWorktreeManager(zap.NewNop(), filepath.Join(dir, "wt"))
	project := &model.Project{ID: "cb", RepoPath: repo, BaseBranch: "nonexistent"}
	ctx := context.Background()

	if _, err := wm.Create(ctx, project, "task-x"); err == nil {
		t.Fatal("Create with bad base branch should error")
	}
}

func branchExists(t *testing.T, repo, branch string) bool {
	t.Helper()
	cmd := exec.Command("git", "-C", repo, "rev-parse", "--verify", branch)
	return cmd.Run() == nil
}
