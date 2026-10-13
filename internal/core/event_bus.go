package core

import (
	"sync"
	"sync/atomic"

	"pieqi/internal/model"
)

// 事件类型常量。
const (
	// EventTaskDelta 内容增量事件（M2 真流式）：ACP AgentMessageChunk/AgentThoughtChunk
	// 增量逐字推送。携带 Delta（轻量），不带完整 Task，前端增量追加而非全量重绘。
	EventTaskDelta = "task_delta"

	// EventTaskUsage 上下文用量更新（轻量）：ACP usage_update 到达时推送。
	//
	// 为什么单开一个事件而不是复用 task_updated：usage_update 一轮内会到达**多次**
	// （每条 assistant message 一次），而 task_updated 带完整 Task —— 那会让前端在
	// 逐字渲染期间被反复全量重绘，正是 agent_stream.go 顶部记着的那个坑。
	// 这里只推 Used/Size 两个数，前端就地更新一个小角标。
	EventTaskUsage = "task_usage"
)

// DeltaPayload task_delta 事件携带的增量载荷（M2 真流式）。
// 仅含本次增量文本与是否思考，不含完整 task，避免前端全量重绘打断逐字渲染。
type DeltaPayload struct {
	Text      string `json:"text"`
	IsThought bool   `json:"is_thought,omitempty"` // true=思考过程，false=回答正文
}

// UsagePayload task_usage 事件携带的用量载荷（轻量，与 DeltaPayload 同理）。
// 字段集与 model.TaskUsage 一致，但**不含时间戳**：那是持久化才需要的事实，
// 前端只关心"现在多满"。
type UsagePayload struct {
	Used    int     `json:"used"`
	Size    int     `json:"size"`
	CostUSD float64 `json:"cost_usd,omitempty"`
	HasCost bool    `json:"has_cost,omitempty"`
}

// Event 任务状态变更事件，由 TaskRunner 发布，WS 层订阅转发。
//
// task_delta / task_usage 只填各自的轻量载荷（Task 为 nil）；
// task_updated 等事件只填 Task（载荷为 nil）。通过 Type 区分，互不破坏。
type Event struct {
	Type   string        `json:"type"` // "task_updated" | "task_created" | "task_deleted" | "task_delta" | "task_usage"
	TaskID string        `json:"task_id"`
	Task   *model.Task   `json:"task,omitempty"`
	Delta  *DeltaPayload `json:"delta,omitempty"` // 仅 task_delta 事件填充
	Usage  *UsagePayload `json:"usage,omitempty"` // 仅 task_usage 事件填充

	// Seq 是**全局单调递增**的事件序号，由 Publish 在广播时统一分配。
	//
	// 为什么需要它（客户端断线无法自知）：task_delta 是纯增量、不带状态，
	// 一旦客户端在断开期间漏收，它**没有任何办法察觉**——载荷里没有 seq，
	// 重连后的 snapshot 又是轻量视图（不含 events）。于是时间线永久缺一段。
	//
	// 设计取舍：seq 是**全局**（跨任务）而非每任务独立。理由是订阅关系也是全局的
	// （一条 WS 连接订阅整个 EventBus），客户端只需维护一个 lastSeq 就能发现
	// "我漏了东西"，不必按任务维护 N 个游标。代价是无法直接用它做按任务增量补拉
	// —— 但那本就不是它的用途：发现 gap 后走的是"重拉该任务完整详情"
	// （持久化事件流才是事实源），seq 只负责把"静默丢失"变成"可发现"。
	Seq uint64 `json:"seq"`
}

// EventBus 任务事件的 fan-out。订阅者慢时不阻塞发布者（丢弃积压）。
//
// ⚠️ 丢弃**必须留痕**：早期实现在缓冲满时静默 `default:` 丢弃，订阅者从此
// 永久落后且无从自知。生产表现（2026-10-06）：重连瞬间服务端要先推全量快照，
// 前端解析期间订阅者必然满 —— 恰好把那次 `task_completed` 丢掉，于是
// 「详情不刷新、输入框一直不可编辑，刷新一下才对」，而磁盘上任务早已完成。
//
// 现在丢弃会置位订阅者的 dropped 标记，消费者据此重新同步（见 TakeDropped）。
type EventBus struct {
	mu          sync.RWMutex
	subscribers map[uint64]*Subscription
	nextID      uint64
	// seq 是全局单调事件序号，Publish 时原子递增并写入 Event.Seq（见 Event.Seq 的说明）。
	seq atomic.Uint64
}

