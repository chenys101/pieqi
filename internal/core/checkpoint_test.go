package core

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"pieqi/internal/model"
)

// newGitRepo 建一个带初始 commit 的临时 git 仓库。
func newGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	_ = os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("head version\n"), 0644)
	run("add", ".")
	run("commit", "-m", "init")
	return dir
}

// fsTask 构造带事件流的 task（指定 Turn 的工具改动）。
func fsTask(t *testing.T, worktree, id string, events []model.TaskEvent, baseline *model.TaskBaseline) *model.Task {
	return &model.Task{
		ID: id, ProjectPath: worktree, WorktreePath: worktree,
		Events: events, Baseline: baseline,
	}
}

func TestFeedbackStore_BaselineAndRewind(t *testing.T) {
	repo := newGitRepo(t)
	fs := NewFeedbackStore(nil, filepath.Join(t.TempDir(), "checkpoints"))

	// 用户起始 dirty 文件（不在 HEAD）+ 已跟踪文件将被 Agent 修改
	_ = os.WriteFile(filepath.Join(repo, "user-dirty.txt"), []byte("user dirty\n"), 0644)
	_ = os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("untracked\n"), 0644)

	events := []model.TaskEvent{
		{Type: model.EventUser, Text: "turn1", Seq: 1},
		toolUseEvent("Edit", "t1", "tracked.txt"),
		{Type: model.EventToolResult, ToolUseID: "t1", ToolName: "Edit"},
		toolUseEvent("Write", "t2", "agent-new.txt"),
		{Type: model.EventToolResult, ToolUseID: "t2", ToolName: "Write"},
		{Type: model.EventUser, Text: "turn2", Seq: 6},
		toolUseEvent("Edit", "t3", "agent-new.txt"),
		{Type: model.EventToolResult, ToolUseID: "t3", ToolName: "Edit"},
		toolUseEvent("Delete", "t4", "tracked.txt"),
		{Type: model.EventToolResult, ToolUseID: "t4", ToolName: "Delete"},
	}

	// 1. baseline 捕获（Agent 未动工前）
	task := fsTask(t, repo, "task-1", events, nil)
	baseline := fs.CaptureBaseline(task)
	if baseline == nil {
		t.Fatal("baseline not captured")
	}
	if baseline.HeadSHA == "" {
		t.Error("head sha empty")
	}
	// dirty = user-dirty + untracked（tracked.txt 尚未改）
	if len(baseline.DirtyPaths) != 2 {
		t.Errorf("dirty paths = %v", baseline.DirtyPaths)
	}

	// 2. Turn 1 执行：修改 tracked.txt、新建 agent-new.txt
	_ = os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("agent modified\n"), 0644)
	_ = os.WriteFile(filepath.Join(repo, "agent-new.txt"), []byte("v1\n"), 0644)
	fs.CaptureTurnEnd(task, 1)

	// 3. Turn 2 执行：改 agent-new.txt、删除 tracked.txt
	_ = os.WriteFile(filepath.Join(repo, "agent-new.txt"), []byte("v2\n"), 0644)
	_ = os.Remove(filepath.Join(repo, "tracked.txt"))
	fs.CaptureTurnEnd(task, 2)

	// 快照登记
	cps := fs.Checkpoints("task-1")
	if len(cps) != 2 || cps[0] != 1 || cps[1] != 2 {
		t.Fatalf("checkpoints = %v", cps)
	}

	// 4. Rewind 回 Turn 2 之前：tracked.txt 恢复（turn1 快照）、agent-new.txt 回 v1
	res, err := fs.RewindToTurn(task, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Restored) != 2 {
		t.Fatalf("restored = %v", res.Restored)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "tracked.txt")); string(b) != "agent modified\n" {
		t.Errorf("tracked.txt should be turn1 state, got %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "agent-new.txt")); string(b) != "v1\n" {
		t.Errorf("agent-new.txt should be v1, got %q", b)
	}

	// 5. Rewind 回 Turn 1 之前（Task 起始）：
	//    tracked.txt 回 HEAD 版本；agent-new.txt 起始不存在 → 删除；用户 dirty 不受影响
	res, err = fs.RewindToTurn(task, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, "agent-new.txt")); !os.IsNotExist(err) {
		t.Error("agent-new.txt should be removed")
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "tracked.txt")); string(b) != "head version\n" {
		t.Errorf("tracked.txt should be HEAD version, got %q", b)
	}
	// 用户 dirty / untracked 文件未被 Rewind 触碰
	if b, _ := os.ReadFile(filepath.Join(repo, "user-dirty.txt")); string(b) != "user dirty\n" {
		t.Error("user dirty file was touched by rewind")
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "untracked.txt")); string(b) != "untracked\n" {
		t.Error("untracked file was touched by rewind")
	}
	_ = res
}

