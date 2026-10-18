// Package core 的 goal 连接器：把 agent 上报的 goal 状态接到任务事件流。
//
// 形状与 agent_usage.go / agent_toolcall.go 对齐（Wire* + Handle + Unwire）。
//
// 为什么落成 EventStatus 而不是新的事件类型：
//   - `status` 的既有语义就是"状态变更 + Text 放人读摘要"，前端已能渲染；
//   - 新增一种 TaskEventType 要同时改 model、前端渲染与 e2e 视觉基线，
//     而 goal 的信息量只有"一句话 + 几个数字"，不值得新开一条渲染管线。
//   - 结构化数据仍进 Input，将来要做专门的 goal 卡片可以直接读它，不用回改存储。
//
// 依赖方向：core → agent（单向）。
package core

import (
	"encoding/json"
	"fmt"
	"time"

	"pieqi/internal/agent"
	"pieqi/internal/model"
	"go.uber.org/zap"
)

// GoalHandle WireGoal 返回的句柄；Unwire 拆卸回调。
type GoalHandle struct {
	adapter agent.AgentAdapter
}

// Unwire 注销 goal 回调。幂等（对 nil 接收者安全）。
func (h *GoalHandle) Unwire() {
	if h == nil || h.adapter == nil {
		return
	}
	if r, ok := h.adapter.(agent.GoalReporter); ok {
		r.OnGoalUpdate(nil)
	}
	h.adapter = nil
}

// WireGoal 把一个 AgentAdapter 的 goal 上报接到任务事件流。
//
// adapter 不支持 goal 上报（GoalReporter 未实现 —— dsh/qoder 走工具调用、print 路径没有）
// 时返回 nil，调用方需容忍：语义是"这个 agent 不走这条通道"，不是故障。
//
// 回调内：
//  1. 把 goal 快照格式化成一句人读摘要；
//  2. store.Update 追加一条 EventStatus（结构化载荷进 Input）；
//  3. Publish task_updated（goal 变化是低频离散事件，全量重绘合适）。
func WireGoal(adapter agent.AgentAdapter, bus *EventBus, store *TaskStore, taskID string, logger *zap.Logger) *GoalHandle {
	reporter, ok := adapter.(agent.GoalReporter)
	if !ok {
		return nil
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	w := &goalWire{bus: bus, store: store, taskID: taskID, logger: logger}
	reporter.OnGoalUpdate(w.onGoal)
	return &GoalHandle{adapter: adapter}
}

// goalWire 持有 goal 回调所需的依赖。taskID 固定（一个 wire 对应一个 task）。
type goalWire struct {
	bus    *EventBus
	store  *TaskStore
	taskID string
	logger *zap.Logger
}

// onGoal OnGoalUpdate 回调实现：追加 status 事件 + 推送。
func (w *goalWire) onGoal(g *agent.GoalSnapshot) {
	ev := model.TaskEvent{
		Type: model.EventStatus,
		Text: goalSummary(g),
		At:   time.Now(),
	}
	// 结构化载荷：nil 也写（`{"cleared":true}`），让"goal 结束"在事件流里可判定。
	// 只写 Text 的话，"goal 被清除"与"goal 摘要恰好为空"无法区分。
	if payload, err := json.Marshal(goalPayload(g)); err == nil {
		ev.Input = payload
	}

	updated, err := w.store.Update(w.taskID, func(t *model.Task) bool {
		w.store.AppendEvent(t, ev)
		return true
	})
	if err != nil || updated == nil {
		// 记录但不中断：goal 是展示性信息，写失败不该让任务失败。
		w.logger.Warn("goal event persist failed",
			zap.String("task", w.taskID), zap.Error(err))
		return
	}
	w.bus.Publish(Event{Type: "task_updated", TaskID: w.taskID, Task: updated})
}

// goalPayload 事件的结构化载荷。nil（goal 已清除）时给出明确的 cleared 标记。
func goalPayload(g *agent.GoalSnapshot) map[string]any {
	if g == nil {
		return map[string]any{"cleared": true}
	}
	p := map[string]any{
		"condition":       g.Condition,
		"iterations":      g.Iterations,
		"set_at":          g.SetAt,
		"tokens_at_start": g.TokensAtStart,
	}
	if g.LastReason != "" {
		p["last_reason"] = g.LastReason
	}
	return p
}

// goalSummary 生成一句人读摘要（进 EventStatus.Text）。
//
// 措辞刻意点明"完成条件"：Claude Code 的 goal 是 `condition`（Stop hook 判定什么算完成），
// 与 dsh/qoder 的「目标 + 轮次上限」不是一回事（见 docs/agents-goal-and-capabilities.md）。
// 写成"目标"会让用户在别的 agent 上产生错误的预期。
func goalSummary(g *agent.GoalSnapshot) string {
	if g == nil {
		return "goal 已结束"
	}
	s := fmt.Sprintf("goal：%s", g.Condition)
	if g.Iterations > 0 {
		s += fmt.Sprintf("（已迭代 %d 次仍未满足）", g.Iterations)
	}
	if g.LastReason != "" {
		s += fmt.Sprintf("——上次未满足：%s", g.LastReason)
	}
	return s
}
