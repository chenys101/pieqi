package core

import (
	"testing"
	"time"

	"pieqi/internal/model"
)

// 本文件守护「陈旧审批卡」不变量（2026-10-23，f74bcb09 会话 no pending 刷屏的服务端根因）。
//
// 现场：同一轮里前一条审批被批准后，agent **紧接着**发了下一条 RequestPermission，
// 于是 CurrentDecision 从 req-N 换成 req-N+1。用户界面上那张旧卡（req-N）还留着，
// 点下去后端 pending 表里查无此物 —— 日志里就是那 16 条
// "approval resolve FAILED: no pending permission for decision"。
//
// 不变量：**没有 pending owner 的 approval 卡不许留在任务上**。
// 与 orphan_approval_test.go 那条（重启导致 owner 已死）是同一个不变量的另一个入口：
// 那条管 load()/接续，这条管"轮次进行中换了卡 / 轮次结束"。

// setApprovalCard 把任务置成"展示着 id 这张 approval 卡"，createdAt 由调用方控制宽限期。
//
// 两处不能省：
//   - 宽限期写进**落盘的** create 入口（见 clearStaleDecision）；
//   - `:=` 不能写成 `=`：approvalDecision 返回指针，`x.CurrentDecision = approvalDecision(id)`
//     之后 `x.CurrentDecision.CreatedAt = ...` 改的仍是 store 内部那份对象，
//     后续 store.Update 会从它的快照克隆出旧形状，注入的 createdAt 会被悄悄丢掉。
//     实测踩过：测试里"10 分钟前"的卡在 store 里读回成了"刚设上"，用例因此假通过。
func setApprovalCard(t *testing.T, store *TaskStore, taskID, id string, createdAt time.Time) {
	t.Helper()
	if _, err := store.Update(taskID, func(x *model.Task) bool {
		x.Status = model.TaskWaitingInput
		d := approvalDecision(id)
		d.CreatedAt = createdAt
		x.CurrentDecision = d
		return true
	}); err != nil {
		t.Fatalf("set approval card: %v", err)
	}
	// 自检：前提真的成立了，否则下面的断言是空转。
	if got, _ := store.Get(taskID); got.CurrentDecision == nil || !got.CurrentDecision.CreatedAt.Equal(createdAt) {
		t.Fatalf("注入失败：卡的 createdAt 没落进 store（got %+v）", got.CurrentDecision)
	}
}

// registerTestWires 把一个已经建好的 PermissionWire 登记到 runner 上。
//
// 这**不是**可选的样板：clearStaleDecision 经 permWire 查 pending，没登记就查不到 → 保守
// TRUE → 什么都不清。第一次写这组用例时就踩了：断言失败的消息是"陈旧卡未被摘除"，
// 看起来像判据写反，实际是被测链路根本没接上。
func registerTestWires(t *testing.T, tr *TaskRunner, taskID string, ph *PermissionWire) {
	t.Helper()
	tr.setWires(taskID, nil, ph, nil, nil, nil)
	if tr.permWire(taskID) == nil {
		t.Fatal("wire 未登记成功：被测的清理链路在第一步就断了")
	}
}

