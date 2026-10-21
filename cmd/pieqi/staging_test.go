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

// --- 2026-10-21 事故：发版流程"已固化"，自重启却静默失效 ---
//
// 现场：:3000 上的实例以 cwd=~/.pieqi/bin 运行、PIEQI_CONFIG 落空，cfgPath 因此是
// 相对值 "config.yaml"。交付目录算成 <cwd>/build（= ~/.pieqi/bin/build，不存在），
// 自重启被关闭 —— `POST /api/admin/restart` 409 no staged binary、
// `POST /api/admin/restart-only` 501，而 build/pieqi.new.exe 好端端躺在仓库里。
// 处置当时只能把产物手动搬进 ~/.pieqi/bin 才让接口复活。所以这里钉两条不变式：
//   ① 相对 configPath 必须先钉成绝对路径，再参与交付目录推导；
//   ② 万一谁又把链路接回相对路径，resolveStagingDir 必须报"为空"而不是悄悄指到 cwd/build。

// TestAbsolutizeConfigPath 相对路径钉成绝对，绝对路径原样（含 Windows 反斜杠形态）。
func TestAbsolutizeConfigPath(t *testing.T) {
	cwd := func() (string, error) { return `D:\code\go\pieqi`, nil }

	got := absolutizeConfigPath("config.yaml", cwd)
	if want := filepath.Join(`D:\code\go\pieqi`, "config.yaml"); got != want {
		t.Fatalf("absolutizeConfigPath(相对)=%q, want %q", got, want)
	}
	abs := filepath.Join(`D:\code\go\pieqi`, "config.yaml")
	if got := absolutizeConfigPath(abs, cwd); got != abs {
		t.Fatalf("绝对路径不应被改写：%q", got)
	}
	if got := absolutizeConfigPath("", cwd); got != "" {
		t.Fatalf("空路径应原样返回，got %q", got)
	}
	// cwd 取不到：原样返回，由 resolveStagingDir 的告警兜底（绝不猜一个目录）。
	failing := func() (string, error) { return "", os.ErrPermission }
	if got := absolutizeConfigPath("config.yaml", failing); got != "config.yaml" {
		t.Fatalf("cwd 失败时应原样返回，got %q", got)
	}
}

// TestResolveStagingDir_RelativePathDoesNotLandInCwd 反向断言：修复前的相对路径
// 场景不能"看起来能用" —— 它推导出的 <cwd>/build 不存在时必须返回空串，
// 而不是把交付目录指向运行位下面的假目录（那正是让 staged 恒 false 的那条路）。
func TestResolveStagingDir_RelativePathDoesNotLandInCwd(t *testing.T) {
	// 造一个"运行位"目录（充当 cwd），里面**没有** build/。
	runDir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(runDir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(wd) }()

	if got := resolveStagingDir("config.yaml"); got != "" {
		t.Fatalf("resolveStagingDir(相对, cwd 无 build/)=%q, want \"\"", got)
	}

	// 钉成绝对路径后（修复的落点），指向的是真正带 build/ 的仓库，而不是 cwd。
	_, configPath, wantStage := stagingFixture(t)
	got := resolveStagingDir(absolutizeConfigPath(configPath, os.Getwd))
	if got != wantStage {
		t.Fatalf("resolveStagingDir(绝对)=%q, want %q", got, wantStage)
	}
	if got == filepath.Join(runDir, "build") {
		t.Fatalf("交付目录仍落在运行位 cwd 下：%q", got)
	}
}
