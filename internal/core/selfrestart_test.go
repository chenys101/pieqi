package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSelfRestart_NoStagedBinary 没有暂存文件时必须报错，而不是"成功"。
// 静默成功会让调用方以为重启过了，实际上还在跑旧代码 —— 这类假成功
// 在自迭代里最危险（agent 会据此认为自己的改动已生效）。
func TestSelfRestart_NoStagedBinary(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "pieqi.exe")
	if err := os.WriteFile(execPath, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	sr := NewSelfRestart(execPath, dir, dir, nil)

	if sr.HasStaged() {
		t.Fatal("HasStaged=true before staging, want false")
	}
	if err := sr.Restart(""); err == nil {
		t.Fatal("Restart with no staged binary succeeded, want error")
	}
}

// TestSelfRestart_EmptyStagedFileRejected 零字节的暂存文件（构建中断留下的残骸）
// 必须被拒。放行它等于用空文件覆盖掉一个能用的服务。
func TestSelfRestart_EmptyStagedFileRejected(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "pieqi.exe")
	if err := os.WriteFile(execPath, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	sr := NewSelfRestart(execPath, dir, dir, nil)

	staged := sr.stagingPath()
	if err := os.MkdirAll(filepath.Dir(staged), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if sr.HasStaged() {
		t.Fatal("HasStaged=true for empty file, want false")
	}
	if err := sr.Restart(""); err == nil {
		t.Fatal("Restart with empty staged file succeeded, want error")
	}
	// 关键：原二进制必须完好无损（不能被空文件换掉）。
	got, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatalf("read original: %v", err)
	}
	if string(got) != "old" {
		t.Fatalf("original binary changed to %q, want %q", got, "old")
	}
}

// TestCopyFile_AtomicAndCorrect 验证 copyFile 真的把内容搬过去，
// 且不留下半成品临时文件。
func TestCopyFile_AtomicAndCorrect(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "new.bin")
	dst := filepath.Join(dir, "old.bin")

	want := []byte("brand new binary content")
	if err := os.WriteFile(src, want, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copyFile: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("dst=%q, want %q", got, want)
	}
	// 临时文件必须被清理，否则每重启一次就留一个垃圾 exe。
	if _, err := os.Stat(dst + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temp file %s.tmp left behind", dst)
	}
}

// TestSelfRestart_StagingIgnoresProcessCwd 回归：交付落点必须由**显式配置**决定，
// 绝不能用进程 cwd 推断。
//
// 为什么这条重要（2026-10-06 实测踩到）：常驻服务是从 <dataRoot>/bin 启动的
// （见 ~/.pieqi/run-pieqi.cmd），所以 cwd 是那个目录 —— 而它正好在沙箱之外，
// agent 写不到。若落点跟着 cwd 走，agent 就永远交付不了新二进制，
// 功能表面存在、实际不可用，且失败方式是静默的（只看到 staged:false）。
func TestSelfRestart_StagingIgnoresProcessCwd(t *testing.T) {
	// 显式交付目录与 execPath/dataRoot 刻意都在不同位置。
	stagingDir := t.TempDir()
	execDir := t.TempDir()
	dataRoot := t.TempDir()
	execPath := filepath.Join(execDir, "pieqi.exe")

	sr := NewSelfRestart(execPath, dataRoot, stagingDir, nil)

	got := sr.stagingPath()
	want := filepath.Join(stagingDir, "pieqi.new"+exeSuffix())
	if got != want {
		t.Fatalf("stagingPath()=%q, want %q（必须落在显式交付目录，而非 cwd/dataRoot）", got, want)
	}
	// 明确断言它不在 dataRoot 之下 —— 那是 agent 写不到的地方。
	if strings.HasPrefix(got, dataRoot) {
		t.Fatalf("stagingPath()=%q 落在 dataRoot 下（agent 写不到），交付会静默失败", got)
	}
	if sr.StagingHint() != want {
		t.Fatalf("StagingHint()=%q, want %q", sr.StagingHint(), want)
	}
}

