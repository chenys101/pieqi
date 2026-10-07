package core

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"pieqi/internal/model"
)

// --- turnQueue 单元测试（队列不变式） ---

// TestTurnQueue_FIFO 按 push 顺序执行，且不重叠：每个 job 记录"进入/离开"序号，
// 离开序号必须紧跟自己进入的那一个（相邻 = 串行）。
func TestTurnQueue_FIFO(t *testing.T) {
	q := &turnQueue{}
	var mu sync.Mutex
	var order []int
	done := make(chan struct{})

	const n = 6
	for i := 0; i < n; i++ {
		i := i
		q.push(func() {
			mu.Lock()
			order = append(order, i)
			mu.Unlock()
			time.Sleep(5 * time.Millisecond) // 拉长重叠窗口：不串行就必然乱序/重叠
			if i == n-1 {
				close(done)
			}
		})
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("queue did not drain")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(order) != n {
		t.Fatalf("executed %d jobs, want %d", len(order), n)
	}
	for i, v := range order {
		if v != i {
			t.Fatalf("execution order = %v, want FIFO [0..%d]", order, n-1)
		}
	}
	waitFor(t, time.Second, "queue idle", func() bool { return q.idle() })
}

// TestTurnQueue_Serialized 并发 push 大量 job：峰值并发必须是 1（这就是"排队"的定义）。
func TestTurnQueue_Serialized(t *testing.T) {
	q := &turnQueue{}
	var mu sync.Mutex
	inFlight, maxInFlight, total := 0, 0, 0

	const n = 40
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q.push(func() {
				mu.Lock()
				inFlight++
				if inFlight > maxInFlight {
					maxInFlight = inFlight
				}
				mu.Unlock()

				time.Sleep(time.Millisecond)

				mu.Lock()
				inFlight--
				total++
				mu.Unlock()
			})
		}()
	}
	wg.Wait()
	waitFor(t, 5*time.Second, "queue idle", func() bool { return q.idle() })

	mu.Lock()
	defer mu.Unlock()
	if total != n {
		t.Fatalf("executed %d jobs, want %d (一条都不能丢)", total, n)
	}
	if maxInFlight != 1 {
		t.Fatalf("max concurrent jobs=%d, want 1 (并发提交必须被串行化)", maxInFlight)
	}
}

// TestTurnQueue_PushAheadCount push 返回"前面还有几轮"：首条 0，在跑时提交的按序递增。
func TestTurnQueue_PushAheadCount(t *testing.T) {
	q := &turnQueue{}
	release := make(chan struct{})
	running := make(chan struct{})

	if ahead := q.push(func() {
		close(running)
		<-release
	}); ahead != 0 {
		t.Fatalf("first push ahead=%d, want 0 (立即开跑)", ahead)
	}
	<-running

	if ahead := q.push(func() {}); ahead != 1 {
		t.Fatalf("second push ahead=%d, want 1 (前面有一轮在跑)", ahead)
	}
	if ahead := q.push(func() {}); ahead != 2 {
		t.Fatalf("third push ahead=%d, want 2 (在跑 1 + 排队 1)", ahead)
	}
	close(release)
	waitFor(t, 2*time.Second, "queue idle", func() bool { return q.idle() })
}

// TestTurnQueue_Flush flush 丢弃尚未开跑的排队轮，已在跑的那轮不受影响。
func TestTurnQueue_Flush(t *testing.T) {
	q := &turnQueue{}
	release := make(chan struct{})
	running := make(chan struct{})
	var mu sync.Mutex
	var ran []string

	q.push(func() {
		close(running)
		<-release
		mu.Lock()
		ran = append(ran, "running")
		mu.Unlock()
	})
	<-running
	q.push(func() { mu.Lock(); ran = append(ran, "queued-1"); mu.Unlock() })
	q.push(func() { mu.Lock(); ran = append(ran, "queued-2"); mu.Unlock() })

	if n := q.flush(); n != 2 {
		t.Fatalf("flush dropped %d, want 2", n)
	}
	close(release)
	waitFor(t, 2*time.Second, "queue idle", func() bool { return q.idle() })

	mu.Lock()
	defer mu.Unlock()
	if len(ran) != 1 || ran[0] != "running" {
		t.Fatalf("ran=%v, want only [running]（排队轮必须被丢弃）", ran)
	}
}

