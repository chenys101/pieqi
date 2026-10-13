package core

import (
	"testing"

	"pieqi/internal/model"
)

// 本文件守护「孤儿审批卡」不变量，对应 2026-10-13 的线上事故（第二次发作）：
//
//	现象：304456.xyz 上某会话的审批卡怎么点都返回
//	      HTTP 409 {"error":"no pending decision for task ..."}
//
// 根因是**两段时间线的错位**，日志实证：
//
//	12:53:32.860  发起者写交接单，affected 快照 = 1 个任务
//	12:53:36.360  本任务才拿到审批卡（ACP RequestPermission 阻塞等答复）
//	12:53:38.187  旧进程 exit —— 挂在 channel 上的那次 RequestPermission 随之蒸发
//	12:53:38.378  "resume interrupted task skipped (not resumable)" status=waiting_input
//
// 关键：这张卡**产生于交接单快照之后**，所以它既不在接续名单里，isResumable
// 又以 "approval 要等 Intervene" 为由拒绝接续 —— 于是谁都不管它，留下一个
// status=waiting_input 且 CurrentDecision 非空的僵尸态：前端照常渲染出可点的卡，
// 后端 pending 表却空无一物。
//
// 不变量：**waiting_input(approval) 必须有一个活着的 owner**。
// 自重启后 owner 必然已死，因此这类残留只有两种合法归宿：被接续，或被清算。
// 绝不允许"卡片还在、但没人能应答"。

// approvalDecision 造一张 approval 卡（复刻事故现场的 CurrentDecision）。
func approvalDecision(id string) *model.Decision {
	return &model.Decision{
		ID:        id,
		Kind:      model.DecisionKindApproval,
		Risk:      "L2",
		ToolName:  "pwsh",
		Summary:   "pwsh",
		Options:   []string{"approve", "deny", "approve_session"},
	}
}

