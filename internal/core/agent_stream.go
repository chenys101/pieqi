// Package core 的流式连接器：把 AgentAdapter 的 OnContentDelta 回调接到 EventBus + 任务持久化。
//
// 这是 M2（真流式落地）的核心：ACP AgentMessageChunk.Content.Text 增量 → 安静持久化进 task
// → 只 Publish task_delta（轻量，不带完整 Task）→ WS → 前端逐字追加，替换 Phase 1 在
// appendEvent 里 200ms 合并后整块 task_updated 的路径。
//
// 设计要点（避免逐 delta 全量重绘）：
//   - 用安静的 appendTextDelta 持久化（只更 store，不 Publish task_updated），
//     不复用 TaskRunner.appendEvent（它会经 transition→task_updated 带完整 task 全量推送）。
//   - 只 Publish task_delta（带 Delta 载荷），前端增量追加 DOM，不触发 render()/renderDetail()。
//   - task_updated 仍只在状态变更（setRunning/completeTask/failTask/waiting_input 等）时发，
//     那时完整 task 已含累积文本，全量重绘也一致。
//
// 依赖方向：core → agent（单向）。internal/agent 不 import internal/core，无循环。
package core

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"pieqi/internal/agent"
	"pieqi/internal/model"
)

// DeltaHandle WireContentDelta 返回的句柄，Unwire 可拆卸回调（断开 adapter↔bus 连接）。
type DeltaHandle struct {
	adapter agent.AgentAdapter
	wire    *contentDeltaWire
}

// Unwire 注销回调（置空），断开 adapter 到 EventBus 的增量推送，并**落盘残留缓冲**。幂等。
// 任务结束/取消时调用，避免后续误触发的增量再写入已终态 task。
//
// 必须先 Flush 再拆回调：FlushDeltaWindow 内的待落盘文本还没进 store，
// 直接拆会让最后 200ms 的正文永久丢失（内存里没了、磁盘上也没写）。
func (h *DeltaHandle) Unwire() {
	if h == nil {
		return
	}
	if h.wire != nil {
		h.wire.Flush()
		h.wire = nil
	}
	if h.adapter == nil {
		return
	}
	h.adapter.OnContentDelta(nil)
	h.adapter = nil
}

// Flush 立刻把待落盘的增量写进 store（幂等；无待落盘内容时是空操作）。
func (h *DeltaHandle) Flush() {
	if h == nil || h.wire == nil {
		return
	}
	h.wire.Flush()
}

// WireContentDelta 把一个 AgentAdapter 的 OnContentDelta 回调接到 EventBus + 任务持久化。
//
// 注册后，adapter 每产出一个内容增量（AgentMessageChunk=回答 / AgentThoughtChunk=思考），
// 回调内：
//  1. 把增量塞进**合并缓冲**（不立刻落盘，见 FlushDeltaWindow 的说明）；
//  2. 立刻 Publish task_delta（带 Delta，不带完整 Task），前端逐字追加 ——
//     推送路径不受合并窗口影响，实时性不变。
//
// 落盘由两条路径保证：窗口到期（200ms），或调用方显式 Flush / Unwire（任务结束时）。
//
// 返回 DeltaHandle，调用方在任务结束/取消时 Unwire 拆卸（Unwire 会先落盘）。
func WireContentDelta(adapter agent.AgentAdapter, bus *EventBus, store *TaskStore, taskID string) *DeltaHandle {
	w := &contentDeltaWire{
		bus:         bus,
		store:       store,
		taskID:      taskID,
		flushWindow: FlushDeltaWindow,
	}
	adapter.OnContentDelta(w.onDelta)
	return &DeltaHandle{adapter: adapter, wire: w}
}

// contentDeltaWire 持有 OnContentDelta 回调所需的依赖。taskID 固定（一个 wire 对应一个 task）。
type contentDeltaWire struct {
	bus    *EventBus
	store  *TaskStore
	taskID string

	// flushWindow 落盘合并窗口（见 FlushDeltaWindow 的说明）。0 = 每个增量立刻落盘。
	flushWindow time.Duration

	// mu 保护下面的待落盘缓冲。onDelta 由 agent 适配器的 goroutine 调用，
	// Flush 可能由任务结束路径（另一个 goroutine）调用，两者会并发。
	mu sync.Mutex
	// pending 是待落盘的增量队列，**按到达顺序**保存且只做相邻同类型合并。
	//
	// 为什么必须是"有序队列"而不是"按类型分两个 builder"：持久化的语义是
	// "末事件同类型就追加、否则新建"（见 appendChunk），也就是**事件块的边界
	// 由类型交替决定**。text→thinking→text 必须落成 3 个 event（时间线上
	// 思考块夹在两段回答之间）。按类型分装会把它们压成 2 个，两段回答黏在一起、
	// 思考跑到前面 —— 内容不丢但顺序错了，而顺序错在时间线上就是错的。
	//
	// 只合并**相邻**同类型：与不缓冲时逐条落盘的结果完全一致（后者也是靠
	// appendChunk 把连续同类型并进末事件），所以缓冲不改变任何持久化语义，
	// 只是把 N 次 store.Update 合成 1 次。
	pending []pendingChunk
	// flushTimer 是合并窗口的定时器；非 nil 表示已排定一次落盘。
	flushTimer *time.Timer

	// flushN 累计**实际落盘次数**。
	//
	// 存在的意义是让"合并确实减少了写盘"成为可观测、可断言的事实。
	// 若只看最终内容是否正确，那么合并完全失效（退化成每个增量落盘一次）的回归
	// 也能通过测试 —— 而写放大正是本次要修的东西。
	flushN atomic.Int64
}

