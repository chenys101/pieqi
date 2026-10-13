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

// Publish 必须给每条事件分配**全局单调**序号：客户端靠它判断自己漏没漏
// （见 Event.Seq 的说明）。序号必须严格 +1 递增 —— 有跳号会让客户端误判为丢事件，
// 重号或回退则会让它漏判。
func TestEventBus_PublishAssignsMonotonicSeq(t *testing.T) {
	bus := NewEventBus()
	sub := bus.Subscribe(16)
	defer bus.Unsubscribe(sub)

	for i := 0; i < 5; i++ {
		bus.Publish(Event{Type: "task_delta", TaskID: "t"})
	}

	var want uint64 = 1
	for i := 0; i < 5; i++ {
		select {
		case ev := <-sub.Chan():
			if ev.Seq != want {
				t.Fatalf("event %d: Seq = %d, want %d", i, ev.Seq, want)
			}
			want++
		case <-time.After(time.Second):
			t.Fatalf("event %d not received", i)
		}
	}
}

// 序号跨任务共享一条序列（订阅关系本身就是全局的，见 Event.Seq 的设计取舍）。
// 这条测试把该取舍钉住：若将来改成"每任务独立序号"，客户端只需一个 lastSeq
// 的简化假设就不再成立，必须同步改 SeqTracker。
func TestEventBus_SeqIsGlobalAcrossTasks(t *testing.T) {
	bus := NewEventBus()
	sub := bus.Subscribe(16)
	defer bus.Unsubscribe(sub)

	bus.Publish(Event{Type: "task_delta", TaskID: "a"})
	bus.Publish(Event{Type: "task_delta", TaskID: "b"})

	first := <-sub.Chan()
	second := <-sub.Chan()
	if first.Seq != 1 || second.Seq != 2 {
		t.Fatalf("seq 应为跨任务的全局序列 1,2，实得 %d,%d", first.Seq, second.Seq)
	}
}

// 调用方自带 Seq 时必须尊重（测试造数据用），不能被覆盖。
func TestEventBus_ExplicitSeqRespected(t *testing.T) {
	bus := NewEventBus()
	sub := bus.Subscribe(8)
	defer bus.Unsubscribe(sub)

	bus.Publish(Event{Type: "x", TaskID: "t", Seq: 999})
	if ev := <-sub.Chan(); ev.Seq != 999 {
		t.Fatalf("Seq = %d, want 999（显式序号应被尊重）", ev.Seq)
	}
}

// Seq() 反映已分配的最大序号；WithSeqHeld 在持锁下读到同一读数。
// 两者是快照告知客户端"你收到的最后一条是第几号"的依据。
func TestEventBus_SeqReadable(t *testing.T) {
	bus := NewEventBus()
	if got := bus.Seq(); got != 0 {
		t.Fatalf("初始 Seq = %d, want 0", got)
	}
	sub := bus.Subscribe(8)
	defer bus.Unsubscribe(sub)
	bus.Publish(Event{Type: "x"})
	bus.Publish(Event{Type: "y"})
	if got := bus.Seq(); got != 2 {
		t.Fatalf("Seq = %d, want 2", got)
	}

	var held uint64
	bus.WithSeqHeld(func(seq uint64) { held = seq })
	if held != 2 {
		t.Fatalf("WithSeqHeld 读到 %d, want 2", held)
	}
}

// WithSeqHeld 必须与 Publish 互斥：持有期间不能有新事件插入，
// 否则快照报出的 seq 与它随后的接收范围不衔接 —— 客户端会漏判（见方法注释）。
func TestEventBus_WithSeqHeldExcludesPublish(t *testing.T) {
	bus := NewEventBus()
	sub := bus.Subscribe(16)
	defer bus.Unsubscribe(sub)

	bus.Publish(Event{Type: "x"}) // seq=1

	// 在持锁回调里读到的 seq，必须与此刻"已广播出去的事件数"一致：
	// 若 Publish 能插入，这里就会读到比已入队事件更大的值。
	var seqInLock uint64
	var queuedWhenLocked int
	bus.WithSeqHeld(func(seq uint64) {
		seqInLock = seq
		queuedWhenLocked = len(sub.Chan())
	})
	if seqInLock != 1 || queuedWhenLocked != 1 {
		t.Fatalf("持锁时 seq=%d 已入队=%d，两者应一致（均为 1）", seqInLock, queuedWhenLocked)
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