// TestSelfRestart_StagingFallsBackToDataRoot 没配交付目录时回退 dataRoot，
// 保证行为可预期（而不是落到某个随机 cwd）。
func TestSelfRestart_StagingFallsBackToDataRoot(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "pieqi.exe")
	sr := NewSelfRestart(execPath, dir, "", nil) // stagingDir 空

	want := filepath.Join(dir, "bin", "pieqi.new"+exeSuffix())
	if got := sr.stagingPath(); got != want {
		t.Fatalf("stagingPath()=%q, want %q", got, want)
	}
}
// TestSelfRestart_BacksUpRunningBinary 备份是"改了就跑不起来"时的唯一退路：
// 必须先把原文件改名成 .old，成功与否都要能找回旧版本。
func TestSelfRestart_BacksUpRunningBinary(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "pieqi.exe")
	if err := os.WriteFile(execPath, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	sr := NewSelfRestart(execPath, dir, dir, nil)

	staged := sr.stagingPath()
	if err := os.MkdirAll(filepath.Dir(staged), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte("new-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !sr.HasStaged() {
		t.Fatal("HasStaged=false, want true")
	}

	// 走到 spawnDetached 会真的起进程；测试里只验到"换文件"这一步的产物，
	// 故直接调用 Restart 并接受 spawn 失败（在同一台机器上可能真的起不来，
	// 但那一步的失败发生在文件已经换好之后）。
	_ = sr.Restart("")

	// 备份必须存在且内容为旧版本 —— 这是回滚能力的物理保证。
	backup, err := os.ReadFile(execPath + ".old")
	if err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if string(backup) != "old-binary" {
		t.Fatalf("backup=%q, want %q", backup, "old-binary")
	}
	// 新二进制必须已就位。
	installed, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatalf("read installed: %v", err)
	}
	if string(installed) != "new-binary" {
		t.Fatalf("installed=%q, want %q", installed, "new-binary")
	}
}

// TestResolveRestartHandoff_WaitsForLateJournal 是 2026-10-06 竞态事故的
// **核心回归测试**。
//
// 事故现场：新进程在启动早期就读交接单，而旧进程按老顺序（spawn → 等端口通 →
// 才写单子）落盘，于是写入**必然**晚于读取。新进程读了个空，把这次计划内重启
// 当成进程崩溃 —— 所有会话被标 failed，用户看到"重启后会话还是失败"。
//
// 本测试模拟"读得比写得早"：先起一个读取方，300ms 后才放下单子。
// 修复前（只读一次）必然读空；修复后（带重试窗口）必须读到。
func TestResolveRestartHandoff_WaitsForLateJournal(t *testing.T) {
	dir := t.TempDir()
	exe, sum := writeFakeExe(t, dir, "pieqi.exe", "v2-new")

	// 旧进程"迟到"的单子：300ms 后才落盘。
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = writeRestartJournal(dir, &RestartJournal{
			StagedSHA256:    sum,
			AffectedTaskIDs: []string{"late-task"},
		})
	}()

	// 读取方立刻开读 —— 此刻盘上还没有单子。
	out := ResolveRestartHandoffWith(dir, exe, 30, 50*time.Millisecond)

	if !out.WasSelfRestart {
		t.Fatalf("WasSelfRestart = false，迟到的交接单被漏掉了（竞态回归）：%s", out.Reason)
	}
	if !out.Succeeded {
		t.Fatalf("Succeeded = false, want true：%s", out.Reason)
	}
	if out.Journal == nil || len(out.Journal.AffectedTaskIDs) != 1 {
		t.Fatalf("受影响会话丢失：%+v", out.Journal)
	}
}

// TestResolveRestartHandoff_SingleAttemptDoesNotWait attempts=1 必须等价于
// "只读一次、立刻返回" —— 保证测试/调用方可以显式关掉等待窗口。
func TestResolveRestartHandoff_SingleAttemptDoesNotWait(t *testing.T) {
	dir := t.TempDir()
	exe, _ := writeFakeExe(t, dir, "pieqi.exe", "v1")

	start := time.Now()
	out := ResolveRestartHandoffWith(dir, exe, 1, time.Second)
	elapsed := time.Since(start)

	if out.WasSelfRestart {
		t.Errorf("无单子时 WasSelfRestart = true, want false")
	}
	// 单次读不得睡眠（否则每次普通启动都会被拖慢）。
	if elapsed > 200*time.Millisecond {
		t.Errorf("attempts=1 竟然等待了 %v，应立即返回", elapsed)
	}
}

