package core

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"pieqi/internal/model"
)

// testTempBase 返回一个**可写、且在 pieqi 仓库之外**的临时目录根。
//
// 为什么需要：① `go test` 的工作目录是**包目录**（internal/core），
// 直接 MkdirTemp("build", …) 会在build/ 下再建一层而找不到；本函数从包目录
// 上溯到仓库根再用它。② 本机会话沙箱把 TMP 指进仓库，t.TempDir() 会落在
// pieqi 仓库内 ⇒ git 命令向上找到的是 pieqi 的 .git，用例前提被破坏。
func testTempBase(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Skipf("取工作目录失败：%v", err)
	}
	// internal/core → 仓库根
	root := filepath.Clean(filepath.Join(wd, "..", ".."))
	base := filepath.Join(root, "build")
	if _, err := os.Stat(base); err != nil {
		t.Skipf("仓库内 build/ 不可用：%v", err)
	}
	return base
}

// newGitRepo 建一个带初始 commit 的临时 git 仓库。
//
// ★为什么用 build/ 下的子目录而不是 t.TempDir()：本机会话沙箱把 TMP 指进仓库，
// t.TempDir() 会落在 pieqi 仓库内 —— git 命令向上找到的是**pieqi 的 .git**，
// 于是本仓库自己的脏文件被算进`git status`，「工作区干净」这个前提根本不成立
// （实测会让 TestGitStatusPorcelain_CleanRepoReturnsEmptyNotNil 假失败）。
// 在 build/ 下 `git init` 一个独立子仓库即可彻底隔离。
func newGitRepo(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(testTempBase(t), "pieqi-testgit-*")
	if err != nil {
		t.Skipf("无法建临时仓库（环境限制）：%v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
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

// cleanNestedGitRepo 在工作区内的 build/ 下建一个**独立的空 git 仓库**。
//
// 为什么这么绕：本机会话沙箱把 TMP 指进仓库、子进程只能写工作区内，于是
// `t.TempDir()` 落在 pieqi 仓库里 —— 对「干净仓库」用例来说它带着一堆脏文件，
// 对「非 git 目录」用例来说它本身就是 git 仓库，两个前提都不成立。
// `git init` 出来的子目录有自己的 .git ⇒ git 命令以它为准，与父仓库完全隔离。
func cleanNestedGitRepo(t *testing.T) string {
	t.Helper()
	base, err := os.MkdirTemp(testTempBase(t), "pieqi-testrepo-*")
	if err != nil {
		t.Skipf("无法在 build/ 下建临时仓库（环境限制）：%v", err)
	}
	cmd := exec.Command("git", "init", "-b", "main")
	cmd.Dir = base
	if out, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(base)
		t.Skipf("git init 失败（环境限制）：%v\n%s", err, out)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	if dirty := GitStatusPorcelain(base); dirty != nil && len(dirty) != 0 {
		t.Skipf("临时仓库不干净，用例前提不成立：%v", dirty)
	}
	if sha := GitHeadSHA(base); sha != "" {
		t.Skipf("临时仓库已有 HEAD（%s），用例前提不成立", sha)
	}
	return base
}

// TestGitStatusPorcelain_CleanRepoReturnsEmptyNotNil 是GitStatusPorcelain 的**契约测试**。
//
// 为什么必须有：返回 nil 与返回空切片在下游是**两个完全不同的意思**
// （nil = 非 git 仓库 → 走全量文件树兜底；空 = 干净的 git 仓库 → 不兜底）。
// 历史上这里用 `var paths []string` 声明，干净仓库（`git status --porcelain` 输出空串）
// 会因过滤掉空行而返回 nil ⇒ 干净的大仓库被误判成非 git 项目 ⇒
// 走 walkWorktreeFiles 遍历 8 万+文件再逐个拷贝 ⇒ **任务静默卡死在 pending**。
// 表现是「命令成功、无错误、日志无异常，就是什么都不发生」，极难定位。
func TestGitStatusPorcelain_CleanRepoReturnsEmptyNotNil(t *testing.T) {
	repo := newGitRepo(t) // 有初始 commit、工作区干净

	got := GitStatusPorcelain(repo)
	if got == nil {
		t.Fatal("干净仓库必须返回非 nil 空切片：nil 会被下游误判为「非 git 仓库」"+
			"并触发全量文件树兜底，这正是任务卡死在 pending 的根因")
	}
	if len(got) != 0 {
		t.Errorf("干净仓库应无脏文件，got %v", got)
	}

	// 反向对照：真有脏文件时长度必须 > 0（确保不是"永远返回空"）。
	_ = os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("x\n"), 0644)
	got2 := GitStatusPorcelain(repo)
	if len(got2) != 1 || got2[0] != "dirty.txt" {
		t.Errorf("脏文件应被检出，got %v", got2)
	}

	// 「无 HEAD 的干净仓库」也必须是非 nil 空切片（新建空仓库的真实形态）。
	fresh := cleanNestedGitRepo(t)
	if got3 := GitStatusPorcelain(fresh); got3 == nil {
		t.Error("无 HEAD 的干净仓库也必须返回非 nil 空切片，不能是 nil")
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
// TestCaptureBaseline_HugeTreeDoesNotHang 规模兜底：非 git 项目但文件数超限时，
// 必须在**有界时间**内返回，且只记路径不拷贝内容。
//
// 为什么需要这个上限：walkWorktreeFiles 只跳过固定几个噪音目录。在把 node_modules
// 直接放在仓库根的项目上（实测 D:\code\agent\deepseek-harness：8.4 万文件、
// node_modules 2.1 万），逐个 ReadFile+WriteFile 要跑几十分钟，
// 表现为**任务永远停在 pending、日志一行不多**——比报错更难排查。
// 基线快照是尽力而为的反馈数据，卡住任务是不可接受的失败模式。
func TestCaptureBaseline_HugeTreeDoesNotHang(t *testing.T) {
	// 非 git 目录 ⇒ 走全量遍历兜底。在 build/ 下建裸目录（不 git init）即是。
	// ★注意：GitHeadSHA/GitStatusPorcelain 会**向上**找到 pieqi 的 .git，
	// 所以这个裸目录在 git 眼里仍属 pieqi 仓库（工作区干净 ⇒ dirty 为空切片，
	// 不会走兜底分支）。要让兜底分支真正被执行，必须让 git 命令**失败**——
	// 这里靠 GIT_CEILING_DIRECTORIES 阻止向上查找，把该目录变成"无仓库"视角。
	wt, err := os.MkdirTemp(testTempBase(t), "pieqi-hugetree-*")
	if err != nil {
		t.Skipf("无法建临时目录（环境限制）：%v", err)
	}
	t.Cleanup(func() { os.RemoveAll(wt) })
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(wt))
	if sha := GitHeadSHA(wt); sha != "" {
		t.Skipf("无法构造非 git 目录视角（HEAD=%s），用例前提不成立", sha)
	}
	// 塞进超过 baselineMaxSnapshotFiles 个文件，验证上限真的生效且快速返回。
	// ★刻意**不进** node_modules/dist 等噪音目录：那些目录会被 walkWorktreeFiles
	// 直接跳过（这是 2026-10-09 那次事故里能走到 8.4 万文件的原因，
	// 说明真实项目的目录布局确实能绕过噪音过滤），所以要用一个不被跳过的路径。
	big := filepath.Join(wt, "src", "generated")
	if err := os.MkdirAll(big, 0755); err != nil {
		t.Fatal(err)
	}
	const n = baselineMaxSnapshotFiles + 50
	for i := 0; i < n; i++ {
		name := filepath.Join(big, fmt.Sprintf("f%05d.js", i))
		if err := os.WriteFile(name, []byte("x\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	fs := NewFeedbackStore(nil, filepath.Join(t.TempDir(), "checkpoints"))
	task := fsTask(t, wt, "task-huge", []model.TaskEvent{{Type: model.EventUser, Text: "t", Seq: 1}}, nil)

	done := make(chan *model.TaskBaseline, 1)
	go func() { done <- fs.CaptureBaseline(task) }()

	select {
	case b := <-done:
		if b == nil {
			t.Fatal("baseline not captured")
		}
		if b.HeadSHA != "" {
			t.Errorf("non-git head should be empty, got %q", b.HeadSHA)
		}
		// ★核心断言：路径数被截到上限，且**没有**把两 thousand 多个文件拷进 pre/。
		if len(b.DirtyPaths) != baselineMaxSnapshotFiles {
			t.Errorf("dirty paths = %d，want 截断到上限 %d", len(b.DirtyPaths), baselineMaxSnapshotFiles)
		}
		preDir := filepath.Join(fs.root, task.ID, "pre")
		if entries, err := os.ReadDir(preDir); err == nil && len(entries) > 0 {
			t.Errorf("超限时不该拷贝文件快照，pre/ 仍有 %d 个条目", len(entries))
		}
	case <-time.After(60 * time.Second):
		t.Fatal("CaptureBaseline 超过 60s 未返回 —— 规模兜底失效，任务会卡死在 pending")
	}
}

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
