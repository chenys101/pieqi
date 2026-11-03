package core

import (
	"fmt"
	"strings"
	"time"

	"pieqi/internal/model"
)

// Promote 把孤儿 Task 挂到需求下（ADR-0021 §2.3）。
//
// 不改写 Timeline；禁止自动 Promote。若任务已属其它需求则拒绝。
func (s *RequirementStore) Promote(requirementID, taskID string) (*model.Task, error) {
	if s == nil || s.backend == nil {
		return nil, fmt.Errorf("requirement store not wired")
	}
	s.mu.Lock()
	ts := s.tasks
	s.mu.Unlock()
	if ts == nil {
		return nil, fmt.Errorf("task store not wired for promote")
	}
	if _, ok := s.Get(requirementID); !ok {
		return nil, fmt.Errorf("requirement not found: %s", requirementID)
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, fmt.Errorf("task_id required")
	}
	t, ok := ts.Get(taskID)
	if !ok {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}
	if t.RequirementID != "" && t.RequirementID != requirementID {
		return nil, fmt.Errorf("task already belongs to requirement %s", t.RequirementID)
	}
	if t.RequirementID == requirementID {
		return t, nil // 幂等
	}

	updated, err := ts.Update(taskID, func(x *model.Task) bool {
		x.RequirementID = requirementID
		return true
	})
	if err != nil {
		return nil, err
	}

	now := time.Now()
	_, _ = s.Update(requirementID, func(req *model.Requirement) bool {
		if req.Status == model.RequirementDraft {
			req.Status = model.RequirementActive
		}
		seq := 1
		for _, ev := range req.Events {
			if ev.Seq >= seq {
				seq = ev.Seq + 1
			}
		}
		req.Events = append(req.Events, model.RequirementEvent{
			Seq:  seq,
			Type: model.ReqEventPromote,
			Text: "promoted task " + taskID,
			At:   now,
		})
		return true
	})
	return updated, nil
}
