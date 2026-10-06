package core

import (
	"testing"
	"time"
)

func TestEventBus_PublishSubscribe(t *testing.T) {
	bus := NewEventBus()
	sub := bus.Subscribe(8)
	defer bus.Unsubscribe(sub)

	bus.Publish(Event{Type: "task_updated", TaskID: "t1"})

	select {
	case ev := <-sub.Chan():
		if ev.Type != "task_updated" || ev.TaskID != "t1" {
			t.Fatalf("got %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("did not receive event")
	}
}

func TestEventBus_MultipleSubscribers(t *testing.T) {
	bus := NewEventBus()
	s1 := bus.Subscribe(8)
	s2 := bus.Subscribe(8)
	defer bus.Unsubscribe(s1)
	defer bus.Unsubscribe(s2)

	bus.Publish(Event{Type: "x", TaskID: "t2"})

	for i, s := range []*Subscription{s1, s2} {
		select {
		case <-s.Chan():
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d missed event", i)
		}
	}
}

func TestEventBus_Unsubscribe(t *testing.T) {
	bus := NewEventBus()
	sub := bus.Subscribe(8)
	bus.Unsubscribe(sub)

	// Unsubscribe 后再 Publish 不应 panic，且 channel 已关闭
	bus.Publish(Event{Type: "x"})
	if _, ok := <-sub.Chan(); ok {
		t.Fatal("channel should be closed after Unsubscribe")
	}
}

func TestEventBus_SlowSubscriberDropsNotBlocks(t *testing.T) {
	bus := NewEventBus()
	sub := bus.Subscribe(2) // 小缓冲
	defer bus.Unsubscribe(sub)

	// 发 5 个，缓冲只能装 2 个，余下应丢弃而非阻塞
	for i := 0; i < 5; i++ {
		bus.Publish(Event{Type: "x", TaskID: "t"})
	}

	// 至少能收到 2 个
	got := 0
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && got < 2 {
		select {
		case <-sub.Chan():
			got++
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	if got < 2 {
		t.Fatalf("got %d, want >=2", got)
	}
}

// 回归（2026-10-06「详情不刷新、输入框一直不可编辑」）：
// 丢弃必须**留痕**。早期实现静默 default 丢弃，订阅者永久落后却无从自知，
// 被丢掉的那条恰好是 task_completed → 前端卡在 running 视图。
func TestEventBus_DropIsObservable(t *testing.T) {
	bus := NewEventBus()
	sub := bus.Subscribe(1)
	defer bus.Unsubscribe(sub)

	// 第一个进缓冲，其余溢出被丢弃
	bus.Publish(Event{Type: "task_delta", TaskID: "t"})
	bus.Publish(Event{Type: "task_completed", TaskID: "t"})

	if !sub.Dropped() {
		t.Fatal("溢出后 Dropped() 应为 true —— 消费者无从得知丢过事件")
	}
	if !sub.TakeDropped() {
		t.Fatal("TakeDropped() 应报告并清除标记")
	}
	// 已消费：不重复报告（否则每轮都重同步，变成周期性全量推送）
	if sub.TakeDropped() {
		t.Fatal("TakeDropped() 消费后不应重复返回 true")
	}
}

// 缓冲够用时不得误报丢弃（否则 WS 会周期性无谓重发快照）。
func TestEventBus_NoDropWhenBuffered(t *testing.T) {
	bus := NewEventBus()
	sub := bus.Subscribe(4)
	defer bus.Unsubscribe(sub)

	bus.Publish(Event{Type: "task_updated", TaskID: "t"})

	if sub.TakeDropped() {
		t.Fatal("未溢出却报告丢弃")
	}
}

// 丢弃只影响落后的那个订阅者，不得牵连跟得上的订阅者。
func TestEventBus_DropIsPerSubscriber(t *testing.T) {
	bus := NewEventBus()
	slow := bus.Subscribe(1)
	fast := bus.Subscribe(64)
	defer bus.Unsubscribe(slow)
	defer bus.Unsubscribe(fast)

	for i := 0; i < 5; i++ {
		bus.Publish(Event{Type: "task_delta", TaskID: "t"})
	}

	if !slow.TakeDropped() {
		t.Fatal("慢订阅者应报告丢弃")
	}
	if fast.TakeDropped() {
		t.Fatal("快订阅者不应报告丢弃")
	}
}
