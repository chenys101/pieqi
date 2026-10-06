package core

import (
	"fmt"
	"sync"

	"pieqi/internal/model"

	"go.uber.org/zap"
)

// turnQueue 单任务串行的 turn 队列（FIFO；同一 task 任意时刻至多一轮在跑）。
//
// 为什么需要它：一轮 turn 是"提交即返回"的（API 立刻 202，实跑在 goroutine 里），
// 而同一 task 的 agent 会话**只允许一个 Run 在跑**：
//   - AgentManager.Run 并发第二个 → "prompt already running"；
//   - AgentManager.Open 并发第二个 → "session already open"。
//
// 用户连点两次"发送" / IM 连发两条时，第二个 turn 撞上第一个，走的是**失败**分支
// （runACPTurn 的 failTask / ensureACPSession 续问分支的 forceFailTask）——
// 于是"重复提交一次"就能把正在正常跑的任务打成 failed；而赢的那轮照常跑完，
// 终态→终态被 transition 拦截，任务永远停在 failed。
//
// 队列把同一 task 的 turn 串起来（排在后面而不是撞上去）：后提交的等前面的收尾再跑。
//
// 语义：
//   - FIFO：按 push 顺序执行，不并行。
//   - flush：丢弃**尚未开跑**的排队轮（Cancel / 删任务 = 停止，排队的就不该再跑）。
//     已在跑的那轮不在这里中断，仍由 AgentManager.Cancel 走原有路径打断。
//   - push 返回"前面还有几轮"（在跑的一轮 + 已排队的），供 UI 回执排队状态。
//
// 队列对象按 taskID 懒建且**不回收**：每个 struct 只有几十字节，终身保留可避免
// "idle 时删表、新旧队列并存"造成的双 drain 竞态（那会让串行化静默失效）。
type turnQueue struct {
	mu   sync.Mutex
	busy bool     // 有 drain goroutine 在跑（即至少有一轮已开跑）
	jobs []func() // 待执行队列（不含已在跑的那一轮）
}

// push 把一轮执行排到队尾，返回前面已排队的轮数（0 = 立即开跑）。
// 队列空闲时本调用会拉起唯一的 drain goroutine 消费队列。
func (q *turnQueue) push(job func()) int {
	q.mu.Lock()
	ahead := 0
	if q.busy {
		ahead = 1 + len(q.jobs) // 在跑的一轮 + 已排在它后面的
	}
	q.jobs = append(q.jobs, job)
	start := !q.busy
	if start {
		q.busy = true
	}
	q.mu.Unlock()
	if start {
		go q.drain()
	}
	return ahead
}

// drain 串行消费队列：一轮跑完再取下一轮；队空则复位 busy 并退出。
func (q *turnQueue) drain() {
	for {
		q.mu.Lock()
		if len(q.jobs) == 0 {
			q.jobs = nil
			q.busy = false
			q.mu.Unlock()
			return
		}
		job := q.jobs[0]
		q.jobs = q.jobs[1:]
		q.mu.Unlock()
		job() // 串行点：job 返回前不会取下一个
	}
}

// flush 丢弃尚未开跑的所有排队轮，返回丢弃条数。已在跑的那轮不受影响。
func (q *turnQueue) flush() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := len(q.jobs)
	q.jobs = nil
	return n
}

// idle 报告队列是否空闲（既没有在跑的轮，也没有排队的轮）。测试/诊断用。
func (q *turnQueue) idle() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return !q.busy && len(q.jobs) == 0
}

// pendingCount 报告尚未开跑的排队轮数（不含正在跑的那一轮）。测试/诊断用。
func (q *turnQueue) pendingCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.jobs)
}

// --- TaskRunner 接入 ---

// turnQueueFor 取（或懒建）taskID 的串行 turn 队列。
func (tr *TaskRunner) turnQueueFor(taskID string) *turnQueue {
	tr.turnMu.Lock()
	defer tr.turnMu.Unlock()
	if tr.turns == nil {
		tr.turns = make(map[string]*turnQueue)
	}
	q, ok := tr.turns[taskID]
	if !ok {
		q = &turnQueue{}
		tr.turns[taskID] = q
	}
	return q
}

// submitTurn 提交一轮执行到 task 的串行队列（见 turn_queue.go 顶部注释）。
//
// **所有"跑一轮"的入口（Start / Resume）都必须经它**，不要再裸 `go tr.run(...)`：
// "同一 task 任意时刻至多一轮在跑"这个保证由队列提供，绕过即回到并发互撞的老 bug。
// 返回前面已排队的轮数（0 = 立即开跑）；job 在独立 goroutine 中按序执行。
//
// job 收到的是**轮开始时刻**的 task 快照（而非调用方手里的旧副本）：排队等待期间
// ClaudeSessionID / WorktreePath / 状态都可能已被前一轮改写，用旧快照会拿错
// --resume 的 session id。任务在排队期间被删除时本轮直接放弃（不复活已删任务的会话）。
func (tr *TaskRunner) submitTurn(taskID string, job func(task *model.Task)) int {
	return tr.turnQueueFor(taskID).push(func() {
		cur, ok := tr.store.Get(taskID)
		if !ok || cur == nil {
			tr.logger.Debug("queued turn skipped: task gone", zap.String("task", taskID))
			return
		}
		job(cur)
	})
}

// flushTurns 丢弃 task 尚未开跑的排队轮（Cancel / 删任务：停止就是停止）。
// 返回丢弃条数。只读表不建表（避免给从未排过队的任务留下空队列）。
func (tr *TaskRunner) flushTurns(taskID string) int {
	tr.turnMu.Lock()
	q := tr.turns[taskID]
	tr.turnMu.Unlock()
	if q == nil {
		return 0
	}
	return q.flush()
}

// noteQueued 排队等待时给用户一条可见回执（ahead<=0 静默：立即开跑，无需解释）。
// 静默排队本身就是历史缺陷的放大版——用户连发两条时第二条的去向必须看得见。
func (tr *TaskRunner) noteQueued(taskID string, ahead int) {
	if ahead <= 0 {
		return
	}
	tr.appendEvent(taskID, model.TaskEvent{Type: model.EventStatus,
		Text: fmt.Sprintf("已排队：前面还有 %d 轮，轮到本条会自动继续", ahead)})
}

// noteResuming 续问冷启动时给用户一条可见回执。
//
// 为什么需要：intervene 对终态续问是**乐观返回 202** 的（internal/api/tasks.go 投递进
// 队列就回 resumed:true），真正耗时的 agent spawn 落在后台。续问要复用上下文就必须新
// spawn 一个进程，冷启动开销完全在这段静默里 —— 2026-10-06 实测 dsh 隔夜续问
// spawn→initialize 用了 79s（同机热会话只要 1.9s）。这段时间既无 delta 也无 status，
// 前端只会显示"消息已发出、内容一直转圈"，用户据此判定"无法续话"。
//
// cold=true（无活会话，需重新 spawn）才提示：热会话复用几乎瞬时，多一条噪音反而碍眼。
// 与 noteQueued 同一取舍——只有真的会让用户等待的路径才发回执。
func (tr *TaskRunner) noteResuming(taskID string, cold bool) {
	if !cold {
		return
	}
	tr.appendEvent(taskID, model.TaskEvent{Type: model.EventStatus,
		Text: "正在恢复会话…（冷启动需重建上下文，首次响应可能要等数十秒）"})
}
