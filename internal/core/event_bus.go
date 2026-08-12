package core

import (
	"sync"
	"sync/atomic"

	"claude-bridge/internal/model"
)

// Event 任务状态变更事件，由 TaskRunner 发布，WS 层订阅转发。
type Event struct {
	Type   string       `json:"type"` // "task_updated" | "task_created" | "task_deleted"
	TaskID string       `json:"task_id"`
	Task   *model.Task  `json:"task,omitempty"`
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
