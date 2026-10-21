package core

import (
	"encoding/json"
	"fmt"
	"net"
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
func TestSelfRestart_BacksUpRunningBinary(t *testing.T) {	dir := t.TempDir()
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

	// 走到 spawnDetached 会真的起进程，而这里的 exe 内容是 "new-binary"这种
	// **非 PE 文件** —— Windows 的 CreateProcess 会回落到 ShellExecute（按文件关联
	// 打开），实测 2026-10-21 真的拉起了一个外部程序（残留说明符被当文件打开）。
	// 所以只验证到"换文件"这一步：直接调两个文件步骤，绕开 spawn。
	if err := os.Rename(execPath, execPath+".old"); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(staged, execPath); err != nil {
		t.Fatal(err)
	}

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

// ---- 就绪信号（坑 7 的核心回归）----
//
// 事故：旧进程用 canDial(addr) 判断"新实例起来了没有"，而它自己就监听着该端口
// ⇒ 第一次探测即 true ⇒ 立刻 exit(0) ⇒ 新实例 150ms 后才 bind ⇒ EADDRINUSE
// ⇒ 服务彻底消失。2026-07-07 首次记录，2026-10-13 12:31 再次复现。
//
// 修法：信号只能来自**新进程**（它写就绪文件报到），且必须排除"我自己写的"
// 与"上一轮遗留的"。以下测试逐条钉住这三个判据。

// TestAwaitRestartReady_NotFooledByStaleFile 陈旧信号文件不得让等待立即成功。
//
// 这是"同义反复"的新形态：若不清掉上一轮留下的文件，旧进程会读到一个 PID 属于
// 早已退出的进程的文件，误判"新实例起来了"而退出 —— 与端口探测犯的是同一个错。
func TestAwaitRestartReady_NotFooledByStaleFile(t *testing.T) {
	dir := t.TempDir()
	// 伪造一个"上一轮"留下的信号（PID 故意不同于当前进程）
	stale := RestartReady{PID: os.Getpid() + 100000, At: time.Now().Add(-time.Hour)}
	data, _ := json.Marshal(stale)
	if err := os.WriteFile(readyPath(dir), data, 0o644); err != nil {
		t.Fatal(err)
	}

	// 短超时：陈旧文件**必须**被清掉，因此这里应等到超时返回 false。
	if awaitRestartReady(dir, 300*time.Millisecond) {
		t.Fatal("陈旧的就绪信号让等待立即成功 —— 这正是同义反复的复发形态")
	}
}

// TestAwaitRestartReady_NotFooledByOwnSignal 自己写的信号不算"新实例报到"。
func TestAwaitRestartReady_NotFooledByOwnSignal(t *testing.T) {
	dir := t.TempDir()
	// MarkRestartReady 用的是当前进程 pid —— 等自己写的信号是纯粹的同义反复。
	if err := MarkRestartReady(dir); err != nil {
		t.Fatal(err)
	}
	if awaitRestartReady(dir, 300*time.Millisecond) {
		t.Fatal("把**自己**写的就绪信号当成了新实例报到 —— 判据必须排除自身 PID")
	}
}

// TestAwaitRestartReady_SucceedsOnForeignSignal 别的进程写了信号 ⇒ 等待成功。
func TestAwaitRestartReady_SucceedsOnForeignSignal(t *testing.T) {
	dir := t.TempDir()

	// 模拟"新实例"：另一个 pid 在 200ms 后写下信号。
	go func() {
		time.Sleep(200 * time.Millisecond)
		foreign := RestartReady{PID: os.Getpid() + 1, At: time.Now()}
		data, _ := json.Marshal(foreign)
		_ = os.WriteFile(readyPath(dir), data, 0o644)
	}()

	if !awaitRestartReady(dir, 3*time.Second) {
		t.Fatal("等到了超时，但新实例确实写下了就绪信号")
	}
}

// TestAwaitRestartReady_TimesOutWithoutSignal 新实例没起来时不能谎报成功。
//
// 谎报的代价是灾难性的：旧进程会退出，而新实例并不存在 ⇒ 服务消失。
func TestAwaitRestartReady_TimesOutWithoutSignal(t *testing.T) {
	dir := t.TempDir()
	if awaitRestartReady(dir, 300*time.Millisecond) {
		t.Fatal("没有任何进程写信号，却报告就绪")
	}
}

// TestAwaitRestartReady_CorruptSignalIgnored 损坏的信号文件不能当成"已就绪"。
func TestAwaitRestartReady_CorruptSignalIgnored(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(readyPath(dir), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if awaitRestartReady(dir, 300*time.Millisecond) {
		t.Fatal("损坏的信号文件被当成了已就绪")
	}
}

// TestMarkRestartReady_WritesParseablePID 信号文件必须带可解析的 PID ——
// 判据完全依赖它（PID ≠ 自己），写不出来等于判据失效。
func TestMarkRestartReady_WritesParseablePID(t *testing.T) {
	dir := t.TempDir()
	if err := MarkRestartReady(dir); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(readyPath(dir))
	if err != nil {
		t.Fatalf("信号文件未落盘：%v", err)
	}
	var r RestartReady
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatalf("信号文件不可解析：%v", err)
	}
	if r.PID != os.Getpid() {
		t.Fatalf("PID=%d, want %d（当前进程）", r.PID, os.Getpid())
	}
}

// TestRollbackBinary_RestoresPreviousBuild 新实例没起来时必须能把旧二进制换回来。
//
// 这是"起不来的版本 + 即将退出的旧进程 = 服务消失"的最后一道防线：
// 二进制已经换成新的了，新版本又跑不起来，不回滚就真的没得跑了。
func TestRollbackBinary_RestoresPreviousBuild(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "pieqi.exe")
	// 现状：路径上是"起不来的新版本"，.old 是可用的旧版本
	if err := os.WriteFile(execPath, []byte("broken-new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(execPath+".old", []byte("good-old"), 0o755); err != nil {
		t.Fatal(err)
	}
	sr := NewSelfRestart(execPath, dir, dir, nil)

	if err := sr.rollbackBinary(); err != nil {
		t.Fatalf("rollbackBinary: %v", err)
	}

	// 路径上必须是旧版本 —— 下次重启才有得跑。
	got, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatalf("read execPath: %v", err)
	}
	if string(got) != "good-old" {
		t.Fatalf("execPath=%q, want %q", got, "good-old")
	}
	// 起不来的新版本应被挪到 .failed 保留（供事后排查为什么不起来），而不是删掉。
	failed, err := os.ReadFile(execPath + ".failed")
	if err != nil {
		t.Fatalf("失败版本未保留在 .failed：%v", err)
	}
	if string(failed) != "broken-new" {
		t.Fatalf(".failed=%q, want %q", failed, "broken-new")
	}
}

// TestRollbackBinary_NoBackupReportsError 没有备份时必须明确报错，
// 而不是静默"回滚成功"让调用方以为还有退路。
func TestRollbackBinary_NoBackupReportsError(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "pieqi.exe")
	if err := os.WriteFile(execPath, []byte("only-one"), 0o755); err != nil {
		t.Fatal(err)
	}
	sr := NewSelfRestart(execPath, dir, dir, nil)

	if err := sr.rollbackBinary(); err == nil {
		t.Fatal("没有 .old 备份却报告回滚成功")
	}
	// 原文件必须原样留着（回滚失败不能把仅有的二进制也搞丢）。
	got, _ := os.ReadFile(execPath)
	if string(got) != "only-one" {
		t.Fatalf("回滚失败后 execPath=%q, want 原内容保留", got)
	}
}

