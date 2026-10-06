package core

import (
	"os"
	"path/filepath"
	"testing"

	"pieqi/internal/model"
)

// writeFakeExe 写一个内容确定的"二进制"文件，返回其路径与摘要。
func writeFakeExe(t *testing.T, dir, name, content string) (string, string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake exe: %v", err)
	}
	sum, err := exeSHA256(p)
	if err != nil {
		t.Fatalf("sha256: %v", err)
	}
	return p, sum
}

// TestResolveRestartHandoff_NoJournal 普通启动（无交接单）不应被当成自重启。
func TestResolveRestartHandoff_NoJournal(t *testing.T) {
	dir := t.TempDir()
	exe, _ := writeFakeExe(t, dir, "pieqi.exe", "v1")

	out := ResolveRestartHandoff(dir, exe)
	if out.WasSelfRestart {
		t.Errorf("WasSelfRestart = true, want false（无交接单）")
	}
	if out.Succeeded {
		t.Errorf("Succeeded = true, want false")
	}
}

// TestResolveRestartHandoff_Success 摘要一致 ⇒ 重启成功，且交接单被清掉。
func TestResolveRestartHandoff_Success(t *testing.T) {
	dir := t.TempDir()
	exe, sum := writeFakeExe(t, dir, "pieqi.exe", "v2-new")

	j := &RestartJournal{
		StagedSHA256:    sum,
		AffectedTaskIDs: []string{"t1", "t2"},
	}
	if err := writeRestartJournal(dir, j); err != nil {
		t.Fatalf("write journal: %v", err)
	}

	out := ResolveRestartHandoff(dir, exe)
	if !out.WasSelfRestart {
		t.Fatalf("WasSelfRestart = false, want true")
	}
	if !out.Succeeded {
		t.Fatalf("Succeeded = false, want true（摘要一致）: %s", out.Reason)
	}
	if got := len(out.Journal.AffectedTaskIDs); got != 2 {
		t.Errorf("AffectedTaskIDs len = %d, want 2", got)
	}
	// 一次性交接：读完即失效，否则下次普通启动会凭空"恢复"旧会话。
	if _, err := os.Stat(journalPath(dir)); !os.IsNotExist(err) {
		t.Errorf("交接单未被清理，stat err = %v", err)
	}
}

// TestResolveRestartHandoff_HashMismatch 摘要不一致 ⇒ 判定未成功（例如新进程
// 其实还是旧二进制在跑，或交接单来自更早一次未完成的重启）。
func TestResolveRestartHandoff_HashMismatch(t *testing.T) {
	dir := t.TempDir()
	exe, _ := writeFakeExe(t, dir, "pieqi.exe", "v1-old")
	_, otherSum := writeFakeExe(t, dir, "pieqi.new.exe", "v2-never-installed")

	if err := writeRestartJournal(dir, &RestartJournal{
		StagedSHA256:    otherSum,
		AffectedTaskIDs: []string{"t1"},
	}); err != nil {
		t.Fatalf("write journal: %v", err)
	}

	out := ResolveRestartHandoff(dir, exe)
	if !out.WasSelfRestart {
		t.Fatalf("WasSelfRestart = false, want true")
	}
	if out.Succeeded {
		t.Fatalf("Succeeded = true, want false（摘要不一致）")
	}
	// 失败也要清掉：否则这张陈旧的单子会一直干扰后续每次启动。
	if _, err := os.Stat(journalPath(dir)); !os.IsNotExist(err) {
		t.Errorf("失败的交接单也应被清理，stat err = %v", err)
	}
}

// TestResolveRestartHandoff_CorruptJournal 损坏的交接单不能当成"没有"：
// 那会静默丢掉一批等待接回的会话。此处验证按普通启动处理并清理。
func TestResolveRestartHandoff_CorruptJournal(t *testing.T) {
	dir := t.TempDir()
	exe, _ := writeFakeExe(t, dir, "pieqi.exe", "v1")
	if err := os.WriteFile(journalPath(dir), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write corrupt journal: %v", err)
	}

	out := ResolveRestartHandoff(dir, exe)
	if out.WasSelfRestart || out.Succeeded {
		t.Errorf("损坏单子应判定为非自重启，got %+v", out)
	}
	if _, err := os.Stat(journalPath(dir)); !os.IsNotExist(err) {
		t.Errorf("损坏单子应被清理，stat err = %v", err)
	}
}

// TestResolveRestartHandoff_EmptyStagedSHA 缺摘要的单子无法校验，一律判未成功。
func TestResolveRestartHandoff_EmptyStagedSHA(t *testing.T) {
	dir := t.TempDir()
	exe, _ := writeFakeExe(t, dir, "pieqi.exe", "v1")
	if err := writeRestartJournal(dir, &RestartJournal{AffectedTaskIDs: []string{"t1"}}); err != nil {
		t.Fatalf("write journal: %v", err)
	}
	out := ResolveRestartHandoff(dir, exe)
	if !out.WasSelfRestart || out.Succeeded {
		t.Errorf("缺摘要应判未成功，got %+v", out)
	}
}

// TestTaskStore_InterruptedByRestartKeepsRunning 被自重启打断的 running 任务
// 必须保留 running（等自动接续），而不是像"进程崩了"那样标 failed。
//
// 这是本次事故的核心回归点：老行为把计划内重启当故障，用户看到一批 failed
// 且被迫人工 resume —— 那正是"会话老是中断"的体感来源。
func TestTaskStore_InterruptedByRestartKeepsRunning(t *testing.T) {
	dir := t.TempDir()

	s1, err := NewTaskStore(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	tsk := &model.Task{ID: "keep-me", Prompt: "p"}
	if _, err := s1.Create(tsk); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s1.Update("keep-me", func(x *model.Task) bool {
		x.Status = model.TaskRunning
		return true
	}); err != nil {
		t.Fatalf("set running: %v", err)
	}

	// 模拟自重启后新进程启动：把该任务列为待接续。
	s2, err := NewTaskStore(dir, "keep-me")
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	got, ok := s2.Get("keep-me")
	if !ok {
		t.Fatalf("task not found after reopen")
	}
	if got.Status != model.TaskRunning {
		t.Errorf("status = %s, want running（被打断的任务应保留待接续）", got.Status)
	}
	if !s2.InterruptedByRestart("keep-me") {
		t.Errorf("InterruptedByRestart = false, want true")
	}
}

// TestTaskStore_NotInterruptedStillFails 未被列为接续的 running 任务仍按
// 老行为标 failed —— 保住"进程崩了"这一真实故障的可见性，不要一并软化。
func TestTaskStore_NotInterruptedStillFails(t *testing.T) {
	dir := t.TempDir()

	s1, _ := NewTaskStore(dir)
	if _, err := s1.Create(&model.Task{ID: "crashed", Prompt: "p"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s1.Update("crashed", func(x *model.Task) bool {
		x.Status = model.TaskRunning
		return true
	}); err != nil {
		t.Fatalf("set running: %v", err)
	}

	s2, err := NewTaskStore(dir) // 不传 interrupted
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, _ := s2.Get("crashed")
	if got.Status != model.TaskFailed {
		t.Errorf("status = %s, want failed（非自重启的 running 仍应判死）", got.Status)
	}
	if got.Error == "" {
		t.Errorf("失败原因应被写入，实际为空")
	}
}
