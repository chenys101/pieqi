package core

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"pieqi/internal/model"
	"pieqi/internal/workflow"
)

// SetWorkflowRegistry 注入流程包注册表（拆分 prompt / DoD / 闸门）。
func (s *RequirementStore) SetWorkflowRegistry(r *workflow.Registry) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workflows = r
}

// WorkflowRegistry 返回已接线的 Registry（可 nil）。
func (s *RequirementStore) WorkflowRegistry() *workflow.Registry {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.workflows
}

// PackFor 解析需求钉死的流程包；历史空 workflow_id → s2-dag。
func (s *RequirementStore) PackFor(r *model.Requirement) (*workflow.Pack, error) {
	reg := s.WorkflowRegistry()
	if reg == nil {
		return nil, fmt.Errorf("workflow registry not wired")
	}
	if r == nil {
		return reg.Get("")
	}
	return reg.ResolveForRequirement(r.WorkflowID)
}

// ContentFingerprint 同 project + title/body 去重用。
func ContentFingerprint(projectPath, title, body string) string {
	h := sha256.New()
	_, _ = h.Write([]byte(strings.TrimSpace(projectPath)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(strings.TrimSpace(title)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(strings.TrimSpace(body)))
	return hex.EncodeToString(h.Sum(nil))
}

// FindDuplicateActive 同 project+文案 hash 的 active/draft 需求（排除 excludeID）。
func (s *RequirementStore) FindDuplicateActive(projectPath, title, body, excludeID string) *model.Requirement {
	fp := ContentFingerprint(projectPath, title, body)
	for _, r := range s.List() {
		if r == nil || r.ID == excludeID {
			continue
		}
		if r.Status != model.RequirementActive && r.Status != model.RequirementDraft {
			continue
		}
		if ContentFingerprint(r.ProjectPath, r.Title, r.Body) == fp {
			cp := *r
			return &cp
		}
	}
	return nil
}

// CanChangeWorkflow 换包规则（§11.4）。
// 返回 allow, needReplace, reason。
func (s *RequirementStore) CanChangeWorkflow(requirementID string) (allow bool, needReplace bool, reason string) {
	r, ok := s.Get(requirementID)
	if !ok || r == nil {
		return false, false, "requirement not found"
	}
	if r.GraphVersion == 0 && !s.hasWorkTasks(requirementID) {
		return true, false, ""
	}
	if s.HasProgressedWork(requirementID) {
		return false, false, "has running/completed work; create a new requirement to compare packs"
	}
	// confirm 过但票全 planned
	if r.GraphVersion > 0 {
		return true, true, "confirmed with planned tickets only; replace + re-split required"
	}
	return true, false, ""
}

func (s *RequirementStore) hasWorkTasks(requirementID string) bool {
	return len(s.WorkTaskIDs(requirementID)) > 0
}

// CountActiveImplement 同需求下已激活（非 planned/cancelled）的实现类票数。
func (s *RequirementStore) CountActiveImplement(requirementID string) int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	ts := s.tasks
	s.mu.RUnlock()
	if ts == nil {
		return 0
	}
	n := 0
	for _, id := range s.WorkTaskIDs(requirementID) {
		t, ok := ts.Get(id)
		if !ok || t == nil {
			continue
		}
		if !workflow.IsImplementRole(t.Role) {
			continue
		}
		switch t.Status {
		case model.TaskPlanned, model.TaskCancelled, model.TaskCompleted, model.TaskFailed:
			continue
		default:
			n++
		}
	}
	return n
}

// CountHighStakesApprovals 全局 waiting_input(approval) 且风险 ≥ L2 的数量。
func CountHighStakesApprovals(ts *TaskStore) int {
	if ts == nil {
		return 0
	}
	n := 0
	for _, t := range ts.List() {
		if t == nil || t.Status != model.TaskWaitingInput {
			continue
		}
		d := t.CurrentDecision
		if d == nil || d.Kind != model.DecisionKindApproval {
			continue
		}
		if d.Risk == model.RiskL2 || d.Risk == model.RiskL3 {
			n++
		}
	}
	return n
}

// ErrCapacityGate 审不过来。
var ErrCapacityGate = fmt.Errorf("review capacity: high-stakes pending approvals >= 2; finish them before starting new implement tickets")

// ErrImplementParallel 同需求实现并行超限。
func ErrImplementParallel(lim int) error {
	return fmt.Errorf("max_parallel_implement=%d reached for this requirement", lim)
}