// NewEventBus 创建事件总线。
func NewEventBus() *EventBus {
	return &EventBus{subscribers: make(map[uint64]*Subscription)}
}

// Subscribe 订阅事件，返回订阅句柄与接收 channel。
// buf 为 channel 缓冲大小；缓冲满时后续事件被丢弃，但会在订阅句柄上留下
// dropped 标记（消费者可用 TakeDropped 发现并重新同步）。
func (b *EventBus) Subscribe(buf int) *Subscription {
	if buf <= 0 {
		buf = 32
	}
	id := atomic.AddUint64(&b.nextID, 1)
	ch := make(chan Event, buf)
	s := &Subscription{id: id, ch: ch}
	b.mu.Lock()
	b.subscribers[id] = s
	b.mu.Unlock()
	return s
}

// Unsubscribe 取消订阅。
func (b *EventBus) Unsubscribe(s *Subscription) {
	if s == nil {
		return
	}
	b.mu.Lock()
	delete(b.subscribers, s.id)
	b.mu.Unlock()
	close(s.ch)
}

// Publish 向所有订阅者广播事件。非阻塞：缓冲满则丢弃该订阅者的事件，
// 并在该订阅者上置 dropped 标记（供其自行重同步）。
//
// 序号在**这里**分配而不是由发布方填：发布点有十几处（状态机、权限、流式、用量…），
// 让每处都记得填 seq 必然会漏，而漏掉的表现是"客户端误判为丢事件"或"漏判"，
// 都是静默的。收口到唯一的广播入口，才可能保证"每条出去的事件都有号"。
func (b *EventBus) Publish(e Event) {
	// 调用方自带 Seq 时尊重它（测试造数据用），否则分配。
	if e.Seq == 0 {
		e.Seq = b.seq.Add(1)
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, s := range b.subscribers {
		select {
		case s.ch <- e:
		default:
			// 订阅者落后，丢弃以防拖慢发布者。置位标记，绝不静默。
			s.dropped.Store(true)
		}
	}
}

// Seq 返回当前已分配的最大事件序号。
//
// ⚠️ 只读快照，**不保证**与调用方构建的快照内容严格衔接：读出这个数之后新发布的事件
// 仍可能落在快照视野之外。用于"告知客户端大致进度"是够的（客户端有 gap 检测兜底，
// 最坏是多触发一次无害的补拉）；需要严格无洞的场景用 WithSeqHeld。
func (b *EventBus) Seq() uint64 { return b.seq.Load() }

// WithSeqHeld 在**持锁状态**下执行 fn，fn 拿到当前已分配的最大序号。
//
// 存在的唯一理由：消掉"快照内容"与"快照所报 seq"之间的竞态。
//
// 快照要报出"此刻已分配到第几号"，客户端据此判断断开期间漏没漏。若读 seq 与读任务列表
// 不是原子的，两步之间新发布的事件就落在快照视野之外 —— 客户端比对后认为"没漏"，
// 而那条事件它其实永远收不到（它落在订阅建立之前）。这是最隐蔽的一种丢事件：
// 连 gap 检测都发现不了，因为它压根不在客户端看到的序号序列里。
//
// 把「读 seq」与「读任务列表」放进同一把锁（与 Publish 互斥），两者之间就不可能有
// 新事件插入：客户端拿到的 seq 与它随后的接收范围严格衔接。
//
// ⚠️ fn 在锁内运行，**必须极轻**（只读内存、不做 IO）—— 它会阻塞所有发布者，
// 而发布者在 agent 流式路径上每个增量都要经过。
func (b *EventBus) WithSeqHeld(fn func(seq uint64)) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	fn(b.seq.Load())
}

// Subscription 订阅句柄。
type Subscription struct {
	id uint64
	ch chan Event
	// dropped 由 Publish 在丢弃事件时置位（原子）；TakeDropped 读取并清除。
	dropped atomic.Bool
}

// Chan 返回事件接收 channel。
func (s *Subscription) Chan() <-chan Event { return s.ch }

// TakeDropped 报告自上次调用以来是否发生过丢弃，并清除标记。
//
// 消费者（WS 转发循环）应定期或在静默间隙调用；返回 true 时必须重新同步
// 全量状态，否则会永久停留在过期视图上。
func (s *Subscription) TakeDropped() bool {
	return s.dropped.Swap(false)
}

// Dropped 只读探测，不消费标记（供监控/测试使用）。
func (s *Subscription) Dropped() bool { return s.dropped.Load() }
