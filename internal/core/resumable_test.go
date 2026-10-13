package core

import (
	"testing"

	"pieqi/internal/model"
)

// 本文件守护 isResumable 的判定语义，直接对应 2026-10-13 的线上事故：
//
//	自重启交接时，发起者那一轮被判 waiting_input，但 CurrentDecision 尚未落盘
//	（快照竞态）⇒ ResumeRich 的 resumable 判定为 false ⇒ ResumeInterrupted
//	把它 failTask("自重启后自动接续失败：task not resumable: waiting_input")。
//
// 后果是**不可逆**的：任务被判 failed，用户必须人工介入；而它其实完全可以续。
// 更糟的是它精准命中"agent 正在等自己发起的重启"这个高频场景。
//
// 修复：decision 为空的 waiting_input 视为可续（本质仍是"在跑的一轮"，续问即排队）。
// 真正的路径 A（approval）一定有 decision，因为 setWaitingInput 是在**同一次
// transition** 里把 status 与 CurrentDecision 一起写的，二者不会分离。

// TestIsResumable_WaitingInputWithoutDecision 钉住本次修复的核心口子：
// waiting_input + CurrentDecision == nil ⇒ 可续（竞态/脏数据），不是"不可续"。
func TestIsResumable_WaitingInputWithoutDecision(t *testing.T) {
	task := &model.Task{ID: "t-race", Status: model.TaskWaitingInput}
	// 显式置空，复刻竞态：状态已落 waiting_input，decision 还没写进去。
	task.CurrentDecision = nil

	if !isResumable(task) {
		t.Fatal("waiting_input 且无 decision 必须判为可续（快照竞态）—— " +
			"判 false 会让自重启接续失败并把任务不可逆地标成 failed")
	}
}

// TestIsResumable_ApprovalStillBlocked 反向守护：**真正的路径 A 不能被放行**。
//
// 这条与上一条是一对：修复不能把口子开成"所有 waiting_input 都可续" ——
// approval 态进程还活着挂在 hook channel 上等决策，续问会撞上它，
// 用户应该去回答那张卡（走 Intervene），而不是发新消息。
func TestIsResumable_ApprovalStillBlocked(t *testing.T) {
	task := &model.Task{
		ID:     "t-approval",
		Status: model.TaskWaitingInput,
		CurrentDecision: &model.Decision{
			ID:   "d1",
			Kind: model.DecisionKindApproval,
		},
	}
	if isResumable(task) {
		t.Fatal("路径 A（approval）不可续：进程还挂在 hook channel 上等决策，" +
			"应走 Intervene 回答那张卡，而不是排一轮新输入")
	}
}

// TestIsResumable_ChoiceAllowed 路径 B（choice）必须保持可续 ——
// 这是 Resume 的既有契约（进程已 end_turn 退出，选完续跑）。
func TestIsResumable_ChoiceAllowed(t *testing.T) {
	task := &model.Task{
		ID:     "t-choice",
		Status: model.TaskWaitingInput,
		CurrentDecision: &model.Decision{
			ID:   "d2",
			Kind: model.DecisionKindChoice,
		},
	}
	if !isResumable(task) {
		t.Fatal("路径 B（choice）应可续：进程已 end_turn 退出，用户选完即续跑")
	}
}

// TestIsResumable_TerminalAndRunning 常规可续状态全覆盖。
func TestIsResumable_TerminalAndRunning(t *testing.T) {
	for _, st := range []model.TaskStatus{
		model.TaskCompleted, model.TaskFailed, model.TaskCancelled, model.TaskRunning,
	} {
		if !isResumable(&model.Task{ID: "t", Status: st}) {
			t.Errorf("status=%s 应可续", st)
		}
	}
}

// TestIsResumable_PendingNotResumable 未开始的任务不该被"续问"——
// 它没有可续的上下文，应让用户正常跑首轮。
func TestIsResumable_PendingNotResumable(t *testing.T) {
	if isResumable(&model.Task{ID: "t", Status: model.TaskPending}) {
		t.Fatal("pending 不可续：尚无会话上下文，应走正常首轮执行")
	}
}

// TestResumeInterrupted_TransientWaitingInputDoesNotFailTask 端到端复刻事故：
// 交接单里的任务处于 waiting_input 且无 decision，接续**不能**把它判 failed。
//
// 这是对"修复是否真的挡住了 failTask 那条不可逆路径"的最终断言 ——
// 上面几条测的是判定函数，这条测的是它在 ResumeInterrupted 里的实际后果。
func TestResumeInterrupted_TransientWaitingInputDoesNotFailTask(t *testing.T) {
	tr, store, _, _ := newTestTaskRunner(t)

	dir := t.TempDir()
	task, err := store.Create(&model.Task{
		ID:          "t-race",
		Prompt:      "现在项目处于哪个进度",
		ProjectPath: dir,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 直接落到竞态现场：状态是 waiting_input，但 decision 还没写进去。
	// WorktreePath 必须给（真实任务恒非空；空会被 Resume 的既有关卡拦下，
	// 那属于另一条错误路径，会掩盖本用例想验的东西）。
	if _, err := store.Update(task.ID, func(x *model.Task) bool {
		x.Status = model.TaskWaitingInput
		x.CurrentDecision = nil
		x.WorktreePath = dir
		return true
	}); err != nil {
		t.Fatalf("set waiting_input: %v", err)
	}

	// initiatorID 用 ""（人类 curl 场景）以免依赖 initiator 的额外断言。
	tr.ResumeInterruptedWithInitiator([]string{task.ID}, "deadbeefcafe", "")

	got, ok := store.Get(task.ID)
	if !ok || got == nil {
		t.Fatal("任务消失")
	}
	if got.Status == model.TaskFailed {
		t.Fatalf("任务被判 failed（err=%q）—— 这正是本次要修的事故："+
			"快照竞态导致的 waiting_input 不该让会话不可逆地死掉", got.Error)
	}
	if got.Error != "" {
		t.Errorf("不应写入错误信息，got %q", got.Error)
	}
}
