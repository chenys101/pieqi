package main

import (
	"os"
	"path/filepath"
	"testing"
)

// resolveStagingDir 的回归测试组。
//
// 背景（2026-10-14 实测事故，值得写清楚以免日后被"简化"回去）：
// restart.bat 早就设了 PIEQI_STAGING_DIR=%REPO%build，但 resolveStagingDir 只从
// os.Executable() 推导，**完全不读这个变量**。后果是自迭代闭环静默断裂：运行副本
// 已按"仓库外运行"的纪律搬到 %USERPROFILE%\.pieqi\bin，于是服务去那里找
// pieqi.new.exe —— 而那是持受限令牌的 agent **写不到**的地方。失败方式是静默的
// （GET /api/admin/restart 恒为 staged:false），看起来像功能坏了。

// TestResolveStagingDir_PrefersEnvOverExecutable 是本组的核心回归：
// 配了 PIEQI_STAGING_DIR 时，**必须**用它，而不是运行产物所在目录。
//
// 为什么这条断言能钉住真 bug：修复前该函数恒返回 filepath.Dir(execPath)，
// 与环境变量无关，所以本用例必然失败。
func TestResolveStagingDir_PrefersEnvOverExecutable(t *testing.T) {
	stage := t.TempDir()
	execDir := t.TempDir()
	execPath := filepath.Join(execDir, "pieqi.exe")

	t.Setenv(stagingDirEnv, stage)

	got := resolveStagingDir(execPath)
	if got != stage {
		t.Fatalf("resolveStagingDir()=%q, want %q（配了 env 就必须用 env）", got, stage)
	}
	// 反向断言：绝不能落在运行产物旁边 —— 那正是"运行位在仓库外"时的错误答案。
	if got == filepath.Dir(execPath) {
		t.Fatalf("resolveStagingDir()=%q 退回了 dir(execPath)：这正是修复前的坏行为", got)
	}
}

// TestResolveStagingDir_FallsBackToExecDir 未配 env 时退回运行产物目录，
// 保证"单机开发、脚本没注入变量"的场景仍可用（行为与修复前一致）。
func TestResolveStagingDir_FallsBackToExecDir(t *testing.T) {
	execDir := t.TempDir()
	execPath := filepath.Join(execDir, "pieqi.exe")

	t.Setenv(stagingDirEnv, "")

	if got, want := resolveStagingDir(execPath), filepath.Dir(execPath); got != want {
		t.Fatalf("resolveStagingDir()=%q, want %q", got, want)
	}
}

// TestResolveStagingDir_EmptyExecPathIsEmpty 两道来源都不可用时返回空串，
// 由调用方明确关闭自重启 —— 绝不瞎猜一个目录。
func TestResolveStagingDir_EmptyExecPathIsEmpty(t *testing.T) {
	t.Setenv(stagingDirEnv, "")
	if got := resolveStagingDir(""); got != "" {
		t.Fatalf("resolveStagingDir(\"\")=%q, want \"\"", got)
	}
}

// TestResolveStagingDir_EnvUnusableDoesNotSilentlyFallback 是本组的第二条关键断言：
// **配了但不可用**时返回空串，而**不是**悄悄退回 dir(execPath)。
//
// 为什么这一点值得单独钉死：退回 dir(execPath) 是"看起来还能跑"的假象 —— 服务会
// 报告一个可用的交付目录，而那个目录在正确部署下（运行位在仓库外）恰恰是 agent
// 写不到的地方。宁可功能明确关闭并在启动日志里报错，也不要给一个永远成功的假象。
func TestResolveStagingDir_EnvUnusableDoesNotSilentlyFallback(t *testing.T) {
	execDir := t.TempDir()
	execPath := filepath.Join(execDir, "pieqi.exe")

	missing := filepath.Join(t.TempDir(), "does-not-exist")
	t.Setenv(stagingDirEnv, missing)

	if got := resolveStagingDir(execPath); got != "" {
		t.Fatalf("resolveStagingDir()=%q, want \"\"（env 指向不存在的目录时必须报错而非退回）", got)
	}
}

// TestUsableStagingDir_RejectsFile 交付目录必须是**目录**：指向普通文件（误配成
// pieqi.new.exe 本身）同样不可用。
func TestUsableStagingDir_RejectsFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := usableStagingDir(file); got != "" {
		t.Fatalf("usableStagingDir(file)=%q, want \"\"", got)
	}
}

// TestUsableStagingDir_AcceptsRealDir 正常目录被接受，且返回绝对路径。
func TestUsableStagingDir_AcceptsRealDir(t *testing.T) {
	dir := t.TempDir()
	got := usableStagingDir(dir)
	if got == "" {
		t.Fatalf("usableStagingDir(%q) 返回空，want 非空", dir)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("usableStagingDir()=%q, want 绝对路径", got)
	}
}

// TestUsableStagingDir_RejectsSymlink 符号链接/junction 一律拒绝。
//
// 理由（与沙箱机制同源）：沙箱按**解析后的最终路径**判定写权限，经链接绕道会把
// 交付物写到预期之外的位置，并连带丢掉完整性标签语义。所以交付目录必须是它自己。
//
// Windows 上创建符号链接需要权限（或开发者模式）；拿不到权限时跳过，而不是让
// 用例假失败 —— 跳过的代价是这条判据在无权限环境下未被覆盖，可接受。
func TestUsableStagingDir_RejectsSymlink(t *testing.T) {
	real := t.TempDir()
	linkParent := t.TempDir()
	link := filepath.Join(linkParent, "link-to-stage")

	if err := os.Symlink(real, link); err != nil {
		t.Skipf("无法创建符号链接（权限不足，属预期）：%v", err)
	}

	if got := usableStagingDir(link); got != "" {
		t.Fatalf("usableStagingDir(symlink)=%q, want \"\"（链接必须被拒绝）", got)
	}
}

// TestStagingDirSource 报告来源，供启动日志取证（"服务到底在看哪个目录、依据是什么"）。
func TestStagingDirSource(t *testing.T) {
	t.Setenv(stagingDirEnv, `C:\some\stage`)
	if got, want := stagingDirSource(), "env:"+stagingDirEnv; got != want {
		t.Fatalf("stagingDirSource()=%q, want %q", got, want)
	}

	t.Setenv(stagingDirEnv, "")
	if got, want := stagingDirSource(), "fallback:dir(executable)"; got != want {
		t.Fatalf("stagingDirSource()=%q, want %q", got, want)
	}
}