// TestClearStaleDecision_ShadowedCardIsCleared 被下一条请求顶掉的旧卡必须被清掉。
//
// 判据是"宽限期已过 且 wire 里没有这条 pending"。服务端此刻展示的是新卡，
// 任务仍是 waiting_input —— 这里**只摘旧卡、不动状态**（新卡还在那儿等）。
func TestClearStaleDecision_ShadowedCardIsCleared(t *testing.T) {
	tr, store, bus, _ := newTestTaskRunner(t)
	sub := bus.Subscribe(64)
	defer bus.Unsubscribe(sub)

	task, err := store.Create(&model.Task{ID: "t-stale", Prompt: "p"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 旧卡：展示于 10 分钟前，早已超过宽限期。
	setApprovalCard(t, store, task.ID, "req-old", time.Now().Add(-10*time.Minute))

	// 挂一个 wire：里面只有**新**的 pending，没有 req-old。
	fa := newFakePermAdapter()
	ph := WirePermission(fa, bus, store, task.ID, nil, time.Minute, nil, nil)
	defer ph.Unwire()
	registerTestWires(t, tr, task.ID, ph)
	ph.mu.Lock()
	ph.pending["req-new"] = &permPending{reqID: "req-new"}
	ph.mu.Unlock()

	tr.clearStaleDecision(task.ID)

	got, _ := store.Get(task.ID)
	if got.CurrentDecision != nil {
		t.Fatalf("陈旧卡未被摘除（%+v）：它已经没有 pending owner，用户点它只能拿到 no pending",
			got.CurrentDecision)
	}
	// 状态不得被这次清理改写：任务仍在等**新**那张卡。
	if got.Status != model.TaskWaitingInput {
		t.Fatalf("status=%s, want waiting_input（清旧卡不该改变任务状态）", got.Status)
	}
	// 前端必须被告知，否则界面上那张死按钮不会消失。
	if tt := findTaskUpdated(t, drainEvents(sub, 200*time.Millisecond)); tt.CurrentDecision != nil {
		t.Fatalf("task_updated 仍带着旧卡: %+v", tt.CurrentDecision)
	}
}

// TestClearStaleDecision_LiveCardIsKept 反向守护：**正在等人回答**的那张卡绝不能被误清。
//
// 这就是宽限期存在的理由：onPermissionRequest 先登记 pending 再展示卡片（反之在
// show 里也是先写 CurrentDecision 再登记定时器），两个方向都有微秒级窗口 ——
// 用"此刻查不到"直接清卡会把用户正在看的那张卡凭空抹掉。
func TestClearStaleDecision_LiveCardIsKept(t *testing.T) {
	tr, store, bus, _ := newTestTaskRunner(t)

	task, err := store.Create(&model.Task{ID: "t-live", Prompt: "p"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	setApprovalCard(t, store, task.ID, "req-live", time.Now().Add(-10*time.Minute))

	fa := newFakePermAdapter()
	ph := WirePermission(fa, bus, store, task.ID, nil, time.Minute, nil, nil)
	defer ph.Unwire()
	registerTestWires(t, tr, task.ID, ph)
	ph.mu.Lock()
	ph.pending["req-live"] = &permPending{reqID: "req-live"}
	ph.mu.Unlock()

	tr.clearStaleDecision(task.ID)

	got, _ := store.Get(task.ID)
	if got.CurrentDecision == nil || got.CurrentDecision.ID != "req-live" {
		t.Fatalf("有人答的卡被误清了: %+v", got.CurrentDecision)
	}
}

// TestClearStaleDecision_RespectsGracePeriod 刚设上的卡（宽限期内）即使 pending 为 nil 也保留。
//
// 真实形态：hook 路径/竞态窗口里 CurrentDecision 已经写进去了，而 pending 还没登记。
func TestClearStaleDecision_RespectsGracePeriod(t *testing.T) {
	tr, store, _, _ := newTestTaskRunner(t)

	task, err := store.Create(&model.Task{ID: "t-grace", Prompt: "p"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	setApprovalCard(t, store, task.ID, "req-fresh", time.Now())

	tr.clearStaleDecision(task.ID)

	got, _ := store.Get(task.ID)
	if got.CurrentDecision == nil {
		t.Fatal("宽限期内的卡被误清：那是刚展示出来、用户正在看的卡")
	}
}

// TestClearStaleDecision_ChoiceCardUntouched 路径 B（choice）永远不归它管。
//
// choice 的"答案"是普通文本消息（走 Resume），不依赖 pending 表 ——
// 它本来就该在 pending 里查不到，且是**合法持久态**（隔天仍可继续选）。
func TestClearStaleDecision_ChoiceCardUntouched(t *testing.T) {
	tr, store, _, _ := newTestTaskRunner(t)

	task, err := store.Create(&model.Task{ID: "t-choice", Prompt: "p"})
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

	tr.clearStaleDecision(task.ID)

	got, _ := store.Get(task.ID)
	if got.CurrentDecision == nil {
		t.Fatal("choice 卡被误清：进程本就 end_turn 退出，pending 里查不到是正常形态")
	}
}

// TestClearStaleDecision_NoWireKeepsCard 保守侧：没有 wire 时无法证伪"没人等"，
// 一律当作可能还有人等 —— 宁可留一张卡，也不误清。
func TestClearStaleDecision_NoWireKeepsCard(t *testing.T) {
	tr, store, _, _ := newTestTaskRunner(t)

	task, err := store.Create(&model.Task{ID: "t-nowire", Prompt: "p"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	setApprovalCard(t, store, task.ID, "req-nowire", time.Now().Add(-time.Hour))

	tr.clearStaleDecision(task.ID)

	got, _ := store.Get(task.ID)
	if got.CurrentDecision == nil {
		t.Fatal("没有 wire 时不该清卡（保守 TRUE：无法证伪即视为有人等）")
	}
}
