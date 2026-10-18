package core

import (
	"encoding/json"
	"strings"
	"testing"

	"pieqi/internal/agent"
	"pieqi/internal/model"
)

// fakeGoalAdapter 测试用 AgentAdapter，额外实现 GoalReporter（可选能力）。
type fakeGoalAdapter struct {
	*fakeDeltaAdapter
	onGoal agent.GoalUpdateFunc
}

func newFakeGoalAdapter() *fakeGoalAdapter {
	return &fakeGoalAdapter{fakeDeltaAdapter: newFakeDeltaAdapter()}
}

func (f *fakeGoalAdapter) OnGoalUpdate(fn agent.GoalUpdateFunc) { f.onGoal = fn }

// emitGoal 手动触发已注册的 OnGoalUpdate 回调（模拟桥的 goal_changed 到达）。
func (f *fakeGoalAdapter) emitGoal(g *agent.GoalSnapshot) {
	if f.onGoal != nil {
		f.onGoal(g)
	}
}

func setupGoalWire(t *testing.T) (*fakeGoalAdapter, *Subscription, *TaskStore, string) {
	t.Helper()
	bus := NewEventBus()
	sub := bus.Subscribe(64)
	store, err := NewTaskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tt, err := store.Create(&model.Task{ProjectID: "p", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	fa := newFakeGoalAdapter()
	if WireGoal(fa, bus, store, tt.ID, nil) == nil {
		t.Fatal("WireGoal 返回 nil：fakeGoalAdapter 已实现 GoalReporter，不该拿不到句柄")
	}
	return fa, sub, store, tt.ID
}

// TestWireGoal_AppendsStatusEventAndPublishes 验证 goal 上报落成一条 status 事件并推送 task_updated。
//
// 为什么落成 EventStatus 而不是新事件类型：见 agent_goal.go 顶部（status 本就是
// "状态变更 + 人读摘要"，前端已能渲染；新开一种 TaskEventType 要连带改前端与视觉基线）。
func TestWireGoal_AppendsStatusEventAndPublishes(t *testing.T) {
	fa, sub, store, taskID := setupGoalWire(t)

	fa.emitGoal(&agent.GoalSnapshot{
		Condition:  "所有 e2e 用例通过",
		Iterations: 2,
		LastReason: "layout 仍有 1 例失败",
	})

	task, ok := store.Get(taskID)
	if !ok {
		t.Fatal("task not found")
	}
	var ev *model.TaskEvent
	for i := range task.Events {
		if task.Events[i].Type == model.EventStatus && strings.Contains(task.Events[i].Text, "goal") {
			ev = &task.Events[i]
			break
		}
	}
	if ev == nil {
		t.Fatalf("没有 goal 的 status 事件；events=%+v", task.Events)
	}
	// 摘要要点明"完成条件"这个语义（Claude Code 的 goal 是 condition，不是 objective）。
	if !strings.Contains(ev.Text, "完成条件") && !strings.Contains(ev.Text, "goal") {
		t.Errorf("摘要应包含 goal 语义，实际: %q", ev.Text)
	}
	if !strings.Contains(ev.Text, "所有 e2e 用例通过") {
		t.Errorf("摘要应包含 condition 原文，实际: %q", ev.Text)
	}
	if !strings.Contains(ev.Text, "2") || !strings.Contains(ev.Text, "layout 仍有 1 例失败") {
		t.Errorf("摘要应包含迭代次数与未满足原因，实际: %q", ev.Text)
	}

	// 结构化载荷必须可解析（将来做 goal 卡片直接读它，不用回改存储）。
	var payload map[string]any
	if err := json.Unmarshal(ev.Input, &payload); err != nil {
		t.Fatalf("Input 不是合法 JSON: %v (%s)", err, string(ev.Input))
	}
	if payload["condition"] != "所有 e2e 用例通过" {
		t.Errorf("载荷 condition=%v", payload["condition"])
	}

	// 必须推 task_updated（前端据此刷新时间线）。
	select {
	case e := <-sub.Chan():
		if e.Type != "task_updated" {
			t.Errorf("推送类型=%q want task_updated", e.Type)
		}
		if e.Task == nil {
			t.Error("task_updated 未带 Task（前端无法渲染）")
		}
	default:
		t.Error("没有推送 task_updated")
	}
}

// TestWireGoal_ClearedIsDistinguishable 回归：goal 清除必须与"没有事件"可区分。
//
// 若清除时不发事件，用户永远看不到"goal 已结束"；若发了但只靠空摘要，
// 又无法与"goal 摘要恰好为空"区分 —— 所以载荷里必须有明确的 cleared 标记。
func TestWireGoal_ClearedIsDistinguishable(t *testing.T) {
	fa, _, store, taskID := setupGoalWire(t)

	fa.emitGoal(nil) // nil = goal 被清除

	task, ok := store.Get(taskID)
	if !ok {
		t.Fatal("task not found")
	}
	var found bool
	for i := range task.Events {
		ev := &task.Events[i]
		if ev.Type != model.EventStatus {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(ev.Input, &payload); err != nil {
			continue
		}
		if payload["cleared"] == true {
			found = true
			if ev.Text == "" {
				t.Error("清除事件的 Text 不该为空（时间线要有一句人读说明）")
			}
			break
		}
	}
	if !found {
		t.Fatalf("goal 清除没有产生带 cleared 标记的 status 事件；events=%+v", task.Events)
	}
}

// TestWireGoal_UnsupportedAdapterReturnsNil agent 没有 GoalReporter 时返回 nil（不是故障）。
//
// dsh / qoder 的 goal 走工具调用、print 路径没有 goal —— 这些情况都不该注册回调。
func TestWireGoal_UnsupportedAdapterReturnsNil(t *testing.T) {
	bus := NewEventBus()
	store, err := NewTaskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tt, err := store.Create(&model.Task{ProjectID: "p", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	// fakeDeltaAdapter 只实现 AgentAdapter，不实现 GoalReporter。
	if h := WireGoal(newFakeDeltaAdapter(), bus, store, tt.ID, nil); h != nil {
		t.Fatal("不支持 goal 的 adapter 应返回 nil 句柄")
	}
}