// TestTurnQueue_FlushThenPush flush 之后新提交的照常执行（取消不影响之后的重新发起）。
func TestTurnQueue_FlushThenPush(t *testing.T) {
	q := &turnQueue{}
	release := make(chan struct{})
	running := make(chan struct{})
	ran := make(chan string, 4)

	q.push(func() { close(running); <-release; ran <- "first" })
	<-running
	q.push(func() { ran <- "dropped" })
	q.flush()
	q.push(func() { ran <- "after-flush" })
	close(release)

	select {
	case got := <-ran:
		if got != "first" {
			t.Fatalf("first ran=%q, want first", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first job did not finish")
	}
	select {
	case got := <-ran:
		if got != "after-flush" {
			t.Fatalf("second ran=%q, want after-flush (flush 只丢已排队的)", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("after-flush job did not run")
	}
	waitFor(t, time.Second, "queue idle", func() bool { return q.idle() })
}

// --- TaskRunner 层：并发 Resume 排队（方案②）端到端 ---

// TestTaskRunner_ACP_ConcurrentResume_Serialized 一次连发 n 条续问（模拟连点发送 /
// IM 连发 / 重复提交）：
//
// 老行为：n-1 条撞上 AgentManager 的"同 task 单 Run"约束 → failTask → 正在跑的任务
// 被打成 failed（赢的那轮跑完也被终态守卫拦住，任务永远 failed）。
// 新行为：n 条全部入队按序执行 —— 任务 completed，峰值并发 SendPrompt == 1，
// n 条 user 事件一条不丢。
func TestTaskRunner_ACP_ConcurrentResume_Serialized(t *testing.T) {
	const n = 8
	// delay 拉长每轮时长，保证 n 条提交都落在"首轮还在跑"的窗口里（否则开局那条可能已跑完，
	// 窗口消失后排队提示就不再必然出现）。
	tr, store, _, fake := newACPTestRunner(t, fakeScript{deltaText: "hello", delay: 50 * time.Millisecond}, false)
	task := createACPTestTask(t, store)
	tr.Start(context.Background(), task)
	waitFor(t, 5*time.Second, "first turn started", func() bool {
		return fake.runCount() > 0 && fake.adapter(task.ID) != nil
	})

	adapter := waitACPAdapter(t, fake, task.ID)
	runsBefore := fake.runCount()

	// 同时提交 n 条续问：全部必须被接受（同步返回 nil），无一被拒。
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = tr.Resume(task.ID, fmt.Sprintf("q%d", i), "")
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("Resume #%d rejected: %v（并发提交只应排队，不应报错）", i, err)
		}
	}

	// 等队列排空：所有 job 已返回 → 无并发 store 写，可安全 Get 断言。
	waitFor(t, 30*time.Second, "turn queue drained", func() bool {
		return tr.turnQueueFor(task.ID).idle() && fake.runCount() == runsBefore+n
	})

	got := getTask(t, store, task.ID)
	if got.Status != model.TaskCompleted {
		t.Fatalf("status=%s (err=%q), want completed —— 并发提交绝不能把任务判死", got.Status, got.Error)
	}
	if got.Error != "" {
		t.Fatalf("task error=%q, want empty", got.Error)
	}
	// 串行化的直接证据：任意时刻只有一个 SendPrompt 在跑。
	if max := adapter.maxConcurrentPrompts(); max != 1 {
		t.Fatalf("max concurrent SendPrompt=%d, want 1（排队必须串行）", max)
	}
	if fake.runCount() != runsBefore+n {
		t.Fatalf("run calls=%d, want %d (n 条都要跑)", fake.runCount(), runsBefore+n)
	}
	// n 条 user 事件都落库（一条都不能被吞）。
	seen := map[string]bool{}
	for _, ev := range got.Events {
		if ev.Type == model.EventUser {
			seen[ev.Text] = true
		}
	}
	for i := 0; i < n; i++ {
		if !seen[fmt.Sprintf("q%d", i)] {
			t.Fatalf("user event q%d missing: %+v", i, got.Events)
		}
	}
	// 事件落库数量 = 首发 prompt + n 条续问 + 若干排队提示。
	queuedNotes := 0
	for _, ev := range got.Events {
		if ev.Type == model.EventStatus && strings.Contains(ev.Text, "已排队") {
			queuedNotes++
		}
	}
	if queuedNotes == 0 {
		t.Fatalf("并发提交应有可见的排队回执，events=%+v", got.Events)
	}
	if fake.Adapter(task.ID) == nil {
		t.Fatalf("ACP 会话应保持保活")
	}
}

// TestTaskRunner_ACP_ResumeDuringRunningQueues 一轮还在跑时提交续问：不报错、不打断，
// 排在当前轮之后执行（running 现在也可 Resume —— 排队语义）。
func TestTaskRunner_ACP_ResumeDuringRunningQueues(t *testing.T) {
	// block=true：首轮阻塞到 ctx 取消，便于构造"正在跑"的时刻。
	tr, store, _, fake := newACPTestRunner(t, fakeScript{deltaText: "hello", block: true}, false)
	task := createACPTestTask(t, store)
	tr.Start(context.Background(), task)

	adapter := waitACPAdapter(t, fake, task.ID)
	<-adapter.sendPromptStarted // 首轮已进入 SendPrompt（task 已是 running）

	if got := getTaskStatus(t, store, task.ID); got != model.TaskRunning {
		t.Fatalf("status=%s, want running（前提：首轮在跑）", got)
	}

	// 首轮在跑时提交续问：必须被接受（排队），不再返回 "not resumable: running"。
	if err := tr.Resume(task.ID, "while-running", ""); err != nil {
		t.Fatalf("Resume during running rejected: %v（应当排队而不是拒绝）", err)
	}
	if n := tr.turnQueueFor(task.ID).pendingCount(); n != 1 {
		t.Fatalf("pending turns=%d, want 1（续问应排在当前轮之后）", n)
	}
	// 排队提示对用户可见。
	if !hasEvent(t, store, task.ID, model.EventStatus, "已排队") {
		t.Fatalf("missing 已排队 status event")
	}

	// 取消当前轮：Cancel 同时丢弃排队轮（停止就是停止），排队的那条不应再跑。
	if err := tr.Cancel(task.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	waitFor(t, 3*time.Second, "queue idle after cancel", func() bool {
		return tr.turnQueueFor(task.ID).idle()
	})
	waitFor(t, 3*time.Second, "status cancelled", func() bool {
		tt, ok := store.Get(task.ID)
		return ok && tt != nil && tt.Status == model.TaskCancelled
	})
	if got := fake.runCount(); got != 1 {
		t.Fatalf("run calls=%d, want 1（被取消的排队轮不得开跑）", got)
	}
}

// TestTaskRunner_ACP_SessionBusyNotFatal 兜底：会话真被并发占用（ErrSessionBusy）时
// 轮内退避重试，而不是 failTask 把任务判死。
func TestTaskRunner_ACP_SessionBusyNotFatal(t *testing.T) {
	tr, store, _, fake := newACPTestRunner(t, fakeScript{deltaText: "hello"}, false)
	task := createACPTestTask(t, store)
	tr.Start(context.Background(), task)
	waitRunACPDone(t, store, task.ID)
	runsBefore := fake.runCount()

	// 下一次 Run 返回会话忙；退避后重试应成功。
	fake.setBusyN(1)
	if err := tr.Resume(task.ID, "after-busy", ""); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	waitFor(t, 5*time.Second, "busy retry turn finished", func() bool {
		return tr.turnQueueFor(task.ID).idle() && fake.runCount() == runsBefore+2
	})

	got := getTask(t, store, task.ID)
	if got.Status != model.TaskCompleted {
		t.Fatalf("status=%s (err=%q), want completed —— 会话忙必须退避重试而不是判死", got.Status, got.Error)
	}
	if fake.runCount() != runsBefore+2 {
		t.Fatalf("run calls=%d, want %d (1 次撞忙 + 1 次重试成功)", fake.runCount(), runsBefore+2)
	}
}

// TestTaskRunner_ACP_QueuedTurnSkippedOnDeletedTask 排队期间任务被删除：本轮直接放弃，
// 不复活已删任务的会话（否则会留下跑在孤儿 worktree 里的 agent 进程）。
func TestTaskRunner_ACP_QueuedTurnSkippedOnDeletedTask(t *testing.T) {
	tr, store, _, fake := newACPTestRunner(t, fakeScript{deltaText: "hello", block: true}, false)
	task := createACPTestTask(t, store)
	tr.Start(context.Background(), task)

	adapter := waitACPAdapter(t, fake, task.ID)
	<-adapter.sendPromptStarted
	runsBefore := fake.runCount()

	// 首轮在跑时提交续问（排队），随后删任务。
	if err := tr.Resume(task.ID, "queued-then-deleted", ""); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if err := tr.store.Delete(task.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// 放行阻塞的首轮，让队列推进到排队的那条。
	_ = tr.agentMgr.Close(task.ID)

	waitFor(t, 5*time.Second, "queued turn dropped", func() bool {
		return tr.turnQueueFor(task.ID).idle()
	})
	if got := fake.runCount(); got != runsBefore {
		t.Fatalf("run calls=%d, want %d（已删任务的排队轮不得开跑）", got, runsBefore)
	}
}

// --- 小工具 ---

// TestTaskRunner_ACP_ResumeColdStartHasReceipt 终态续问冷启动（无活会话，需重新 spawn
// agent 并 session/load 重建上下文）时必须先落一条可见的 status 回执。
//
// 回归的是 2026-10-06 的线上缺陷：intervene 对终态续问乐观返回 202（投递进队列即回
// resumed:true），真正的 spawn 开销完全落在后台。实测 dsh 隔夜续问 spawn→initialize
// 耗时 79s（同机热会话 1.9s），这段静默里既无 delta 也无 status，界面上就是"消息发出去了
// 但永远转圈"，被用户读成"无法续话"。
func TestTaskRunner_ACP_ResumeColdStartHasReceipt(t *testing.T) {
	tr, store, _, fake := newACPTestRunner(t, fakeScript{deltaText: "hello"}, false)
	task := createACPTestTask(t, store)

	// 首轮跑到终态：completed 后 runACPTurn 保活会话（adapter 仍在册）。
	tr.Start(context.Background(), task)
	waitRunACPDone(t, store, task.ID)
	if got := getTaskStatus(t, store, task.ID); got != model.TaskCompleted {
		t.Fatalf("首轮 status=%s, want completed", got)
	}

	// 构造"隔夜冷续问"：会话已被回收（adapter 摘除），只剩持久化的 session id 可 resume。
	// 这正是线上场景——空闲回收器收掉进程后，续问必须重新 spawn。
	_ = tr.agentMgr.Close(task.ID)
	waitFor(t, 2*time.Second, "adapter gone", func() bool { return fake.adapter(task.ID) == nil })

	if err := tr.Resume(task.ID, "冷续问", ""); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	// 关键断言：Resume 返回时（即 API 回 202 的那一刻）回执必须已经落库，
	// 否则用户在那个时间点上看到的就是一片空白。
	if !hasEvent(t, store, task.ID, model.EventStatus, "正在恢复会话") {
		t.Fatalf("冷续问必须先落可见回执，events=%+v", getTask(t, store, task.ID).Events)
	}

	waitFor(t, 30*time.Second, "queue drained", func() bool {
		return tr.turnQueueFor(task.ID).idle()
	})
}

// TestTaskRunner_ACP_ResumeWarmSessionNoReceipt 热会话复用（adapter 仍活，不重新 spawn、
// 不 LoadSession）几乎瞬时，不应产生"正在恢复会话"噪音 —— 与 noteQueued 同一取舍：
// 只有真的会让用户等待的路径才发回执。
func TestTaskRunner_ACP_ResumeWarmSessionNoReceipt(t *testing.T) {
	tr, store, _, fake := newACPTestRunner(t, fakeScript{deltaText: "hello"}, false)
	task := createACPTestTask(t, store)

	tr.Start(context.Background(), task)
	waitRunACPDone(t, store, task.ID)

	if fake.adapter(task.ID) == nil {
		t.Fatalf("前提不成立：保活语义下 adapter 应仍在册（热会话）")
	}
	if err := tr.Resume(task.ID, "热续问", ""); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if hasEvent(t, store, task.ID, model.EventStatus, "正在恢复会话") {
		t.Fatalf("热会话复用不应有冷启动回执（噪音）")
	}

	waitFor(t, 30*time.Second, "queue drained", func() bool {
		return tr.turnQueueFor(task.ID).idle()
	})
}

// getTaskStatus 读状态（仅在无并发写时调用）。
func getTaskStatus(t *testing.T, store *TaskStore, taskID string) model.TaskStatus {
	t.Helper()
	tt, ok := store.Get(taskID)
	if !ok || tt == nil {
		t.Fatalf("task %s not found", taskID)
	}
	return tt.Status
}

// hasEvent 判断是否存在满足类型 + 文本包含关系的任务事件（仅在无并发写时调用）。
func hasEvent(t *testing.T, store *TaskStore, taskID string, typ model.TaskEventType, contains string) bool {
	t.Helper()
	tt, ok := store.Get(taskID)
	if !ok || tt == nil {
		return false
	}
	for _, ev := range tt.Events {
		if ev.Type == typ && strings.Contains(ev.Text, contains) {
			return true
		}
	}
	return false
}