// TestResumeInterrupted_OrphanedApprovalIsCleared ★ 本次事故的直接复刻。
//
// 任务在接续名单里、状态是 waiting_input(approval)：ResumeInterrupted 会跳过它
// （isResumable=false）。修复前这个 `continue` 什么都不做 —— 卡片留在盘上，
// 用户点「批准」拿 409。修复后必须清算成 failed **且清空 CurrentDecision**。
func TestResumeInterrupted_OrphanedApprovalIsCleared(t *testing.T) {
	tr, store, _, _ := newTestTaskRunner(t)

	dir := t.TempDir()
	task, err := store.Create(&model.Task{
		ID:          "t-orphan",
		Prompt:      "现在项目处于哪个进度",
		ProjectPath: dir,
		WorktreePath: dir,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 复刻现场：ACP 路径抛出的 approval 卡正挂着，进程随后被自重启杀掉。
	if _, err := store.Update(task.ID, func(x *model.Task) bool {
		x.Status = model.TaskWaitingInput
		x.CurrentDecision = approvalDecision("call_00_S5RsS4cEc7FlwAiIymmh9240")
		return true
	}); err != nil {
		t.Fatalf("set waiting_input(approval): %v", err)
	}

	tr.ResumeInterruptedWithInitiator([]string{task.ID}, "67e681c2a6dd", "")

	got, ok := store.Get(task.ID)
	if !ok || got == nil {
		t.Fatal("任务消失")
	}
	// 断言 1：不能再停在 waiting_input —— 那正是"卡片可点"的来源。
	if got.Status == model.TaskWaitingInput {
		t.Fatal("孤儿审批卡仍停在 waiting_input：前端会继续渲染一张" +
			"点了必然 409 的卡片（本次事故的现象）")
	}
	// 断言 2：卡片必须被摘掉。只改 status 不清 decision，前端照样有卡可渲染。
	if got.CurrentDecision != nil {
		t.Fatalf("CurrentDecision 未清空（%+v）：UI 仍会渲染这张卡，"+
			"而后端已无 pending 可应答 ⇒ 用户点批准只会拿到 409", got.CurrentDecision)
	}
	// 断言 3：落终态并给出可行动的说明，而不是无声无息。
	if got.Status != model.TaskFailed {
		t.Fatalf("status=%s, want failed（owner 已死且不可续，应清算成终态）", got.Status)
	}
	if got.Error == "" {
		t.Error("Error 为空：用户无从得知这轮为何结束、下一步该做什么")
	}
}

// TestResumeInterrupted_ChoiceCardSurvives 反向守护：路径 B（choice）**不能被误伤**。
//
// 它与 approval 长得很像（都是 waiting_input + 非空 decision），但进程本就
// end_turn 退出，是**合法持久态** —— 用户隔天仍可继续选。修复若粗暴地
// "清掉所有 waiting_input 的卡"，就会毁掉这个跨天可续的能力。
func TestResumeInterrupted_ChoiceCardSurvives(t *testing.T) {
	tr, store, _, _ := newTestTaskRunner(t)

	dir := t.TempDir()
	task, err := store.Create(&model.Task{
		ID: "t-choice", Prompt: "p", ProjectPath: dir, WorktreePath: dir,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.Update(task.ID, func(x *model.Task) bool {
		x.Status = model.TaskWaitingInput
		x.CurrentDecision = &model.Decision{ID: "d-choice", Kind: model.DecisionKindChoice}
		return true
	}); err != nil {
		t.Fatalf("set: %v", err)
	}

	tr.ResumeInterruptedWithInitiator([]string{task.ID}, "sha", "")

	got, _ := store.Get(task.ID)
	if got == nil || got.CurrentDecision == nil {
		t.Fatal("路径 B 的选项卡被误清：choice 进程本就 end_turn 退出，" +
			"是合法持久态，必须保留供用户隔天继续选")
	}
	if got.CurrentDecision.Kind != model.DecisionKindChoice {
		t.Fatalf("decision kind=%s, want choice", got.CurrentDecision.Kind)
	}
}

// TestStoreLoad_OrphanedApprovalClearedOnRestart ★ load() 侧的兜底。
//
// 覆盖"卡片产生于交接单快照之后"这个**本次事故的真实时序**：任务压根不在
// interruptedByRestart 名单里，走 load() 的常规"进程被重启打断"分支。
// 修复前该分支只改 status、**不清 CurrentDecision** ⇒ 僵尸卡照旧留在盘上。
func TestStoreLoad_OrphanedApprovalClearedOnRestart(t *testing.T) {
	dir := t.TempDir()

	// ① 模拟旧进程：写出一个 waiting_input(approval) 的任务文件。
	s1, err := NewTaskStore(dir)
	if err != nil {
		t.Fatalf("store1: %v", err)
	}
	if _, err := s1.Create(&model.Task{
		ID: "t2", Prompt: "p", ProjectPath: dir, WorktreePath: dir,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s1.Update("t2", func(x *model.Task) bool {
		x.Status = model.TaskWaitingInput
		x.CurrentDecision = approvalDecision("call_x")
		return true
	}); err != nil {
		t.Fatalf("set: %v", err)
	}

	// ② 模拟新进程 load：**不传** interrupted（该卡不在快照里，正是事故时序）。
	s2, err := NewTaskStore(dir)
	if err != nil {
		t.Fatalf("store2: %v", err)
	}
	got, ok := s2.Get("t2")
	if !ok || got == nil {
		t.Fatal("任务丢失")
	}
	if got.CurrentDecision != nil {
		t.Fatalf("load 后 CurrentDecision 仍非空（%+v）：磁盘上留着一张"+
			"没有 owner 的审批卡，重启后前端会渲染它，点击必然 409", got.CurrentDecision)
	}
	if got.Status == model.TaskWaitingInput {
		t.Fatalf("status 仍为 waiting_input，want failed")
	}
}

// TestStoreLoad_HandoffTerminalTaskCardCleared 覆盖名单内的**第三种**形状：
// 任务在接续名单里，但状态是终态（failed/completed），却还挂着一张 approval 卡。
//
// 为什么这种形状也需要兜底：ResumeInterrupted 第一关就要求
// status ∈ {running, waiting_input}，终态任务直接被 continue 掉 —— 没有任何人
// 会来收这张卡。而 UI 认的是"有没有 CurrentDecision"，不看 status，于是照样
// 渲染出一个可点的批准按钮，点了必然 409。
//
// 真实来源：任务被 failTask（清 decision）与并发写回之间交错、或手工/早期版本
// 留下的脏数据。不变量与前两条完全一致：**没有 owner 的审批卡不许留在盘上**。
func TestStoreLoad_HandoffTerminalTaskCardCleared(t *testing.T) {
	dir := t.TempDir()

	s1, err := NewTaskStore(dir)
	if err != nil {
		t.Fatalf("store1: %v", err)
	}
	if _, err := s1.Create(&model.Task{ID: "t4", Prompt: "p", ProjectPath: dir}); err != nil {
		t.Fatalf("create: %v", err)
	}
	// 终态 + 残留 approval 卡（脏数据形状）。
	if _, err := s1.Update("t4", func(x *model.Task) bool {
		x.Status = model.TaskFailed
		x.Error = "boom"
		x.CurrentDecision = approvalDecision("call_z")
		return true
	}); err != nil {
		t.Fatalf("set: %v", err)
	}

	s2, err := NewTaskStore(dir, "t4") // 在接续名单里
	if err != nil {
		t.Fatalf("store2: %v", err)
	}
	got, _ := s2.Get("t4")
	if got == nil {
		t.Fatal("任务丢失")
	}
	if got.CurrentDecision != nil {
		t.Fatalf("终态任务仍挂着 approval 卡（%+v）：接续流程不会处理它（状态不在"+
			"running/waiting_input），前端却会渲染出可点按钮 ⇒ 点了必然 409", got.CurrentDecision)
	}
	// 终态本身不该被改动。
	if got.Status != model.TaskFailed {
		t.Fatalf("status=%s, want failed（兜底只该摘卡，不该重写终态）", got.Status)
	}
}
// 的处理：**必须原样交给接续流程**，不能在这里就清算掉。
//
// 为什么不能：接续流程（ResumeInterrupted）才是决定"接续还是清算"的地方，
// 它在 load 之后才跑。若 load 抢先把它标 failed，接续逻辑第一关
// （status 必须是 running/waiting_input）就把它滤掉，等于回退成
// "重启后被判死"的老问题（那正是 99cbafb 修的事故）。
func TestStoreLoad_HandoffKeepsApprovalForResume(t *testing.T) {
	dir := t.TempDir()

	s1, err := NewTaskStore(dir)
	if err != nil {
		t.Fatalf("store1: %v", err)
	}
	if _, err := s1.Create(&model.Task{ID: "t3", Prompt: "p", ProjectPath: dir}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s1.Update("t3", func(x *model.Task) bool {
		x.Status = model.TaskWaitingInput
		x.CurrentDecision = approvalDecision("call_y")
		return true
	}); err != nil {
		t.Fatalf("set: %v", err)
	}

	// 明确把它列进接续名单。
	s2, err := NewTaskStore(dir, "t3")
	if err != nil {
		t.Fatalf("store2: %v", err)
	}
	got, _ := s2.Get("t3")
	if got == nil {
		t.Fatal("任务丢失")
	}
	if got.Status != model.TaskWaitingInput {
		t.Fatalf("status=%s, want waiting_input：名单内的任务必须原样交给接续流程，"+
			"在那里统一决定接续还是清算", got.Status)
	}
	if got.CurrentDecision == nil {
		t.Fatal("名单内任务的 decision 被 load 抢先清掉了：接续流程再也看不到这张卡")
	}
}
