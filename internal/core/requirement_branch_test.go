package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"pieqi/internal/model"

	"go.uber.org/zap"
)

func TestIntegrationBranchName(t *testing.T) {
	got := IntegrationBranchName("0c2a7cb4-f118-43d2-8f07-e17de10ae426")
	if got != "req/0c2a7cb4" {
		t.Fatalf("got %q", got)
	}
}

func TestEnsureIntegrationBranch_CreatesFromMain(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := initRepo(t, testTempDir(t), "main")
	br := "req/testhook"
	if err := EnsureIntegrationBranch(context.Background(), repo, br); err != nil {
		t.Fatal(err)
	}
	if !gitRefExists(context.Background(), repo, br) {
		t.Fatal("branch not created")
	}
	if err := EnsureIntegrationBranch(context.Background(), repo, br); err != nil {
		t.Fatal(err)
	}
}

func TestFormatAndAppendRequirementContext(t *testing.T) {
	r := &model.Requirement{
		ID:                "abc",
		Title:             "demo",
		IntegrationBranch: "req/abcdef01",
		Acceptance:        []model.AcceptanceItem{{ID: "ac1", Text: "works", Verifiable: true}},
	}
	task := &model.Task{CoversAC: []string{"ac1"}}
	block := FormatRequirementExecContext(r, task)
	if !strings.Contains(block, reqContextMarker) || !strings.Contains(block, "req/abcdef01") {
		t.Fatalf("block=%s", block)
	}
	p := AppendRequirementContext("do stuff", block)
	if !strings.Contains(p, "do stuff") || !strings.Contains(p, "req/abcdef01") {
		t.Fatalf("prompt=%s", p)
	}
	if AppendRequirementContext(p, block) != p {
		t.Fatal("append should be idempotent")
	}
}

func TestWorktreeCreate_UsesBaseRef(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := testTempDir(t)
	repo := initRepo(t, dir, "main")
	if err := EnsureIntegrationBranch(context.Background(), repo, "req/baseuse"); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "checkout", "req/baseuse")
	if err := os.WriteFile(filepath.Join(repo, "from-req.txt"), []byte("on-req"), 0644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "add", "from-req.txt")
	mustGit(t, repo, "-c", "user.email=t@t.com", "-c", "user.name=t", "commit", "-m", "on req")
	mustGit(t, repo, "checkout", "main")

	wm := NewWorktreeManager(zap.NewNop(), filepath.Join(dir, "wts"))
	project := &model.Project{ID: "p", RepoPath: repo, BaseRef: "req/baseuse"}
	wt, err := wm.Create(context.Background(), project, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wt, "from-req.txt")); err != nil {
		t.Fatalf("worktree should include req-branch file: %v", err)
	}
}

func TestConfirm_SetsIntegrationBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ts := newTestTaskStore(t)
	rs := NewRequirementStore(ts.Backend())
	rs.SetTaskStore(ts)
	repo := initRepo(t, testTempDir(t), "main")
	req, err := rs.Create(&model.Requirement{
		Title: "feat", ProjectPath: repo,
		Acceptance: []model.AcceptanceItem{{ID: "ac1", Text: "works", Verifiable: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := rs.Confirm(req.ID, []model.ProposedTaskNode{
		{TempID: "be", Prompt: "impl", Role: model.RoleBackend, CoversAC: []string{"ac1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := IntegrationBranchName(req.ID)
	if out.Requirement.IntegrationBranch != want {
		t.Fatalf("branch=%q want %q", out.Requirement.IntegrationBranch, want)
	}
	if !gitRefExists(context.Background(), repo, want) {
		t.Fatal("git branch missing")
	}
}

func TestEnsureRequirementContext_OnRunner(t *testing.T) {
	ts := newTestTaskStore(t)
	rs := NewRequirementStore(ts.Backend())
	rs.SetTaskStore(ts)
	req, err := rs.Create(&model.Requirement{
		Title: "feat", ProjectPath: t.TempDir(),
		IntegrationBranch: "req/deadbeef",
		Acceptance:        []model.AcceptanceItem{{ID: "ac1", Text: "x", Verifiable: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := ts.Create(&model.Task{
		Source: model.SourceHTTP, Status: model.TaskPending,
		RequirementID: req.ID, ProjectPath: req.ProjectPath,
		Prompt: "impl only", Role: model.RoleBackend, CoversAC: []string{"ac1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	tr := &TaskRunner{store: ts, requirements: rs}
	got := tr.ensureRequirementContext(task)
	if !strings.Contains(got.Prompt, "req/deadbeef") || !strings.Contains(got.Prompt, reqContextMarker) {
		t.Fatalf("prompt=%s", got.Prompt)
	}
	got2 := tr.ensureRequirementContext(got)
	if got2.Prompt != got.Prompt {
		t.Fatal("idempotent")
	}
}

// testTempDir 在仓库 build/ 下建临时目录（沙箱写不了系统 Temp）。
func testTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(testTempBase(t), "pieqi-req-*")
	if err != nil {
		t.Skipf("无法建临时目录：%v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// gitProjectPath 给 Confirm 测试用的已 init 仓库路径。
func gitProjectPath(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	return initRepo(t, testTempDir(t), "main")
}