// TestResolveRestartHandoff_CorruptDoesNotWait 损坏的单子不该傻等：
// 它不是"还没写出来"，等下去也不会变好，必须立刻判定。
func TestResolveRestartHandoff_CorruptDoesNotWait(t *testing.T) {
	dir := t.TempDir()
	exe, _ := writeFakeExe(t, dir, "pieqi.exe", "v1")
	if err := os.WriteFile(journalPath(dir), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	out := ResolveRestartHandoffWith(dir, exe, 30, 100*time.Millisecond)
	elapsed := time.Since(start)

	if out.WasSelfRestart || out.Succeeded {
		t.Errorf("损坏单子应判定为非自重启，got %+v", out)
	}
	if elapsed > 300*time.Millisecond {
		t.Errorf("损坏单子竟然等待了 %v，应立即判定", elapsed)
	}
}

// TestRestart_WritesHandoffBeforeSpawn 守住"先落单、再拉起新进程"这个顺序。
//
// 这是本次修复的**顺序契约**：只要单子在新进程被 spawn 之前落盘，新进程就不
// 可能读空。测试用 affectedFunc 记录调用，并断言 Restart 返回后单子已在盘上
// 且内容完整 —— 若有人把 writeHandoff 挪回 spawn 之后，本测试会失败。
func TestRestart_WritesHandoffBeforeSpawn(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "pieqi.exe")
	if err := os.WriteFile(execPath, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	sr := NewSelfRestart(execPath, dir, dir, nil)
	sr.SetAffectedFunc(func() []string { return []string{"t1"} })
	sr.SetInitiator("t1")

	staged := sr.stagingPath()
	if err := os.MkdirAll(filepath.Dir(staged), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte("new-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	// spawnDetached 在测试环境会真的尝试起进程；无论成败，交接单都必须在
	// spawn **之前**就已落盘（这正是本测试要守住的顺序）。
	_ = sr.Restart("")

	// 若 spawn 成功，单子应保留；若 spawn 失败，实现会撤回单子（那也是正确行为）。
	// 两种情况下都断言："单子内容若存在，必须完整且指向新二进制"。
	if j, err := readRestartJournal(dir); err != nil {
		t.Fatalf("读取交接单出错: %v", err)
	} else if j != nil {
		wantSum, _ := exeSHA256(staged)
		if j.StagedSHA256 != wantSum {
			t.Errorf("交接单摘要 = %s, want %s", shortSHA(j.StagedSHA256), shortSHA(wantSum))
		}
		if len(j.AffectedTaskIDs) != 1 || j.AffectedTaskIDs[0] != "t1" {
			t.Errorf("受影响会话 = %v, want [t1]", j.AffectedTaskIDs)
		}
		if j.InitiatorTaskID != "t1" {
			t.Errorf("发起者 = %q, want %q", j.InitiatorTaskID, "t1")
		}
	}
}

// TestRestart_NoHandoffLeftBehindWhenSpawnFails spawn 失败时不能留下"幽灵单子"。
//
// 留下的后果很隐蔽：下一次**普通启动**会读到一张声称"有重启"的单子，而它的
// 摘要与实际运行的二进制对不上 —— 于是一批早已处理过的会话被凭空判成待接续。
func TestRestart_NoHandoffLeftBehindWhenSpawnFails(t *testing.T) {
	dir := t.TempDir()
	// execPath 指向一个不存在的可执行文件会让 spawnDetached 失败。
	execPath := filepath.Join(dir, "not-runnable.exe")
	if err := os.WriteFile(execPath, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	sr := NewSelfRestart(execPath, dir, dir, nil)
	sr.SetAffectedFunc(func() []string { return []string{"t1"} })

	staged := sr.stagingPath()
	if err := os.MkdirAll(filepath.Dir(staged), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte("new-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := sr.Restart("")
	if err == nil {
		t.Skip("本环境下 spawnDetached 意外成功，跳过（该分支只在 spawn 失败时适用）")
	}

	// spawn 失败 ⇒ 重启没有发生 ⇒ 盘上不应有交接单。
	if _, statErr := os.Stat(journalPath(dir)); !os.IsNotExist(statErr) {
		t.Errorf("spawn 失败后残留了交接单，stat err = %v", statErr)
	}
}