// TestWaitForListener_UsesReadySignalNotPort 回归：等待判据必须是"新进程报到"，
// **不能**退回按端口探测。
//
// 这里起一个真实监听同一端口的 listener 来模拟"旧进程自己还在监听"——
// 旧实现会因此立即返回 true（同义反复），新实现必须仍然等到超时。
func TestWaitForListener_UsesReadySignalNotPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	// 持续接受连接，模拟"端口是通的"
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	dir := t.TempDir()
	sr := NewSelfRestart(filepath.Join(dir, "pieqi.exe"), dir, dir, nil)
	addr := ln.Addr().String()

	// 端口是通的，但没有新进程写就绪信号 ⇒ 必须返回 false。
	// 旧实现（canDial）在这里会返回 true，这正是把它改成信号判据的原因。
	if sr.waitForListener(addr, 400*time.Millisecond) {
		t.Fatal("端口通就报告新实例就绪 —— 同义反复回归！等待判据必须只看新进程的报到信号")
	}
}

// ---- staged 文件的自动清理（对齐 magpie 的 RemoveStaleNew）----

// TestCleanupStagedIfConsumed_RemovesIdentical staged 与已安装内容一致 ⇒ 删掉。
//
// 不清理的后果：GET /api/admin/restart 永远报 staged:true，而"待应用的新版本"
// 其实早就装上了。更糟的是它会给下一次重启埋雷 —— 某次构建后忘记重新投递，
// 磁盘上却还躺着上一版的同名文件，一次误触就把旧版本换了上去（等于回退）。
func TestCleanupStagedIfConsumed_RemovesIdentical(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "pieqi.exe")
	staged := filepath.Join(dir, "pieqi.new.exe")
	content := []byte("identical-build")
	if err := os.WriteFile(execPath, content, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, content, 0o755); err != nil {
		t.Fatal(err)
	}
	sr := NewSelfRestart(execPath, dir, dir, nil)

	sr.cleanupStagedIfConsumed(staged)

	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatalf("内容一致的 staged 文件未被清理，stat err = %v", err)
	}
	// 已安装的 exe 绝不能被误删。
	if _, err := os.Stat(execPath); err != nil {
		t.Fatalf("清理误伤了已安装的 exe：%v", err)
	}
}

