package agent

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"pieqi/internal/config"
)

// fakeExeName 按平台给出可执行文件名（Windows 带 .exe）。
func fakeExeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// isolateHomeAndPath 把 HOME/USERPROFILE 指向 tmp，并清空 PATH，
// 使 exec.LookPath 必然失败、安装落点必然落在 tmp 下 —— 测试结果与
// 本机是否装了 qodercli、PATH 里有什么完全无关。
func isolateHomeAndPath(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PATH", "") // LookPath 无目录可查 → ErrNotFound
	t.Setenv("QODER_HOME", "")
	t.Setenv("PATHEXT", "") // 不依赖外部 PATHEXT
}

// TestResolveSpawnName_ExplicitPathUntouched 显式路径必须原样返回 ——
// 解析器只处理裸名，绝不改写用户写死的路径（否则会引入难以察觉的行为变化）。
func TestResolveSpawnName_ExplicitPathUntouched(t *testing.T) {
	for _, p := range []string{
		`C:\Users\x\.qoder\bin\qodercli\qodercli.exe`,
		`/usr/local/bin/qodercli`,
		`./scripts/qodercli`,
		`../bin/qodercli`,
		`sub/qodercli`,
	} {
		if got := resolveSpawnName(p); got != p {
			t.Fatalf("resolveSpawnName(%q) = %q, want unchanged", p, got)
		}
	}
}

// TestResolveSpawnName_FallbackVendorLayout 是本次改动的核心用例：
// LookPath 失败时，必须能按安装器落点找到 <home>/.qoder/bin/qodercli/qodercli(.exe)。
// 反向验证：删掉 resolveSpawnName 里的 spawnFallbackPaths 分支，本用例必 FAIL。
func TestResolveSpawnName_FallbackVendorLayout(t *testing.T) {
	home := t.TempDir()
	isolateHomeAndPath(t, home)

	want := filepath.Join(home, ".qoder", "bin", "qodercli", fakeExeName("qodercli"))
	if err := os.MkdirAll(filepath.Dir(want), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(want, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("write fake exe: %v", err)
	}

	if got := resolveSpawnName("qodercli"); got != want {
		t.Fatalf("resolveSpawnName(qodercli) = %q, want %q", got, want)
	}
}

// TestResolveSpawnName_QoderHomeEnv 环境变量声明的安装根同样要生效。
func TestResolveSpawnName_QoderHomeEnv(t *testing.T) {
	home := t.TempDir()
	isolateHomeAndPath(t, home)
	alt := t.TempDir()
	t.Setenv("QODER_HOME", alt)

	want := filepath.Join(alt, "bin", "qodercli", fakeExeName("qodercli"))
	if err := os.MkdirAll(filepath.Dir(want), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(want, []byte("x"), 0755); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := resolveSpawnName("qodercli"); got != want {
		t.Fatalf("QODER_HOME 未生效: got %q want %q", got, want)
	}
}

// TestResolveSpawnName_LookPathWins PATH 命中时优先用 PATH 的结果，
// 不去查安装落点（保持既有语义：配置的裸名先按系统 PATH 解析）。
func TestResolveSpawnName_LookPathWins(t *testing.T) {
	home := t.TempDir()
	isolateHomeAndPath(t, home)
	binDir := t.TempDir()
	t.Setenv("PATH", binDir)

	onPath := filepath.Join(binDir, fakeExeName("qodercli"))
	if err := os.WriteFile(onPath, []byte("x"), 0755); err != nil {
		t.Fatalf("write: %v", err)
	}
	// 同时在 home 下也放一份，证明 PATH 优先。
	other := filepath.Join(home, ".qoder", "bin", "qodercli", fakeExeName("qodercli"))
	if err := os.MkdirAll(filepath.Dir(other), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(other, []byte("x"), 0755); err != nil {
		t.Fatalf("write: %v", err)
	}

	if got := resolveSpawnName("qodercli"); got != onPath {
		t.Fatalf("PATH 命中应优先: got %q want %q", got, onPath)
	}
}

// TestResolveSpawnName_NotFoundReturnsBareName 都找不到时返回裸名本身，
// 让 exec 报出标准错误（调用方再补充候选落点提示）。
func TestResolveSpawnName_NotFoundReturnsBareName(t *testing.T) {
	isolateHomeAndPath(t, t.TempDir())
	if got := resolveSpawnName("qodercli"); got != "qodercli" {
		t.Fatalf("got %q, want bare name unchanged", got)
	}
	if got := resolveSpawnName(""); got != "" {
		t.Fatalf("empty name: got %q, want empty", got)
	}
}

// TestSpawnFallbackPaths_OnlyKnownVendors 未登记的命令名不应凭空产生候选
// （避免对任意命令都去猜路径）。同时确认 qodercli 的候选里含"同名子目录"那一层。
func TestSpawnFallbackPaths_OnlyKnownVendors(t *testing.T) {
	if got := spawnFallbackPaths("totally-unmapped-cmd"); len(got) != 0 {
		t.Fatalf("未登记命令不应有候选，got %v", got)
	}
	home := t.TempDir()
	isolateHomeAndPath(t, home)
	want := filepath.Join(home, ".qoder", "bin", "qodercli", fakeExeName("qodercli"))
	found := false
	for _, p := range spawnFallbackPaths("qodercli") {
		if p == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("候选缺少同名子目录落点 %q；实际 %v", want, spawnFallbackPaths("qodercli"))
	}
}

// TestNewACPAgent_ResolvesBinPathKeepsCmdName 构造期解析必须落在 binPath，
// 且 **cmdName 保持配置原样** —— CmdName() 是可观测契约（既有测试断言 npx）。
func TestNewACPAgent_ResolvesBinPathKeepsCmdName(t *testing.T) {
	home := t.TempDir()
	isolateHomeAndPath(t, home)

	want := filepath.Join(home, ".qoder", "bin", "qodercli", fakeExeName("qodercli"))
	if err := os.MkdirAll(filepath.Dir(want), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(want, []byte("x"), 0755); err != nil {
		t.Fatalf("write: %v", err)
	}

	a := NewACPAgent(config.ACPConfig{AgentType: "qodercli"}, nil)
	defer a.Close(context.Background())

	if a.CmdName() != "qodercli" {
		t.Fatalf("CmdName = %q, want qodercli（配置原样，不得被解析结果覆盖）", a.CmdName())
	}
	if a.BinPath() != want {
		t.Fatalf("BinPath = %q, want %q", a.BinPath(), want)
	}
	if len(a.CmdArgs()) == 0 || a.CmdArgs()[0] != "--acp" {
		t.Fatalf("CmdArgs = %v, want 以 --acp 开头", a.CmdArgs())
	}
}

// TestNewACPAgent_BinPathFallsBackToCmdName 解析不到时 BinPath == CmdName，
// 保证错误信息与既有行为一致（仍由 exec 报 not found）。
func TestNewACPAgent_BinPathFallsBackToCmdName(t *testing.T) {
	isolateHomeAndPath(t, t.TempDir())
	a := NewACPAgent(config.ACPConfig{AgentType: "qodercli"}, nil)
	defer a.Close(context.Background())
	if a.BinPath() != "qodercli" {
		t.Fatalf("BinPath = %q, want qodercli", a.BinPath())
	}
}
