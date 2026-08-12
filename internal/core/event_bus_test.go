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
