package core

import (
	"os"
	"testing"

	"pieqi/internal/model"
)

// TestSelfRestart_HandoffEndToEnd 端到端串起一次自重启交接：
//
//	旧进程：算 staged 摘要 → 落交接单（含受影响会话）
//	换二进制（模拟 copy staged → execPath）
//	新进程：ResolveRestartHandoff 判成功 → 用该会话列表开 store → 任务保留 running
//
// 这是本次事故（会话被自重启反复打断且无法自动恢复）的完整回归路径。
func TestSelfRestart_HandoffEndToEnd(t *testing.T) {
	dir := t.TempDir()
	tasksDir := dir + "/tasks"

	// ---- ① 旧进程：先有一个正在跑的任务 ----
	store, err := NewTaskStore(tasksDir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if _, err := store.Create(&model.Task{ID: "iter-task", Prompt: "自迭代任务"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.Update("iter-task", func(x *model.Task) bool {
		x.Status = model.TaskRunning
		return true
	}); err != nil {
		t.Fatalf("set running: %v", err)
	}

	// 交付方（agent）把新二进制放进 staging 落点
	_, stagedSum := writeFakeExe(t, dir, "pieqi.new.exe", "binary-v2")
	// 当前跑的还是旧二进制
	exePath, _ := writeFakeExe(t, dir, "pieqi.exe", "binary-v1")

	// ---- ② 旧进程落交接单（等价于 SelfRestart.writeHandoff）----
	if err := writeRestartJournal(dir, &RestartJournal{
		FromPID:         1234,
		StagedSHA256:    stagedSum,
		AffectedTaskIDs: []string{"iter-task"},
	}); err != nil {
		t.Fatalf("write handoff: %v", err)
	}

	// ---- ③ 换二进制：此刻 exePath 内容变成 staged 的内容 ----
	if err := os.WriteFile(exePath, []byte("binary-v2"), 0o755); err != nil {
		t.Fatalf("swap binary: %v", err)
	}

	// ---- ④ 新进程启动：判定 + 恢复 ----
	out := ResolveRestartHandoff(dir, exePath)
	if !out.Succeeded {
		t.Fatalf("重启应判定成功，reason = %s", out.Reason)
	}
	if out.Journal == nil || len(out.Journal.AffectedTaskIDs) != 1 {
		t.Fatalf("受影响会话丢失：%+v", out.Journal)
	}

	store2, err := NewTaskStore(tasksDir, out.Journal.AffectedTaskIDs...)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	got, ok := store2.Get("iter-task")
	if !ok {
		t.Fatalf("任务在重启后消失")
	}
	// 关键断言：任务保留 running（可被 ResumeInterrupted 接续），不是 failed。
	if got.Status != model.TaskRunning {
		t.Errorf("status = %s, want running（应保留待自动接续）", got.Status)
	}
	if got.Error != "" {
		t.Errorf("被计划内重启打断的任务不应带错误信息，got %q", got.Error)
	}
}

// TestSelfRestart_InitiatorEndToEnd 端到端串起**发起者**被接回的完整链路。
//
// 与上面 HandoffEndToEnd 的区别：那条走的是"旁观者"（本来就在 affected 列表里），
// 这条走的是最难的一类 —— **发起重启的那个会话**。它的状态在退出前早已不是
// running，所以只有靠"收到请求时捕获的身份"才能被接回。
//
// 全链路（用真实 SelfRestart，不手搓交接单，确保 SetInitiator → writeHandoff
// 这段接线本身也被覆盖）：
//
//	① 旧进程：initiator 正在 running，它 POST /restart（SetInitiator）
//	② 退出前落交接单
//	③ 换二进制
//	④ 新进程：判定成功 → 用交接单开 store → initiator 保留 running
//	⑤ 接续时 initiator 拿到专属文案（含"不要再次调用"）
func TestSelfRestart_InitiatorEndToEnd(t *testing.T) {
	dir := t.TempDir()
	tasksDir := dir + "/tasks"

	// ---- ① 旧进程：发起者与一个旁观者都在 running ----
	store, err := NewTaskStore(tasksDir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	for _, id := range []string{"initiator", "bystander"} {
		if _, err := store.Create(&model.Task{ID: id, Prompt: "p", ProjectPath: dir}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
		if _, err := store.Update(id, func(x *model.Task) bool {
			x.Status = model.TaskRunning
			return true
		}); err != nil {
			t.Fatalf("set running %s: %v", id, err)
		}
	}

	_, stagedSum := writeFakeExe(t, dir, "pieqi.new.exe", "binary-v2")
	exePath, _ := writeFakeExe(t, dir, "pieqi.exe", "binary-v1")

	// ---- ② 旧进程落交接单：发起者身份在收到请求时已捕获 ----
	s := NewSelfRestart(exePath, dir, dir, nil)
	// 注意 affectedFunc 刻意**不含** initiator —— 精确复刻真实场景：
	// 走到这一步时发起者那一轮已死，LiveTaskIDs 看不到它。
	s.SetAffectedFunc(func() []string { return []string{"bystander"} })
	s.SetInitiator("initiator")
	if err := s.writeHandoff(); err != nil {
		t.Fatalf("writeHandoff: %v", err)
	}

	// ---- ③ 换二进制 ----
	if err := os.WriteFile(exePath, []byte("binary-v2"), 0o755); err != nil {
		t.Fatalf("swap: %v", err)
	}

	// ---- ④ 新进程启动 ----
	out := ResolveRestartHandoff(dir, exePath)
	if !out.Succeeded {
		t.Fatalf("应判定成功：%s", out.Reason)
	}
	if out.Journal == nil {
		t.Fatal("交接单丢失")
	}
	if out.Journal.InitiatorTaskID != "initiator" {
		t.Fatalf("InitiatorTaskID=%q, want initiator", out.Journal.InitiatorTaskID)
	}
	_ = stagedSum

	store2, err := NewTaskStore(tasksDir, out.Journal.AffectedTaskIDs...)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}

	// 关键断言 1：发起者被救回来了 —— 它没在 affectedFunc 里，全靠 SetInitiator。
	init, ok := store2.Get("initiator")
	if !ok || init == nil {
		t.Fatal("发起者任务丢失")
	}
	if init.Status != model.TaskRunning {
		t.Fatalf("发起者 status=%s, want running（必须靠身份捕获救回，而不是靠状态）", init.Status)
	}
	// 旁观者照旧保留。
	by, _ := store2.Get("bystander")
	if by == nil || by.Status != model.TaskRunning {
		t.Fatalf("旁观者未保留：%+v", by)
	}

	// ---- ⑤ 接续文案：发起者必须拿到劝阻重试的那一段 ----
	initPrompt := initiatorResumePrompt(out.Journal.StagedSHA256)
	if initPrompt == genericResumePrompt(out.Journal.StagedSHA256) {
		t.Fatal("发起者拿到了通用文案 —— 它可能重试重启，形成重启循环")
	}
}

// TestSelfRestart_HandoffNotSucceededFailsTask 反例：交接单在但二进制没换上
// （例如 spawn 了新进程但它其实没跑起来、又或是人工把旧 exe 放回去了），
// 此时**不能**自动接续，任务应退回 failed 交给人工 —— 宁可少接也不能误接。
func TestSelfRestart_HandoffNotSucceededFailsTask(t *testing.T) {
	dir := t.TempDir()
	tasksDir := dir + "/tasks"

	store, _ := NewTaskStore(tasksDir)
	if _, err := store.Create(&model.Task{ID: "t", Prompt: "p"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.Update("t", func(x *model.Task) bool {
		x.Status = model.TaskRunning
		return true
	}); err != nil {
		t.Fatalf("set running: %v", err)
	}

	_, stagedSum := writeFakeExe(t, dir, "pieqi.new.exe", "binary-v2")
	exePath, _ := writeFakeExe(t, dir, "pieqi.exe", "binary-v1") // 没换

	if err := writeRestartJournal(dir, &RestartJournal{
		StagedSHA256:    stagedSum,
		AffectedTaskIDs: []string{"t"},
	}); err != nil {
		t.Fatalf("write handoff: %v", err)
	}

	out := ResolveRestartHandoff(dir, exePath)
	if out.Succeeded {
		t.Fatalf("摘要不一致时不应判定成功")
	}

	// 判定失败 ⇒ main 侧传空列表 ⇒ 走原有 failed 分支
	store2, err := NewTaskStore(tasksDir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, _ := store2.Get("t")
	if got.Status != model.TaskFailed {
		t.Errorf("status = %s, want failed（未成功则退回人工）", got.Status)
	}
}
