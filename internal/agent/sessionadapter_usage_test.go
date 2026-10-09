package agent

import (
	"testing"
	"time"
)

// 回归（2026-10-09 实测踩到）：**用量在 sessionBackedAdapter 这一层被静默丢掉**。
//
// 现象：线上日志里 `acp usage update {"used":...}` 一直在打（ACP 层完全正常），
// 但任务文件里 `usage` 永远为空。根因：TaskRunner 持有的 adapter 是
// sessionBackedAdapter，而 WireUsage 对它做 `.(UsageReporter)` 断言 ——
// 那个类型当时**没实现 OnUsageUpdate**，断言失败 => WireUsage 返回 nil => 永不落库。
//
// 为什么原先的单测抓不到：那些用例用**直接实现 UsageReporter 的 fake** 打桩，
// 恰好绕过了"真实 adapter 到底有没有实现这个接口"这一环。
// 本文件专门守那一环 —— 判据是**真实类型的接口满足性**，不是行为。
//
// 这类"链路中段少一环"的失效有共同特征：**上游一切正常、下游永远收不到**，
// 且日志里看不出任何异常。所以必须由类型层面的断言来守，而不是靠端到端观察。

// TestSessionBackedAdapterImplementsUsageReporter 守护本层必须转发用量。
//
// 一旦有人删掉/改掉 OnUsageUpdate，这里立刻红 —— 而不是等到线上发现
// "用量怎么一直是空的"。
func TestSessionBackedAdapterImplementsUsageReporter(t *testing.T) {
	var a any = newSessionBackedAdapter("test", nil)
	reporter, ok := a.(UsageReporter)
	if !ok {
		t.Fatal("sessionBackedAdapter 未实现 UsageReporter —— " +
			"WireUsage 的类型断言会失败并返回 nil，用量将静默永不落库")
	}
	// 接口满足还不够，注册后必须真的被调用。
	called := make(chan UsageInfo, 1)
	reporter.OnUsageUpdate(func(u UsageInfo) { called <- u })

	a.(*sessionBackedAdapter).emitUsage(UsageInfo{SessionID: "s1", Used: 42, Size: 1000})

	select {
	case got := <-called:
		if got.Used != 42 || got.Size != 1000 {
			t.Errorf("usage=%+v want Used=42 Size=1000", got)
		}
	case <-time.After(time.Second):
		t.Fatal("OnUsageUpdate 注册的回调未被触发 —— 转发链断了")
	}
}

// TestSessionAdapterBridgesUsageToNeutralEvent 验证**中性层**（sessionAdapter）也转发用量。
//
// 两层都要转发，缺一不可：
//
//	ACPAgent --usage--> sessionAdapter(中性 EventUsage) --Event--> sessionBackedAdapter --usage--> WireUsage
//
// 只补一层的话另一层照样断 —— 当初正是只想到 ACP 层、漏了桥接层。
func TestSessionAdapterBridgesUsageToNeutralEvent(t *testing.T) {
	fake := &usageReporterFake{fakeAdapter: &fakeAdapter{done: make(chan struct{})}}
	s := NewSessionAdapter(fake, "sess-1", Caps{})

	var got []Event
	s.OnEvent(func(ev Event) {
		if ev.Kind == EventUsage {
			got = append(got, ev)
		}
	})

	fake.fireUsage(UsageInfo{Used: 7, Size: 100})
	if len(got) != 1 {
		t.Fatalf("got %d usage events, want 1（sessionAdapter 未把用量桥接成中性事件）", len(got))
	}
	if got[0].Usage == nil {
		t.Fatal("Event.Usage is nil；用量载荷丢了")
	}
	if got[0].Usage.Used != 7 || got[0].Usage.Size != 100 {
		t.Errorf("usage=%+v want Used=7 Size=100", *got[0].Usage)
	}
}

// TestSessionAdapterNoUsageReporterIsSilent 验证底层不支持上报时**不 panic、不报错**。
//
// 判据：claude 的桥会话就是这一类（不上报用量）。这条路径必须是安静的，
// 而不是错误 —— 否则每个 claude 任务都会刷一条无意义告警。
func TestSessionAdapterNoUsageReporterIsSilent(t *testing.T) {
	// fakeAdapter 未实现 UsageReporter；注册回调时类型断言会失败，必须安全跳过。
	s := NewSessionAdapter(&fakeAdapter{done: make(chan struct{})}, "sess-1", Caps{})
	s.OnEvent(func(Event) {}) // 不得 panic
}

// --- 测试替身 ---

// usageReporterFake 给现有 fakeAdapter 加上 UsageReporter 能力（其余行为复用）。
// 用嵌入而不是重写一遍：本文件要守的是"类型实现与否"这条边界，
// 替身的行为细节越少越好。
type usageReporterFake struct {
	*fakeAdapter
	usageFn UsageUpdateFunc
}

func (f *usageReporterFake) OnUsageUpdate(fn UsageUpdateFunc) { f.usageFn = fn }

func (f *usageReporterFake) fireUsage(u UsageInfo) {
	if f.usageFn != nil {
		f.usageFn(u)
	}
}

// 编译期断言：这条能力边界正是本文件要守的东西。
var (
	_ AgentAdapter  = (*fakeAdapter)(nil)     // 基础替身：**不**报用量
	_ UsageReporter = (*usageReporterFake)(nil) // 加了能力的替身
	_ AgentAdapter  = (*usageReporterFake)(nil)
)
