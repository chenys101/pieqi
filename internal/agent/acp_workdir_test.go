package agent

import (
	"os"
	"path/filepath"
	"testing"

	"pieqi/internal/config"
)

// deadAgentCfg 一个 spawn 必然失败的配置：用来在**不真起进程**的前提下
// 走到 NewSession 的字段落定逻辑（workDir/taskID 都在 ensureStarted 之前赋值）。
func deadAgentCfg() config.ACPConfig {
	return config.ACPConfig{
		AgentType:    "dsh",
		SpawnCommand: []string{"definitely-not-a-real-binary-xyz"},
	}
}

// 子进程工作目录（cmd.Dir）的回归测试。
//
// 守的是什么 bug（2026-10-07）：dsh 的沙箱配置是
// `workspaceRoot: !!js process.cwd()` —— 它把**自己的 cwd** 当"工作区"，
// 区内写入免审、区外要审批。而 pieqi 自身跑在 `~/.pieqi/bin`（工作区之外），
// 不设 cmd.Dir 时 dsh 会继承那个 cwd，"工作区"就变成了 ~/.pieqi/bin
// ⇒ agent 在**项目目录**里 `go build` / `npm run build` / 写文件全被当成越界，
// 每次都要人工审批（实测本项目里构建前端就弹过）。
//
// 修法：把 cmd.Dir 设成任务的项目目录 ⇒ 项目内自由、项目外照旧审批。
// 多项目天然正确：每会话一个 ACPAgent，workDir 随任务走。

// TestACPWorkDir_ExistingDir 正常目录原样返回。
func TestACPWorkDir_ExistingDir(t *testing.T) {
	dir := t.TempDir()
	if got := acpWorkDir(dir); got != dir {
		t.Fatalf("acpWorkDir(%q) = %q，want 原样返回", dir, got)
	}
}

// TestACPWorkDir_BlankReturnsEmpty 空/空白 ⇒ 空串（调用方跳过 cmd.Dir）。
//
// 非任务场景（如标题生成）没有 Cwd，此时保持旧行为（继承 pieqi 的 cwd），
// 而不是让 exec 因为 Dir="" 报错。
func TestACPWorkDir_BlankReturnsEmpty(t *testing.T) {
	for _, in := range []string{"", "   ", "\t", "\n"} {
		if got := acpWorkDir(in); got != "" {
			t.Errorf("acpWorkDir(%q) = %q，want 空串", in, got)
		}
	}
}

// TestACPWorkDir_NonExistentReturnsEmpty ★ 不存在的目录必须不设。
//
// exec.Cmd 对不存在的 Dir 会在 Start 时报
// "chdir ...: no such file or directory" —— 那会让**整个会话起不来**。
// 所以宁可退回旧行为（继承 cwd），也不能把坏路径交给 exec。
func TestACPWorkDir_NonExistentReturnsEmpty(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does", "not", "exist")
	if got := acpWorkDir(missing); got != "" {
		t.Fatalf("acpWorkDir(不存在的目录) = %q，want 空串（否则 spawn 会失败）", got)
	}
}

// TestACPWorkDir_FileReturnsEmpty 指向文件（不是目录）也必须不设。
func TestACPWorkDir_FileReturnsEmpty(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := acpWorkDir(f); got != "" {
		t.Fatalf("acpWorkDir(文件) = %q，want 空串", got)
	}
}

// TestACPWorkDir_TrimsWhitespace 前后空白要修掉。
//
// 配置里偶尔会写成 " D:/code/x " 这种；带空白的路径在 Windows 上
// Stat 会失败 ⇒ 退化成"不设 Dir"（静默退回旧行为），不如这里修掉。
func TestACPWorkDir_TrimsWhitespace(t *testing.T) {
	dir := t.TempDir()
	if got := acpWorkDir("  " + dir + "  "); got != dir {
		t.Fatalf("acpWorkDir(带空白) = %q，want %q", got, dir)
	}
}

// TestNewSession_SetsWorkDirBeforeSpawn 会话创建时落定 workDir。
//
// 这是"工作区随任务走"的直接判据：不同任务 → 不同 Cwd → 不同 cmd.Dir。
// 本用例不 spawn 真实进程（Cwd 校验发生在 ensureStarted 之前的那一步之后，
// 用不存在的 spawn 命令让 Start 失败即可），只断言字段被正确落定。
func TestNewSession_SetsWorkDirBeforeSpawn(t *testing.T) {
	dir := t.TempDir()
	// 故意用不存在的命令：spawn 会失败，但 workDir 必须在失败前就已落定
	// （它要在 Start 里用，而 Start 发生在 ensureStarted 内）。
	a := NewACPAgent(deadAgentCfg(), nil)

	_, _ = a.NewSession(t.Context(), SessionConfig{Cwd: dir})

	if a.workDir != dir {
		t.Fatalf("workDir = %q，want %q（必须在 spawn 前落定，否则 cmd.Dir 设不上）",
			a.workDir, dir)
	}
}

// TestNewSession_EmptyCwdRejected Cwd 为空时不建会话（既有约束，顺带守住）。
func TestNewSession_EmptyCwdRejected(t *testing.T) {
	a := NewACPAgent(deadAgentCfg(), nil)
	if _, err := a.NewSession(t.Context(), SessionConfig{}); err == nil {
		t.Fatal("Cwd 为空应报错")
	}
}