// TestRewindFileToTurn 文件级回退（p2-design.md §7）：单文件恢复，不影响其他文件。
func TestRewindFileToTurn(t *testing.T) {
	repo := newGitRepo(t)
	fs := NewFeedbackStore(nil, filepath.Join(t.TempDir(), "checkpoints"))

	events := []model.TaskEvent{
		{Type: model.EventUser, Text: "turn1", Seq: 1},
		toolUseEvent("Write", "t1", "a.txt"),
		{Type: model.EventToolResult, ToolUseID: "t1", ToolName: "Write"},
		toolUseEvent("Write", "t2", "b.txt"),
		{Type: model.EventToolResult, ToolUseID: "t2", ToolName: "Write"},
		{Type: model.EventUser, Text: "turn2", Seq: 6},
		toolUseEvent("Edit", "t3", "a.txt"),
		{Type: model.EventToolResult, ToolUseID: "t3", ToolName: "Edit"},
	}
	task := fsTask(t, repo, "task-file", events, nil)
	fs.CaptureBaseline(task)

	// Turn 1：建 a.txt=v1、b.txt=v1；Turn 2：a.txt=v2
	_ = os.WriteFile(filepath.Join(repo, "a.txt"), []byte("v1\n"), 0644)
	_ = os.WriteFile(filepath.Join(repo, "b.txt"), []byte("v1\n"), 0644)
	fs.CaptureTurnEnd(task, 1)
	_ = os.WriteFile(filepath.Join(repo, "a.txt"), []byte("v2\n"), 0644)
	fs.CaptureTurnEnd(task, 2)

	// 文件级回退 a.txt → Turn 2 之前：a.txt=v1，b.txt 不受影响
	res, err := fs.RewindFileToTurn(task, 2, "a.txt")
	if err != nil {
		t.Fatalf("rewind file: %v", err)
	}
	if len(res.Restored) != 1 || res.Restored[0] != "a.txt" {
		t.Fatalf("restored = %v", res.Restored)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "a.txt")); string(b) != "v1\n" {
		t.Errorf("a.txt = %q, want v1", b)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "b.txt")); string(b) != "v1\n" {
		t.Errorf("b.txt must be untouched, got %q", b)
	}

	// 未在目标 Turn 触碰的文件 → 拒绝（b.txt 无 Turn>=2 改动）
	if _, err := fs.RewindFileToTurn(task, 2, "b.txt"); err == nil {
		t.Fatal("file not touched at/after turn must be rejected")
	}
	// Agent 从未触碰的文件 → 拒绝
	if _, err := fs.RewindFileToTurn(task, 1, "tracked.txt"); err == nil {
		t.Fatal("never-touched file must be rejected")
	}
}

