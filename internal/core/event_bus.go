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
func (b *EventBus) Publish(e Event) {
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