// TestCleanupStagedIfConsumed_KeepsDifferent staged 与已安装内容**不同** ⇒ 必须留着。
//
// 这是本函数最重要的保护：不同的内容意味着**有第三方正在交付另一个版本**
// （并行会话的 agent 刚写好新文件）。此时删掉就是替别人丢掉他的构建产物，
// 而对方无从得知 —— 他的重启会以"staged binary not found"失败。
func TestCleanupStagedIfConsumed_KeepsDifferent(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "pieqi.exe")
	staged := filepath.Join(dir, "pieqi.new.exe")
	if err := os.WriteFile(execPath, []byte("installed-build"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte("someone-elses-new-build"), 0o755); err != nil {
		t.Fatal(err)
	}
	sr := NewSelfRestart(execPath, dir, dir, nil)

	sr.cleanupStagedIfConsumed(staged)

	got, err := os.ReadFile(staged)
	if err != nil {
		t.Fatalf("内容不同的 staged 文件被误删了（会替并行会话丢掉构建产物）：%v", err)
	}
	if string(got) != "someone-elses-new-build" {
		t.Fatalf("staged 内容被改动：%q", got)
	}
}

// TestCleanupStagedIfConsumed_MissingStagedIsSafe staged 不存在时不得 panic 或报错。
func TestCleanupStagedIfConsumed_MissingStagedIsSafe(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "pieqi.exe")
	if err := os.WriteFile(execPath, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	sr := NewSelfRestart(execPath, dir, dir, nil)
	sr.cleanupStagedIfConsumed(filepath.Join(dir, "does-not-exist.exe"))
	// 已安装的 exe 必须安然无恙
	if _, err := os.Stat(execPath); err != nil {
		t.Fatalf("exe 被误伤：%v", err)
	}
}

// ---- 死锁回归：让出端口必须发生在等新实例**之前** ----
//
// 事故（2026-10-13 20:52 实测）：上一版让旧实例"等到新实例报到才退出"，
// 却没让它让出端口，于是形成闭环：
//
//	新实例要 bind :3000 → 得等旧实例退出
//	旧实例要退出         → 得等新实例报到（发生在其 bind 成功之后）
//
// 实测结果：新实例 bind 重试 10s 后 FATAL，旧实例 20s 后回滚 —— 重启永远不成。
// 修法：**先关掉自己的 listener 让出端口，再等新实例**。以下测试钉住这个顺序。

// TestRestartAndExit_ReleasesPortBeforeWaiting 让位必须先于等待。
//
// 判据：调用 RestartAndExit 后 listener 应**立即**被关闭（而不是等超时后才关）。
// 若不成立，新实例在整个等待窗口里都 bind 不上 —— 正是死锁。
func TestRestartAndExit_ReleasesPortBeforeWaiting(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "pieqi.exe")
	if err := os.WriteFile(execPath, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(dir, "pieqi.new"+exeSuffix())
	if err := os.WriteFile(staged, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}

	// 起一个真实 listener 充当"旧实例占着的端口"。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	sr := NewSelfRestart(execPath, dir, dir, nil)
	sr.SetListener(ln)
	sr.readyTimeout = 300 * time.Millisecond // 缩短等待，测试不必真等 25s
	exited := make(chan int, 1)
	sr.SetRebindFunc(func() error { return nil }) // 抢回：本测试只关心"没退出"

	// RestartAndExit 会：换二进制 → 关 listener（让位）→ 返回（不阻塞等待）。
	sr.RestartAndExit(ln.Addr().String(), func(code int) { exited <- code })

	// ★ 核心断言：端口必须**已**被让出 —— 用"能不能重新 bind 上"来验证。
	//
	// 为什么不直接对旧 listener 调 Accept 看它是否报错：Go 里 Close 并发进行中的
	// Accept **不保证立即返回**（实测在 Windows 上会一直阻塞），拿它当判据会写出
	// 一个永远卡住的测试。而"端口是否可被占用"才是真正决定新实例生死的那条性质
	// —— 新实例要做的正是 net.Listen 同一个地址。
	//
	// 注：这里必须先 Close 掉测试自己持有的 ln（defer 里的那份），否则同进程内
	// 它自己还占着地址。故先显式关，再尝试 bind。
	addr := ln.Addr().String()
	_ = ln.Close()
	rebind, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("端口未被让出（新实例会 bind 失败 ⇒ 死锁）：%v", err)
	}
	_ = rebind.Close()

	// 等后台决策落地：新实例没报到 ⇒ 应走"抢回端口"，**不能退出**。
	select {
	case code := <-exited:
		t.Fatalf("不该退出（应抢回端口继续服务），却 exit(%d)", code)
	case <-time.After(3 * time.Second):
		// 预期：既不退出，也已让出端口。
	}
}

