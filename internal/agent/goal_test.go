package agent

import (
	"context"
	"testing"
)

// TestSessionBackedAdapterGoalPassthrough 回归：goal 事件必须透传到 GoalReporter。
//
// 背景（真实缺口）：Claude Agent SDK 会发 `active_goal` 事件
// （{condition, iterations, set_at, tokens_at_start, last_reason?}，null = goal 清除），
// 但桥的 session.js 消息 switch 只处理 system/stream_event/assistant/user，
// **没有 active_goal 分支** —— 于是用户设了 `/goal <条件>` 后，迭代次数、
// "为什么还没完成"、goal 是否已结束全部不可见，而且**一行日志都不会报**。
//
// 本测试钉住整条链路的 Go 侧一半：EventGoalChanged → GoalReporter 回调。
// 桥侧那一半由 services/claude-sdk-bridge/test/goal_event_test.mjs 覆盖。
func TestSessionBackedAdapterGoalPassthrough(t *testing.T) {
	fake := &fakeSession4Adapter{id: "claude-goal"}
	adapter := newSessionBackedAdapter("claude", openFake("claude", fake))
	if _, err := adapter.NewSession(context.Background(), SessionConfig{Cwd: "/tmp"}); err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	var got []*GoalSnapshot
	adapter.OnGoalUpdate(func(g *GoalSnapshot) { got = append(got, g) })

	// ① 设定 goal
	fake.fire(Event{Kind: EventGoalChanged, Goal: &GoalSnapshot{
		Condition:     "所有 e2e 用例通过",
		Iterations:    0,
		SetAt:         1792240915904,
		TokensAtStart: 12000,
	}})
	// ② Stop hook 判未满足，迭代 +1 并带原因
	fake.fire(Event{Kind: EventGoalChanged, Goal: &GoalSnapshot{
		Condition:     "所有 e2e 用例通过",
		Iterations:    1,
		SetAt:         1792240915904,
		TokensAtStart: 12000,
		LastReason:    "layout 仍有 1 例失败",
	}})
	// ③ goal 被清除（nil 是**有效载荷**，不是"没数据"）
	fake.fire(Event{Kind: EventGoalChanged, Goal: nil})

	if len(got) != 3 {
		t.Fatalf("goal 回调次数 = %d，期望 3（设定 / 迭代 / 清除 各一次）", len(got))
	}
	if got[0] == nil || got[0].Condition != "所有 e2e 用例通过" || got[0].Iterations != 0 {
		t.Fatalf("第一条 goal 快照不对: %+v", got[0])
	}
	if got[1] == nil || got[1].Iterations != 1 || got[1].LastReason != "layout 仍有 1 例失败" {
		t.Fatalf("第二条 goal 快照不对: %+v", got[1])
	}
	// 这一条最容易写错：若 adapter 把 nil 当作"没有变化"而跳过，用户就永远看不到 goal 结束。
	if got[2] != nil {
		t.Fatalf("goal 清除必须以 nil 送达（收到 %+v）—— 否则前端无法渲染\"goal 已结束\"", got[2])
	}
}

// TestSessionBackedAdapterImplementsGoalReporter 类型断言守卫。
//
// WireGoal 对 adapter 做 `adapter.(agent.GoalReporter)` 断言；TaskRunner 持有的是
// sessionBackedAdapter 而不是底层 AgentSession。本类型若漏实现该方法，断言失败 ->
// WireGoal 返回 nil -> goal 事件静默消失（**日志里一切正常**）——
// 这是"链路中段少一环"式的静默失效，用直接实现接口的 fake 抓不到。
func TestSessionBackedAdapterImplementsGoalReporter(t *testing.T) {
	var a AgentAdapter = newSessionBackedAdapter("claude", nil)
	if _, ok := a.(GoalReporter); !ok {
		t.Fatal("sessionBackedAdapter 未实现 GoalReporter：WireGoal 会返回 nil，goal 状态静默丢失")
	}
}
