package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pieqi/internal/model"
)

// 本文件守护「自重启后把**发起者**接回来」这条链路。
//
// 为什么单独一个文件：这里的失败模式极其隐蔽 —— 交接单、store.load、
// ResumeInterrupted 三段各自都"工作正常"，缺的只是它们之间那个 id 有没有
// 被传下去。任何一段漏掉，表现都只是"发起者又变成 failed 了"，
// 而日志里看不出是哪一段断的。所以每个衔接点都要有一条断言钉住。

// TestWriteHandoff_IncludesInitiator 交接单必须记下发起者，且它同时出现在
// AffectedTaskIDs 里。
//
// 两者都要：InitiatorTaskID 供"用专属文案接续"，AffectedTaskIDs 供
// store.load 保留 running。只记前者，load 会把任务判 failed，接续逻辑
// 压根看不到它 —— 这正是修复前发起者必死的机制。
func TestWriteHandoff_IncludesInitiator(t *testing.T) {
	dir := t.TempDir()
	exe, sum := writeFakeExe(t, dir, "pieqi.exe", "v-new")

	s := NewSelfRestart(exe, dir, "", nil)
	s.SetAffectedFunc(func() []string { return []string{"other-1", "other-2"} })
	s.SetInitiator("initiator-task")

	if err := s.writeHandoff(); err != nil {
		t.Fatalf("writeHandoff: %v", err)
	}

	j, err := readRestartJournal(dir)
	if err != nil || j == nil {
		t.Fatalf("readRestartJournal: j=%v err=%v", j, err)
	}
	if j.InitiatorTaskID != "initiator-task" {
		t.Fatalf("InitiatorTaskID=%q, want initiator-task", j.InitiatorTaskID)
	}
	if j.StagedSHA256 != sum {
		t.Fatalf("StagedSHA256 不匹配")
	}
	if j.Version == "" {
		t.Fatalf("Version 为空：接续文案要用它讲清新版本是哪个")
	}
	if !contains(j.AffectedTaskIDs, "initiator-task") {
		t.Fatalf("AffectedTaskIDs=%v，缺少发起者 —— load 会把它判 failed，接续看不到它",
			j.AffectedTaskIDs)
	}
	// 原有会话不能因为并入发起者而丢失。
	if !contains(j.AffectedTaskIDs, "other-1") || !contains(j.AffectedTaskIDs, "other-2") {
		t.Fatalf("AffectedTaskIDs=%v，原有会话被破坏", j.AffectedTaskIDs)
	}
}

// TestWriteHandoff_InitiatorNotDuplicated 发起者本来就在 affected 列表里时
// 不能重复 —— 重复 id 会让接续对同一会话跑两次 Resume。
func TestWriteHandoff_InitiatorNotDuplicated(t *testing.T) {
	dir := t.TempDir()
	exe, _ := writeFakeExe(t, dir, "pieqi.exe", "v-new")

	s := NewSelfRestart(exe, dir, "", nil)
	s.SetAffectedFunc(func() []string { return []string{"a", "initiator", "b"} })
	s.SetInitiator("initiator")

	if err := s.writeHandoff(); err != nil {
		t.Fatalf("writeHandoff: %v", err)
	}
	j, _ := readRestartJournal(dir)
	if j == nil {
		t.Fatal("journal nil")
	}
	n := 0
	for _, id := range j.AffectedTaskIDs {
		if id == "initiator" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("发起者出现 %d 次，want 1：%v", n, j.AffectedTaskIDs)
	}
}

// TestWriteHandoff_NoInitiatorKeepsOldBehavior 人类直接 curl 重启（识别不到
// 发起者）时必须完全退化成原行为：不新增任何 id、InitiatorTaskID 留空。
// 这条防止"为修一个特例而改变常规路径"。
func TestWriteHandoff_NoInitiatorKeepsOldBehavior(t *testing.T) {
	dir := t.TempDir()
	exe, _ := writeFakeExe(t, dir, "pieqi.exe", "v-new")

	s := NewSelfRestart(exe, dir, "", nil)
	s.SetAffectedFunc(func() []string { return []string{"only-1"} })

	if err := s.writeHandoff(); err != nil {
		t.Fatalf("writeHandoff: %v", err)
	}
	j, _ := readRestartJournal(dir)
	if j == nil {
		t.Fatal("journal nil")
	}
	if j.InitiatorTaskID != "" {
		t.Fatalf("InitiatorTaskID=%q, want 空", j.InitiatorTaskID)
	}
	if len(j.AffectedTaskIDs) != 1 || j.AffectedTaskIDs[0] != "only-1" {
		t.Fatalf("AffectedTaskIDs=%v, want [only-1]（无发起者时不该改动）", j.AffectedTaskIDs)
	}
}

// TestResolveRestartHandoff_CarriesInitiator 判定成功的路径上，发起者必须
// 能被启动流程读到 —— 它是 main 决定"要不要对它用专属文案"的唯一依据。
func TestResolveRestartHandoff_CarriesInitiator(t *testing.T) {
	dir := t.TempDir()
	exe, sum := writeFakeExe(t, dir, "pieqi.exe", "v-new")

	if err := writeRestartJournal(dir, &RestartJournal{
		StartedAt:       time.Now(),
		StagedSHA256:    sum,
		AffectedTaskIDs: []string{"initiator", "other"},
		InitiatorTaskID: "initiator",
		Version:         shortSHA(sum),
	}); err != nil {
		t.Fatalf("writeRestartJournal: %v", err)
	}

	out := ResolveRestartHandoff(dir, exe)
	if !out.Succeeded {
		t.Fatalf("want Succeeded, reason=%s", out.Reason)
	}
	if out.Journal == nil || out.Journal.InitiatorTaskID != "initiator" {
		t.Fatalf("Journal.InitiatorTaskID 丢失：%+v", out.Journal)
	}
}