// pendingChunk 一段待落盘的同类文本。
type pendingChunk struct {
	isThought bool
	text      string
}

// flushCount 返回累计落盘次数（测试与诊断用）。
func (w *contentDeltaWire) flushCount() int64 { return w.flushN.Load() }

// FlushDeltaWindow 是流式增量的**落盘**合并窗口。
//
// 为什么需要：appendTextDelta 原先对每个增量都调用 store.Update，而 Update 会在
// 全局写锁内 marshal 整个 task（含全部 events）+ 写文件 + rename —— 代价是
// **O(事件总数) 每增量**，任务越长每次落盘越慢。后果不只是慢：
//   1. 写锁被长时间持有，与 List()（WS 快照要用）串行 → 快照阻塞 → 连接被心跳超时关掉；
//   2. 客户端因此断线，断线期间的事件又被静默丢弃（见 Event.Seq 的说明）→ 详情不刷新。
// 这是个正反馈：任务越大越容易断线、断了就越丢事件。
//
// 200ms 与 TaskRunner.appendEvent 的文本合并窗口一致 —— 两处都是"减少事件数与写盘"，
// 用同一个数免得出现两套手感。事件推送（task_delta）**不受此窗口影响**，仍然逐字实时。
//
// 为什么不取更长（如 1s）：这只影响**落盘**，不影响推送，所以取值的唯一约束是
// "进程被杀时最多丢多久的正文"。200ms 让最坏丢失量小到用户无法察觉，同时已足以把
// "每字符一次落盘"（实测 ACP 增量可达每 10–30ms 一条）压到每 200ms 一次，写放大降一个量级。
// 真正的正确性由 Flush 保证 —— 任务结束/取消时必定落盘，不依赖窗口到期。
const FlushDeltaWindow = 200 * time.Millisecond

// onDelta OnContentDelta 回调实现：入合并缓冲 + 立刻推送 task_delta。
// SessionID（agent.ContentDelta.SessionID）忽略——一个 wire 绑定一个 task，持久化以 taskID 为准。
func (w *contentDeltaWire) onDelta(d agent.ContentDelta) {
	if d.Text == "" {
		return
	}
	w.buffer(d.Text, d.IsThought)
	w.bus.Publish(Event{
		Type:   EventTaskDelta,
		TaskID: w.taskID,
		Delta:  &DeltaPayload{Text: d.Text, IsThought: d.IsThought},
	})
}

// buffer 把增量攒进待落盘队列，并确保窗口到期后会落盘。
func (w *contentDeltaWire) buffer(text string, isThought bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	// 与末块同类型则并入（等价于逐条落盘时 appendChunk 的合并），否则新开一块。
	if n := len(w.pending); n > 0 && w.pending[n-1].isThought == isThought {
		w.pending[n-1].text += text
	} else {
		w.pending = append(w.pending, pendingChunk{isThought: isThought, text: text})
	}
	if w.flushTimer == nil {
		w.flushTimer = time.AfterFunc(w.flushWindow, w.Flush)
	}
}

// Flush 把缓冲里的增量写进 store。幂等；无待落盘内容时是空操作。
//
// 由三条路径调用：窗口定时器到期、DeltaHandle.Unwire（任务结束）、以及测试直接调用。
// 因为会被多个 goroutine 触发，内部靠 mu + 取走队列保证"同一段文本只落盘一次"。
func (w *contentDeltaWire) Flush() {
	w.mu.Lock()
	if w.flushTimer != nil {
		w.flushTimer.Stop()
		w.flushTimer = nil
	}
	chunks := w.pending
	w.pending = nil
	w.mu.Unlock()

	if len(chunks) == 0 {
		return
	}
	w.persist(chunks)
}

// persist 把一批增量按序写进 task 的事件流与 Output。一次 Update = 一次落盘。
func (w *contentDeltaWire) persist(chunks []pendingChunk) {
	w.flushN.Add(1)
	now := time.Now()
	var textTotal strings.Builder
	_, _ = w.store.Update(w.taskID, func(t *model.Task) bool {
		for _, c := range chunks {
			if c.isThought {
				w.appendChunk(t, model.EventThinking, c.text, now)
				continue
			}
			w.appendChunk(t, model.EventText, c.text, now)
			// 非思考增量追加到 Output（回答正文累积；思考过程不计入）
			textTotal.WriteString(c.text)
		}
		if s := textTotal.String(); s != "" {
			t.Output += s
		}
		return true
	})
}

// appendChunk 把一个同类型的文本块并进末事件；末事件类型不同（或为空）则新建。
//
// 与 TaskRunner.appendEvent 的 200ms 合并路径互斥：本方法专供 ACP 流式路径，
// 不经 transition，不 Publish task_updated，故不会触发前端全量重绘。
func (w *contentDeltaWire) appendChunk(t *model.Task, evType model.TaskEventType, text string, now time.Time) {
	if len(t.Events) > 0 && t.Events[len(t.Events)-1].Type == evType {
		// 累积到最后一个同类型 event（连续同类型增量合并到同一块）
		last := &t.Events[len(t.Events)-1]
		last.Text += text
		last.At = now
		return
	}
	// 最后一个 event 类型不同（或空）：新建一个同类型 event
	// （Seq 分配与保留上限裁剪统一由 AppendEvent 负责）
	w.store.AppendEvent(t, model.TaskEvent{
		Type: evType,
		Text: text,
		At:   now,
	})
}