func TestFeedbackStore_CaptureTurnEndRecordsDeletion(t *testing.T) {
	repo := newGitRepo(t)
	root := filepath.Join(t.TempDir(), "checkpoints")
	fs := NewFeedbackStore(nil, root)

	// a.txt / b.txt 需先进 HEAD，Rewind 才能从 HEAD 恢复
	_ = os.WriteFile(filepath.Join(repo, "a.txt"), []byte("x\n"), 0644)
	_ = os.WriteFile(filepath.Join(repo, "b.txt"), []byte("y\n"), 0644)
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("add", ".")
	run("commit", "-m", "add ab")
	events := []model.TaskEvent{
		{Type: model.EventUser, Text: "go", Seq: 1},
		toolUseEvent("Delete", "d1", "a.txt"),
		{Type: model.EventToolResult, ToolUseID: "d1", ToolName: "Delete"},
	}
	task := fsTask(t, repo, "task-2", events, nil)
	_ = os.MkdirAll(filepath.Join(root, "task-2", "pre"), 0755) // baseline 视为已捕获

	_ = os.Remove(filepath.Join(repo, "a.txt"))
	fs.CaptureTurnEnd(task, 1)

	// manifest 记录了 a.txt
	data, err := os.ReadFile(filepath.Join(root, "task-2", "turn1", deletedManifestName))
	if err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
	var deleted []string
	_ = json.Unmarshal(data, &deleted)
	if len(deleted) != 1 || deleted[0] != "a.txt" {
		t.Errorf("manifest = %v", deleted)
	}

	// Rewind 回 Turn 1 前：a.txt 从 pre/…pre 无 → HEAD 恢复
	if _, err := fs.RewindToTurn(task, 1); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(repo, "a.txt")); err != nil || string(b) != "x\n" {
		t.Errorf("a.txt should be restored from HEAD, got err=%v content=%q", err, b)
	}
}

func TestSafeJoin_BlocksTraversal(t *testing.T) {
	if got := safeJoin("/root", "../etc/passwd"); got != filepath.Join("/root", "invalid") {
		t.Errorf("traversal not blocked: %s", got)
	}
	if got := safeJoin("/root", "a/b.txt"); got != filepath.Join("/root", "a", "b.txt") {
		t.Errorf("normal join broken: %s", got)
	}
}

// TestCaptureBaseline_NonGitWorktree 非 git 项目的基线兜底：git status 失败时
// 全量快照文件树（跳过 node_modules/dist），保证 before 状态可知、diff 不被夸大成整文件。
func TestCaptureBaseline_NonGitWorktree(t *testing.T) {
	wt := t.TempDir() // 普通目录，非 git 仓库
	for _, dir := range []string{"src", "node_modules", "dist"} {
		if err := os.MkdirAll(filepath.Join(wt, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(wt, "src", "App.vue"), []byte("v1 line\nv2 line\n"), 0644)
	_ = os.WriteFile(filepath.Join(wt, "node_modules", "junk.js"), []byte("noise\n"), 0644)
	_ = os.WriteFile(filepath.Join(wt, "dist", "bundle.js"), []byte("noise\n"), 0644)

	fs := NewFeedbackStore(nil, filepath.Join(t.TempDir(), "checkpoints"))
	events := []model.TaskEvent{
		{Type: model.EventUser, Text: "turn1", Seq: 1},
		toolUseEvent("Edit", "t1", "src/App.vue"),
		{Type: model.EventToolResult, ToolUseID: "t1", ToolName: "Edit"},
	}
	task := fsTask(t, wt, "task-ng", events, nil)

	baseline := fs.CaptureBaseline(task)
	if baseline == nil {
		t.Fatal("baseline not captured")
	}
	if baseline.HeadSHA != "" {
		t.Errorf("non-git head should be empty, got %q", baseline.HeadSHA)
	}
	// 全量快照覆盖 src/App.vue，且跳过噪音目录
	if len(baseline.DirtyPaths) != 1 || baseline.DirtyPaths[0] != "src/App.vue" {
		t.Errorf("dirty paths = %v, want [src/App.vue]", baseline.DirtyPaths)
	}

	// Turn 1 修改文件后，before 状态应从 pre/ 取到 v1 → 差分可得真实改动
	_ = os.WriteFile(filepath.Join(wt, "src", "App.vue"), []byte("v1 line\nCHANGED\n"), 0644)
	fs.CaptureTurnEnd(task, 1)
	before, ok := fs.AssembleBefore(task, 1, "src/App.vue")
	if !ok || string(before) != "v1 line\nv2 line\n" {
		t.Errorf("AssembleBefore = (%q,%v), want v1 state", before, ok)
	}
	_, add, del := UnifiedDiff("src/App.vue", string(before), "v1 line\nCHANGED\n", 3)
	if add != 1 || del != 1 {
		t.Errorf("diff = +%d/-%d, want +1/-1", add, del)
	}
}