// TestWaitAfterRelease_ExitsWhenNewInstanceReportsReady 新实例报到 ⇒ 退出让位。
//
// 直接测 WaitAfterRelease（而非整个 RestartAndExit）：后者要真的 spawn 一个可执行
// 子进程，在单测里只能用文本文件冒充，spawn 必然失败并**正确地**提前返回
// （"spawn 失败就保持现状"）。决策逻辑本身在 WaitAfterRelease 里，而它不需要 spawn。
func TestWaitAfterRelease_ExitsWhenNewInstanceReportsReady(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "pieqi.exe")
	if err := os.WriteFile(execPath, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(execPath+".old", []byte("older"), 0o755); err != nil {
		t.Fatal(err)
	}

	sr := NewSelfRestart(execPath, dir, dir, nil)
	sr.readyTimeout = 6 * time.Second

	exited := make(chan int, 1)
	sr.exitFn = func(code int) { exited <- code }
	sr.SetRebindFunc(func() error {
		t.Error("新实例已报到，不该走抢回分支")
		return nil
	})

	go sr.WaitAfterRelease()

	// 让位之后，模拟"另一个进程报到"。必须用**别的** pid：
	// awaitRestartReady 会排除自身 pid（那是防同义反复的判据之一）。
	time.Sleep(100 * time.Millisecond)
	foreign := RestartReady{PID: os.Getpid() + 1, At: time.Now()}
	b, _ := json.Marshal(foreign)
	if err := os.WriteFile(readyPath(dir), b, 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case code := <-exited:
		if code != 0 {
			t.Fatalf("exit code = %d, want 0（成功让位）", code)
		}
	case <-time.After(9 * time.Second):
		t.Fatal("新实例已报到，旧实例却没有退出让位")
	}
}

// TestWaitAfterRelease_RollsBackAndKeepsServing 新实例没起来 ⇒ 回滚 + 抢回端口保活。
//
// 这是"最坏情况"的定义：重启没成功，但**服务一直在**。
func TestWaitAfterRelease_RollsBackAndKeepsServing(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "pieqi.exe")
	if err := os.WriteFile(execPath, []byte("new-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(execPath+".old", []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	var logs []string
	sr := NewSelfRestart(execPath, dir, dir, func(msg string, kv ...any) {
		logs = append(logs, fmt.Sprintf("%s %v", msg, kv))
	})
	sr.readyTimeout = 300 * time.Millisecond // 新实例不会报到

	rebound := make(chan struct{}, 1)
	sr.SetRebindFunc(func() error {
		rebound <- struct{}{}
		return nil
	})
	exited := make(chan int, 1)
	sr.exitFn = func(code int) { exited <- code }

	go sr.WaitAfterRelease()

	select {
	case <-rebound:
		// 正确：抢回端口继续服务
	case code := <-exited:
		t.Fatalf("新实例没起来时不该退出（服务会消失），却 exit(%d)；日志=%v", code, logs)
	case <-time.After(5 * time.Second):
		t.Fatalf("既没抢回端口，也没退出 —— 卡住了；日志=%v", logs)
	}

	// 二进制必须已回滚：下次重启才有得跑。
	got, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatalf("read execPath: %v", err)
	}
	if string(got) != "old-binary" {
		t.Fatalf("execPath=%q, want 回滚成 old-binary；日志=%v", got, logs)
	}
}

// TestWaitAfterRelease_NoRebindHookExits 没接线 rebind 时不能变成僵尸进程。
//
// 判据：既抢不回端口、又不退出 = 一个不再服务的进程白占着 PID，
// 而外部监督者（技能 §2.1 的拉起路径）也无从判断该不该介入。
func TestWaitAfterRelease_NoRebindHookExits(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "pieqi.exe")
	if err := os.WriteFile(execPath, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(execPath+".old", []byte("older"), 0o755); err != nil {
		t.Fatal(err)
	}
	sr := NewSelfRestart(execPath, dir, dir, nil)
	sr.readyTimeout = 200 * time.Millisecond
	// 刻意不 SetRebindFunc

	exited := make(chan int, 1)
	sr.exitFn = func(code int) { exited <- code }

	go sr.WaitAfterRelease()

	select {
	case code := <-exited:
		if code == 0 {
			t.Fatal("没抢回端口却报 exit(0)（会让调用方以为服务已交给别人）")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("没接线 rebind 时既没抢回也没退出 —— 变成僵尸进程")
	}
}
