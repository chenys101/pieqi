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
)

// DeltaPayload task_delta 事件携带的增量载荷（M2 真流式）。
// 仅含本次增量文本与是否思考，不含完整 task，避免前端全量重绘打断逐字渲染。
type DeltaPayload struct {
	Text      string `json:"text"`
	IsThought bool   `json:"is_thought,omitempty"` // true=思考过程，false=回答正文
}

// Event 任务状态变更事件，由 TaskRunner 发布，WS 层订阅转发。
//
// task_delta 事件只填 Delta（Task 为 nil）；task_updated 等事件只填 Task（Delta 为 nil）。
// 两者通过 Type 区分，互不破坏。
type Event struct {
	Type   string        `json:"type"` // "task_updated" | "task_created" | "task_deleted" | "task_delta"
	TaskID string        `json:"task_id"`
	Task   *model.Task   `json:"task,omitempty"`
	Delta  *DeltaPayload `json:"delta,omitempty"` // 仅 task_delta 事件填充
}

// EventBus 任务事件的 fan-out。订阅者慢时不阻塞发布者（丢弃积压）。
type EventBus struct {
	mu          sync.RWMutex
	subscribers map[uint64]chan Event
	nextID      uint64
}

// NewEventBus 创建事件总线。
func NewEventBus() *EventBus {
	return &EventBus{subscribers: make(map[uint64]chan Event)}
}

// Subscribe 订阅事件，返回订阅句柄与接收 channel。
// buf 为 channel 缓冲大小；缓冲满时后续事件被丢弃。
func (b *EventBus) Subscribe(buf int) *Subscription {
	if buf <= 0 {
		buf = 32
	}
	id := atomic.AddUint64(&b.nextID, 1)
	ch := make(chan Event, buf)
	b.mu.Lock()
	b.subscribers[id] = ch
	b.mu.Unlock()
	return &Subscription{id: id, ch: ch}
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

// Publish 向所有订阅者广播事件。非阻塞：缓冲满则丢弃该订阅者的事件。
func (b *EventBus) Publish(e Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subscribers {
		select {
		case ch <- e:
		default:
			// 订阅者落后，丢弃以防拖慢发布者
		}
	}
}

// Subscription 订阅句柄。
type Subscription struct {
	id uint64
	ch chan Event
}

// Chan 返回事件接收 channel。
func (s *Subscription) Chan() <-chan Event { return s.ch }
