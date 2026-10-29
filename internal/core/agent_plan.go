// Package core 的 plan 连接器：把 ACP session/update(plan) 接到任务事件流。
//
// 协议（Agent Plan）：每次更新带**完整** entries，客户端整表替换。
// 这里同时：
//  1. 写 Task.Plan（当前快照，前端 sticky / 刷新后仍在）；
//  2. 追加 EventPlan（时间线清单卡；连续更新各自一条，保留进度历史）。
//
// adapter 未实现 PlanReporter（print 路径）→ WirePlan 返回 nil。
package core

import (
	"encoding/json"
	"fmt"
	"time"

	"pieqi/internal/agent"
	"pieqi/internal/model"

	"go.uber.org/zap"
)

// PlanHandle WirePlan 返回的句柄；Unwire 拆卸回调。
type PlanHandle struct {
	// any：只需要 PlanReporter；收 AgentAdapter 会逼测试 fake 实现整表接口。
	adapter any
}

// Unwire 注销 plan 回调。幂等。
func (h *PlanHandle) Unwire() {
	if h == nil || h.adapter == nil {
		return
	}
	if r, ok := h.adapter.(agent.PlanReporter); ok {
		r.OnPlanUpdate(nil)
	}
	h.adapter = nil
}

// WirePlan 把 adapter 的 plan 推送接到任务事件流。不支持时返回 nil。
// adapter 参数为 any：运行时传 AgentAdapter，测试可只实现 PlanReporter。
func WirePlan(adapter any, bus *EventBus, store *TaskStore, taskID string, logger *zap.Logger) *PlanHandle {
	reporter, ok := adapter.(agent.PlanReporter)
	if !ok {
		return nil
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	w := &planWire{bus: bus, store: store, taskID: taskID, logger: logger}
	reporter.OnPlanUpdate(w.onPlan)
	return &PlanHandle{adapter: adapter}
}

type planWire struct {
	bus    *EventBus
	store  *TaskStore
	taskID string
	logger *zap.Logger
}

func (w *planWire) onPlan(u agent.PlanUpdate) {
	entries := make([]model.PlanEntry, 0, len(u.Entries))
	for _, e := range u.Entries {
		entries = append(entries, model.PlanEntry{
			Content:  e.Content,
			Priority: model.PlanEntryPriority(e.Priority),
			Status:   model.PlanEntryStatus(e.Status),
		})
	}
	payload := model.PlanPayload{Entries: entries}
	raw, err := json.Marshal(payload)
	if err != nil {
		w.logger.Warn("plan marshal failed", zap.String("task", w.taskID), zap.Error(err))
		return
	}
	ev := model.TaskEvent{
		Type:  model.EventPlan,
		Text:  planSummary(entries),
		Input: raw,
		At:    time.Now(),
	}
	updated, err := w.store.Update(w.taskID, func(t *model.Task) bool {
		cp := payload
		t.Plan = &cp
		w.store.AppendEvent(t, ev)
		return true
	})
	if err != nil || updated == nil {
		w.logger.Warn("plan event persist failed", zap.String("task", w.taskID), zap.Error(err))
		return
	}
	w.bus.Publish(Event{Type: "task_updated", TaskID: w.taskID, Task: updated})
}

func planSummary(entries []model.PlanEntry) string {
	n := len(entries)
	if n == 0 {
		return "计划已清空"
	}
	done := 0
	for _, e := range entries {
		if e.Status == model.PlanCompleted {
			done++
		}
	}
	return fmt.Sprintf("计划 %d 项 · %d 完成", n, done)
}
