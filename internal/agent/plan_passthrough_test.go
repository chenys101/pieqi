package agent

import (
	"context"
	"testing"
	"time"
)

// TestSessionBackedAdapterPlanPassthrough 回归：plan 事件必须透传到 PlanReporter。
//
// 背景（2026-11-01 任务 384e5205）：cursor/create_plan 扩展应答显示 accepted、
// entries=6，但 Task.Plan 始终为空 —— WirePlan 对 sessionBackedAdapter 做
// PlanReporter 断言失败后返回 nil，emitPlan 的 onPlan 回调从未挂上。
func TestSessionBackedAdapterPlanPassthrough(t *testing.T) {
	fake := &fakeSession4Adapter{id: "cursor-plan"}
	adapter := newSessionBackedAdapter("cursor", openFake("cursor", fake))
	if _, err := adapter.NewSession(context.Background(), SessionConfig{Cwd: "/tmp"}); err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	var got []PlanUpdate
	adapter.OnPlanUpdate(func(u PlanUpdate) { got = append(got, u) })

	fake.fire(Event{Kind: EventPlanChanged, SessionID: "cursor-plan", PlanEntries: []PlanEntry{
		{Content: "checkpoint 跳过 .pieqi", Status: "pending", Priority: "medium"},
		{Content: "审批超时冷却", Status: "in_progress", Priority: "high"},
	}})
	fake.fire(Event{Kind: EventPlanChanged, SessionID: "cursor-plan", PlanEntries: nil})

	if len(got) != 2 {
		t.Fatalf("plan 回调次数 = %d，期望 2（写入 / 清空）", len(got))
	}
	if len(got[0].Entries) != 2 || got[0].Entries[0].Content != "checkpoint 跳过 .pieqi" {
		t.Fatalf("第一条 plan 不对: %+v", got[0].Entries)
	}
	if got[0].Entries[1].Status != "in_progress" {
		t.Fatalf("进行中条目 status=%q", got[0].Entries[1].Status)
	}
	if len(got[1].Entries) != 0 {
		t.Fatalf("清空必须以空 entries 送达（收到 %+v）", got[1].Entries)
	}
}

// TestSessionBackedAdapterImplementsPlanReporter 类型断言守卫（对齐 Usage/Goal）。
func TestSessionBackedAdapterImplementsPlanReporter(t *testing.T) {
	var a any = newSessionBackedAdapter("cursor", nil)
	reporter, ok := a.(PlanReporter)
	if !ok {
		t.Fatal("sessionBackedAdapter 未实现 PlanReporter：WirePlan 会返回 nil，CreatePlan 静默丢清单")
	}
	called := make(chan PlanUpdate, 1)
	reporter.OnPlanUpdate(func(u PlanUpdate) { called <- u })
	a.(*sessionBackedAdapter).emitPlan(PlanUpdate{
		SessionID: "s1",
		Entries:   []PlanEntry{{Content: "x", Status: "pending"}},
	})
	select {
	case got := <-called:
		if len(got.Entries) != 1 || got.Entries[0].Content != "x" {
			t.Fatalf("got=%+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("OnPlanUpdate 回调未被触发")
	}
}

// TestSessionAdapterBridgesPlanToNeutralEvent 中性层也必须桥接 PlanReporter。
//
//	ACPAgent --plan--> sessionAdapter(EventPlanChanged) --Event--> sessionBackedAdapter --plan--> WirePlan
func TestSessionAdapterBridgesPlanToNeutralEvent(t *testing.T) {
	fake := &planReporterFake{fakeAdapter: &fakeAdapter{done: make(chan struct{})}}
	s := NewSessionAdapter(fake, "sess-1", Caps{})

	var got []Event
	s.OnEvent(func(ev Event) {
		if ev.Kind == EventPlanChanged {
			got = append(got, ev)
		}
	})

	fake.firePlan(PlanUpdate{
		SessionID: "sess-1",
		Entries:   []PlanEntry{{Content: "A", Status: "pending", Priority: "medium"}},
	})
	if len(got) != 1 {
		t.Fatalf("got %d plan events, want 1", len(got))
	}
	if len(got[0].PlanEntries) != 1 || got[0].PlanEntries[0].Content != "A" {
		t.Fatalf("PlanEntries=%+v", got[0].PlanEntries)
	}
}

type planReporterFake struct {
	*fakeAdapter
	planFn PlanUpdateFunc
}

func (f *planReporterFake) OnPlanUpdate(fn PlanUpdateFunc) { f.planFn = fn }

func (f *planReporterFake) firePlan(u PlanUpdate) {
	if f.planFn != nil {
		f.planFn(u)
	}
}
