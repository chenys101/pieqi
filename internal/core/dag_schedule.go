package core

import (
	"context"
	"fmt"
	"strings"

	"pieqi/internal/model"

	"go.uber.org/zap"
)

// DepsSatisfied 报告同需求内 DependsOn 是否均已 completed。
//
// 短路径（无 DependsOn）恒为 true。
// 前置为 failed/cancelled/planned/pending/running → 未满足（不得开跑）。
func DepsSatisfied(store *TaskStore, t *model.Task) bool {
	if t == nil || len(t.DependsOn) == 0 {
		return true
	}
	for _, depID := range t.DependsOn {
		if depID == "" {
			continue
		}
		dep, ok := store.Get(depID)
		if !ok || dep.Status != model.TaskCompleted {
			return false
		}
	}
	return true
}

// ReadyPlannedTasks 列出某需求下依赖已满足、仍为 planned 的任务。
func ReadyPlannedTasks(store *TaskStore, requirementID string) []*model.Task {
	if store == nil || requirementID == "" {
		return nil
	}
	var out []*model.Task
	for _, t := range store.List() {
		if t.RequirementID != requirementID || t.Status != model.TaskPlanned {
			continue
		}
		if DepsSatisfied(store, t) {
			out = append(out, t)
		}
	}
	return out
}

// ActivatePlanned 将 planned 标为 pending（可被 Runner 启动）。依赖未满足则报错。
func (s *TaskStore) ActivatePlanned(id string) (*model.Task, error) {
	t, ok := s.Get(id)
	if !ok {
		return nil, fmt.Errorf("task not found: %s", id)
	}
	if t.Status != model.TaskPlanned {
		return nil, fmt.Errorf("task %s is %s, want planned", id, t.Status)
	}
	if !DepsSatisfied(s, t) {
		return nil, fmt.Errorf("dependencies not satisfied")
	}
	return s.Update(id, func(x *model.Task) bool {
		x.Status = model.TaskPending
		return true
	})
}

// StartReadyInRequirement 激活并启动某需求下所有依赖已满足的 planned（confirm/expand 后调用）。
//
// 缺 Agent 的 planned 跳过：planned 允许空 agent，启动前必须指定（见 startPlannedTask）。
func (tr *TaskRunner) StartReadyInRequirement(requirementID string) {
	if tr == nil || requirementID == "" {
		return
	}
	for _, ready := range ReadyPlannedTasks(tr.store, requirementID) {
		if strings.TrimSpace(ready.Agent) == "" {
			tr.logger.Info("start ready planned skipped: agent unset",
				zap.String("task", ready.ID),
				zap.String("requirement", requirementID))
			continue
		}
		act, err := tr.store.ActivatePlanned(ready.ID)
		if err != nil {
			tr.logger.Warn("start ready planned", zap.String("task", ready.ID), zap.Error(err))
			continue
		}
		tr.bus.Publish(Event{Type: "task_updated", TaskID: act.ID, Task: act})
		tr.Start(context.Background(), act)
	}
}
