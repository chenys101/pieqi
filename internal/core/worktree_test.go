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
	project := &model.Project{ID: "cb", RepoPath: repo}

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
	project := &model.Project{ID: "cb", RepoPath: repo}
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
	project := &model.Project{ID: "cb", RepoPath: repo}
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

func TestWorktreeManager_CreateOnMasterRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	// 业务仓常见：默认分支是 master，没有 main。
	repo := initRepo(t, dir, "master")
	wm := NewWorktreeManager(zap.NewNop(), filepath.Join(dir, "wt"))
	project := &model.Project{ID: "cb", RepoPath: repo}
	ctx := context.Background()

	wtPath, err := wm.Create(ctx, project, "task-biz")
	if err != nil {
		t.Fatalf("Create on master-only repo failed: %v", err)
	}
	if _, err := os.Stat(wtPath); err != nil {
		t.Fatalf("worktree missing: %v", err)
	}
	got, err := resolveBaseRef(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if got != "master" {
		t.Fatalf("resolved base=%q, want master", got)
	}
}

func TestResolveBaseRef_OriginHEAD(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	// 本地检出 develop，但 origin/HEAD 指向 master —— 应以远端默认为准。
	repo := initRepo(t, dir, "master")
	mustGit(t, repo, "checkout", "-b", "develop")
	mustGit(t, repo, "remote", "add", "origin", repo)
	mustGit(t, repo, "update-ref", "refs/remotes/origin/master", "master")
	mustGit(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/master")

	ctx := context.Background()
	got, err := resolveBaseRef(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if got != "master" {
		t.Fatalf("got %q, want master (from origin/HEAD)", got)
	}
}

func TestResolveBaseRef_NoUsableRef(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	repo := filepath.Join(dir, "empty")
	if err := os.MkdirAll(repo, 0755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "init", "-b", "orphan-tmp")
	ctx := context.Background()

	if _, err := resolveBaseRef(ctx, repo); err == nil {
		t.Fatal("empty repo should fail to resolve base")
	}
}

func branchExists(t *testing.T, repo, branch string) bool {
	t.Helper()
	cmd := exec.Command("git", "-C", repo, "rev-parse", "--verify", branch)
	return cmd.Run() == nil
}
