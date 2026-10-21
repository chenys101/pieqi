package core

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 「只重启、不换二进制」的纪律测试（2026-10-21，用户要求：
// "有新编译的产物就走替换并启动流程，没有的话就只是重新启动"）。
//
// 这条路径最容易出的错不是"重启失败"，而是**悄悄换了版**：
// 调用方以为只是重启，磁盘上的 exe 却被替换了。所以这里钉死三件事：
//  1. ReloadJustRestart 不碰 exe、不碰 staged；
//  2. 交接单的摘要取自**当前** exe（这样新进程能通过校验、把会话接回来）；
//  3. 没有 staged 文件时它也必须能跑通（不报"staged not found"）。

// TestReloadJustRestart_DoesNotTouchExecutable 只重启不得动任何文件。
//
// ⚠️ 测试隔离纪律（2026-10-21 实测踩到）：`reloadTestExec` 里那个 exe 是"内容为
// 任意字节"的假文件。一旦走到 spawnDetached，Windows 的 CreateProcess 对
// "文件存在但格式不对"会**回落到 ShellExecute**（按文件关联去打开）—— 实测
// 因此真的拉起了一个外部程序。所以本用例只验证到"落单"为止，**不调**会 spawn 的
// ReloadJustRestart；"只重启的编排照常让位/等就绪"由 TestRestartAndExit_* 覆盖。
func TestReloadJustRestart_DoesNotTouchExecutable(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "pieqi.exe")
	if err := os.WriteFile(execPath, []byte("current-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	sr := NewSelfRestart(execPath, dir, dir, nil)

	// 只重启走的是 restartWith("", false) → spawnAndHandoff()（不读 staged、
	// 不 os.Rename、不 copyFile）。这里直接调那个共用步骤，让它在 spawn 之前
	// 返回失败（exec 路径是假文件，Start 会报错），从而只验证"落单是否用了
	// 当前 exe 摘要"这一条纯文件纪律。
	err := sr.spawnAndHandoff()
	if err == nil {
		t.Fatal("假 exe 竟然 spawn 成功了，测试环境假设被打破，请重新评估隔离方案")
	}
	if _, statErr := os.Stat(execPath + ".old"); statErr == nil {
		t.Fatalf("%s.old 被创建了 —— 只重启不该产生任何备份交换", execPath)
	}

	got, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatalf("read exec: %v", err)
	}
	if string(got) != "current-binary" {
		t.Fatalf("exe 被改写成 %q，want %q（只重启不得替换二进制）", got, "current-binary")
	}
	if _, statErr := os.Stat(execPath + ".old"); statErr == nil {
		t.Fatalf("%s.old 被创建了 —— 只重启不该产生任何备份交换", execPath)
	}
}

// TestReloadJustRestart_WithStagedStillDoesNotSwap 即使磁盘上**有**新产物，
// 只重启路径也必须只重启（"要不要换版"由调用方决定，不是它）。
func TestReloadJustRestart_WithStagedStillDoesNotSwap(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "pieqi.exe")
	if err := os.WriteFile(execPath, []byte("current-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	sr := NewSelfRestart(execPath, dir, dir, nil)
	staged := sr.stagingPath()
	if err := os.MkdirAll(filepath.Dir(staged), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte("brand-new-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	// 同上一例：只走到落单失败为止，不触发 spawn（见测试顶部隔离说明）。
	if err := sr.spawnAndHandoff(); err == nil {
		t.Fatal("假 exe 竟然 spawn 成功了，测试环境假设被打破")
	}

	got, _ := os.ReadFile(execPath)
	if string(got) != "current-binary" {
		t.Fatalf("exe 被 staged 覆盖了（%q）—— 只重启路径不得消费 staged", got)
	}
	// staged 也必须原封不动：那是别人待交付的产物，不是我们该清的。
	sgot, err := os.ReadFile(staged)
	if err != nil {
		t.Fatalf("staged 被删除了：%v", err)
	}
	if string(sgot) != "brand-new-binary" {
		t.Fatalf("staged 被改写为 %q", sgot)
	}
}

// TestRestartAndExitNoSwap_ReleasesPortWithoutSwapping 只重启的编排同样要
// **先让出端口**，且全程不得替换二进制。
//
// 这条是给"只重启"补的时序回归：它和换版路径共用 restartAndExit，
// 最容易在重构时被拆成两条各写一遍 —— 那第二条就没人守着 2026-10-13 的死锁。
func TestRestartAndExitNoSwap_ReleasesPortWithoutSwapping(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "pieqi.exe")
	if err := os.WriteFile(execPath, []byte("current-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()

	sr := NewSelfRestart(execPath, dir, dir, nil)
	sr.SetListener(ln)
	sr.readyTimeout = 300 * time.Millisecond
	exited := make(chan int, 1)
	sr.SetRebindFunc(func() error { return nil }) // 抢回：本测试只关心"没退出"

	sr.RestartAndExitNoSwap(addr, func(code int) { exited <- code })

	// 端口必须已让出（与换版路径同一条判据）。
	_ = ln.Close()
	rebind, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("只重启路径没有让出端口（新实例会 bind 失败）：%v", err)
	}
	_ = rebind.Close()

	// 二进制一个字节都不能变。
	got, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatalf("read exec: %v", err)
	}
	if string(got) != "current-binary" {
		t.Fatalf("只重启把 exe 换成了 %q", got)
	}

	select {
	case code := <-exited:
		t.Fatalf("不该退出（应抢回端口继续服务），却 exit(%d)", code)
	case <-time.After(2 * time.Second):
	}
}

// TestReloadJustRestart_HandoffUsesCurrentExecutable 交接单摘要 = 当前 exe。
//
// 这条决定了"只重启"后能不能把在途会话接回来：新进程读单子时会拿**自己的**
// exe 摘要与 StagedSHA256 比对，不等就被判"重启未成功"，一批 running 会话
// 会因此判 failed。所以这里的判据不是"写了个文件"，而是"写的摘要对得上"。
func TestReloadJustRestart_HandoffUsesCurrentExecutable(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "pieqi.exe")
	if err := os.WriteFile(execPath, []byte("current-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	sr := NewSelfRestart(execPath, dir, dir, nil)

	// ⚠️ 只调落单那一步，**绝不调 ReloadJustRestart()**：后者会 spawnDetached，
	// 而这里的 execPath 是"内容为 current-binary 的假 exe"—— Windows 的
	// CreateProcess 对格式不对的文件会回落到 ShellExecute（按文件关联打开），
	// 实测 2026-10-21 因此真的调起了外部程序。见测试顶部关于隔离的说明。
	if err := sr.writeHandoff(); err != nil {
		t.Fatalf("writeHandoff: %v", err)
	}
	want, err := exeSHA256(execPath)
	if err != nil {
		t.Fatal(err)
	}
	j, err := readRestartJournal(dir)
	if err != nil || j == nil {
		t.Fatalf("read journal: %v (nil=%v)", err, j == nil)
	}
	if j.StagedSHA256 != want {
		t.Fatalf("StagedSHA256=%s, want 当前 exe 的摘要 %s（否则新进程校验不过、会话被判 failed）",
			j.StagedSHA256, want)
	}
	// 用同一张单子去"解析"，应判定为重启成功。
	if out := ResolveRestartHandoffWith(dir, execPath, 1, 0); !out.Succeeded {
		t.Fatalf("按当前 exe 解析交接单应判成功，实际：%s", out.Reason)
	}
}