// TestStoreLoad_KeepsInitiatorRunning 端到端的第一段：发起者在 load 时
// 必须保留 running（而不是被判"进程被重启打断"→ failed）。
//
// 这是修复前发起者必死的**直接地点**：它提交重启后状态早已不是 running，
// 若不显式把它放进 interrupted 列表，load 就把它标 failed 并写盘，
// 之后任何接续逻辑都无能为力（任务已是终态）。
func TestStoreLoad_KeepsInitiatorRunning(t *testing.T) {
	dir := t.TempDir()

	// 造一个"重启前"的 store：发起者与旁观者都在 running。
	s1, err := NewTaskStore(dir)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	for _, id := range []string{"initiator", "bystander"} {
		if _, err := s1.Create(&model.Task{ID: id, Status: model.TaskRunning,
			Prompt: "p", ProjectPath: dir}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
		if _, err := s1.Update(id, func(x *model.Task) bool {
			x.Status = model.TaskRunning
			return true
		}); err != nil {
			t.Fatalf("update %s: %v", id, err)
		}
	}

	// 模拟新进程启动：交接单说这两个都该接续。
	s2, err := NewTaskStore(dir, "initiator", "bystander")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	for _, id := range []string{"initiator", "bystander"} {
		tk, ok := s2.Get(id)
		if !ok || tk == nil {
			t.Fatalf("%s 丢失", id)
		}
		if tk.Status != model.TaskRunning {
			t.Fatalf("%s status=%s, want running（不该被判 failed）", id, tk.Status)
		}
	}
}

// TestStoreLoad_NonInitiatorStillFails 对照组：不在交接单里的 running 任务
// 仍必须被判 failed —— 那是"进程崩了"的意外场景，标 failed 是对的。
// 这条守住"不要把修复扩大成'所有 running 都不判失败'"，那会让真崩溃被掩盖。
func TestStoreLoad_NonInitiatorStillFails(t *testing.T) {
	dir := t.TempDir()
	s1, err := NewTaskStore(dir)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	if _, err := s1.Create(&model.Task{ID: "crashed", Status: model.TaskRunning,
		Prompt: "p", ProjectPath: dir}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s1.Update("crashed", func(x *model.Task) bool {
		x.Status = model.TaskRunning
		return true
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	// 只有别的任务在交接单里；crashed 不在 → 应判 failed。
	s2, err := NewTaskStore(dir, "someone-else")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	tk, _ := s2.Get("crashed")
	if tk == nil {
		t.Fatal("crashed 丢失")
	}
	if tk.Status != model.TaskFailed {
		t.Fatalf("status=%s, want failed（不在交接单里的 running 是意外崩溃）", tk.Status)
	}
	if tk.Error == "" {
		t.Fatal("Error 为空：必须说明是进程被打断")
	}
}

// TestResumeInterruptedWithInitiator_PromptWarnsAgainstRetry 这是整条链路的
// 关键断言：发起者拿到的 prompt 必须**明确劝阻再次重启**。
//
// 不做这件事的后果是重启循环：发起者接回后看到"我发过重启命令但不知结果"，
// 很可能重试 → 杀掉刚起来的新进程 → 又产生一个待接回的发起者 → 无限循环。
// 这是本功能最危险的失败模式，所以用"文案必须含劝阻"钉死它。
func TestResumeInterruptedWithInitiator_PromptWarnsAgainstRetry(t *testing.T) {
	if got := initiatorResumePrompt("v123"); !strings.Contains(got, "不要再次调用") {
		t.Fatalf("发起者文案缺少劝阻重试的内容：\n%s", got)
	}
	if got := initiatorResumePrompt("v123"); !strings.Contains(got, "v123") {
		t.Fatalf("发起者文案没带上版本号，它无法核对跑起来的是不是自己要的版本：\n%s", got)
	}
}

// TestResumeInterruptedWithInitiator_OnlyInitiatorGetsSpecialPrompt
// 通用文案与发起者文案必须是两条不同的路径 —— 对旁观者讲"这次重启是你发起的"
// 会让它误以为自己该负责验证，凭空制造困惑。
func TestResumeInterruptedWithInitiator_OnlyInitiatorGetsSpecialPrompt(t *testing.T) {
	generic := genericResumePrompt("v123")
	initiator := initiatorResumePrompt("v123")
	if generic == initiator {
		t.Fatal("两段文案相同：旁观者会被告知『这次重启是你发起的』")
	}
	if strings.Contains(generic, "不要再次调用") {
		t.Fatal("通用文案不该包含『不要再次调用重启』——旁观者本来就没触发它")
	}
}

// TestJournalPath_IsUnderDataRoot 交接单必须落在数据目录下（~/.pieqi），
// 而不是工作区 —— 它的读者是**新进程**，而新进程的 cwd 是 <dataRoot>/bin。
func TestJournalPath_IsUnderDataRoot(t *testing.T) {
	dir := t.TempDir()
	p := journalPath(dir)
	if filepath.Dir(p) != dir {
		t.Fatalf("journalPath=%q, want 位于 %q 下", p, dir)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("未写入时不该存在：%v", err)
	}
}
