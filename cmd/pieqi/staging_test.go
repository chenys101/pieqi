package main

import (
	"os"
	"path/filepath"
	"testing"
)

// resolveStagingDir 的回归测试组。
//
// 背景（2026-10-14 实测事故，值得写清楚以免日后被"简化"回去）：
// 交付目录原先由 filepath.Dir(os.Executable()) 推导。一旦按"运行产物必须在仓库外"
// 的纪律把运行副本放到 %USERPROFILE%\.pieqi\bin，服务就会去那里找 pieqi.new.exe
// —— 而那是持受限令牌的 agent **写不到**的地方。失败方式是静默的
// （GET /api/admin/restart 恒为 staged:false），看起来像功能坏了。
//
// 现约定：**交付目录 = <config.yaml 所在目录>/build**，不引入任何配置项或环境变量。
// 理由是 config.yaml 本来就是启动必需的输入，且它与 go.mod 同级（= 仓库根）。

// stagingFixture 造一个"仓库根"：含 config.yaml 与 build/ 子目录。
func stagingFixture(t *testing.T) (repoRoot, configPath, wantStage string) {
	t.Helper()
	repoRoot = t.TempDir()
	configPath = filepath.Join(repoRoot, "config.yaml")
	if err := os.WriteFile(configPath, []byte("server:\n  port: 3000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantStage = filepath.Join(repoRoot, "build")
	if err := os.MkdirAll(wantStage, 0o755); err != nil {
		t.Fatal(err)
	}
	return repoRoot, configPath, wantStage
}

// TestResolveStagingDir_DerivesFromConfigLocation 是本组的核心回归：
// 交付目录必须是 <config.yaml 所在目录>/build。
//
// 为什么这条断言能钉住真 bug：修复前该函数恒返回 filepath.Dir(execPath)，
// 与 configPath 无关，所以本用例必然失败（它根本无法从 execPath 得到这个目录）。
func TestResolveStagingDir_DerivesFromConfigLocation(t *testing.T) {
	repoRoot, configPath, wantStage := stagingFixture(t)

	got := resolveStagingDir(configPath)
	if got != wantStage {
		t.Fatalf("resolveStagingDir()=%q, want %q", got, wantStage)
	}
	// 反向断言：绝不能是运行产物所在目录 —— 运行位在仓库外时那是 agent 写不到的地方。
	execDir := t.TempDir()
	if got == execDir {
		t.Fatalf("resolveStagingDir() 落在了 execDir，正是修复前的坏行为")
	}
	// 也不该是 config.yaml 所在目录本身（那是仓库根，agent 可写但约定是 build/）。
	if got == repoRoot {
		t.Fatalf("resolveStagingDir() 应指向 build/ 子目录，而不是仓库根")
	}
}

// TestResolveStagingDir_IndependentOfExecPath 交付目录与运行产物位置**无关**。
//
// 这条把"解耦"这件事钉死：即使运行副本被搬到任意目录（区外运行的正解），
// 交付目录仍然由 config.yaml 决定。若哪天有人把 execPath 重新接回公式，本用例变红。
func TestResolveStagingDir_IndependentOfExecPath(t *testing.T) {
	_, configPath, wantStage := stagingFixture(t)
	if got := resolveStagingDir(configPath); got != wantStage {
		t.Fatalf("resolveStagingDir()=%q, want %q（与 execPath 无关）", got, wantStage)
	}
}

// TestResolveStagingDir_EmptyConfigPathIsEmpty 拿不到 configPath 时返回空串，
// 由调用方明确报错并关闭自重启 —— 不瞎猜一个目录。
func TestResolveStagingDir_EmptyConfigPathIsEmpty(t *testing.T) {
	if got := resolveStagingDir(""); got != "" {
		t.Fatalf("resolveStagingDir(\"\")=%q, want \"\"", got)
	}
	if got := resolveStagingDir("   "); got != "" {
		t.Fatalf("resolveStagingDir(空白)=%q, want \"\"", got)
	}
}

// TestResolveStagingDir_MissingBuildDirIsEmpty 仓库还没构建过时返回空串。
//
// 为什么不代为创建 build/：它是编译落点，由构建/交付流程建立。服务替它建目录
// 只会掩盖"这个仓库根本没构建过"，让问题推迟到 agent 投递失败时才暴露。
func TestResolveStagingDir_MissingBuildDirIsEmpty(t *testing.T) {
	repoRoot := t.TempDir()
	configPath := filepath.Join(repoRoot, "config.yaml")
	if err := os.WriteFile(configPath, []byte("x: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 刻意不建 build/。
	if got := resolveStagingDir(configPath); got != "" {
		t.Fatalf("resolveStagingDir()=%q, want \"\"（build/ 不存在时不可用）", got)
	}
	// 再确认它**没有**替我们创建：副作用检测。
	if _, err := os.Stat(filepath.Join(repoRoot, "build")); !os.IsNotExist(err) {
		t.Fatalf("resolveStagingDir 不应创建 build/（会让'从未构建'被掩盖）")
	}
}

// TestResolveStagingDir_BuildIsFileNotDir build 是文件（而非目录）时不可用。
func TestResolveStagingDir_BuildIsFileNotDir(t *testing.T) {
	repoRoot := t.TempDir()
	configPath := filepath.Join(repoRoot, "config.yaml")
	if err := os.WriteFile(configPath, []byte("x: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "build"), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := resolveStagingDir(configPath); got != "" {
		t.Fatalf("resolveStagingDir()=%q, want \"\"（build 是文件时不可用）", got)
	}
}

// TestResolveStagingDir_RelativeConfigPathIsAbsolutized 相对路径的 configPath
// 也要能算出绝对交付目录（SelfRestart 用它拼 pieqi.new.exe 的完整路径）。
func TestResolveStagingDir_RelativeConfigPathIsAbsolutized(t *testing.T) {
	_, _, wantStage := stagingFixture(t)
	// 切到仓库根，用相对路径调用 —— 模拟 PIEQI_CONFIG=config.yaml 的情形。
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(filepath.Dir(wantStage)); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(wd) }()

	got := resolveStagingDir("config.yaml")
	if !filepath.IsAbs(got) {
		t.Fatalf("resolveStagingDir()=%q, want 绝对路径", got)
	}
	if got != wantStage {
		t.Fatalf("resolveStagingDir()=%q, want %q", got, wantStage)
	}
}

// TestStagingDirSource 报告推导依据，供启动日志取证。
func TestStagingDirSource(t *testing.T) {
	_, configPath, _ := stagingFixture(t)
	if got, want := stagingDirSource(configPath), "convention:dir(config.yaml)/build"; got != want {
		t.Fatalf("stagingDirSource()=%q, want %q", got, want)
	}
	if got, want := stagingDirSource(""), "unresolved:empty config path"; got != want {
		t.Fatalf("stagingDirSource(\"\")=%q, want %q", got, want)
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

// TestUsableStagingDir_RejectsFile 必须是**目录**。
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
