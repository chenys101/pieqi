package core

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"pieqi/internal/model"

	"github.com/google/uuid"
)

// SetTaskStore 注入 Task 创建能力（confirm 落地 planned Task 需要）。
func (s *RequirementStore) SetTaskStore(ts *TaskStore) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks = ts
}

// PutProposal 校验并暂存提案（内存；拒确认则丢弃，ADR-0021）。
func (s *RequirementStore) PutProposal(requirementID string, nodes []model.ProposedTaskNode) (*model.SplitProposal, error) {
	if s == nil || s.backend == nil {
		return nil, fmt.Errorf("requirement store not wired")
	}
	r, ok := s.Get(requirementID)
	if !ok {
		return nil, fmt.Errorf("requirement not found: %s", requirementID)
	}
	p := &model.SplitProposal{
		ID:            uuid.New().String(),
		RequirementID: requirementID,
		Nodes:         nodes,
		CreatedAt:     time.Now(),
	}
	if err := ValidateSplitProposal(r, p); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.proposals == nil {
		s.proposals = make(map[string]*model.SplitProposal)
	}
	s.proposals[requirementID] = p
	s.mu.Unlock()
	cp := *p
	cp.Nodes = append([]model.ProposedTaskNode(nil), p.Nodes...)
	return &cp, nil
}

// GetProposal 返回当前暂存提案。
func (s *RequirementStore) GetProposal(requirementID string) (*model.SplitProposal, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.proposals[requirementID]
	if !ok || p == nil {
		return nil, false
	}
	cp := *p
	cp.Nodes = append([]model.ProposedTaskNode(nil), p.Nodes...)
	return &cp, true
}

// DiscardProposal 丢弃暂存提案。
func (s *RequirementStore) DiscardProposal(requirementID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.proposals, requirementID)
}

// ConfirmResult confirm 产物。
type ConfirmResult struct {
	Requirement *model.Requirement `json:"requirement"`
	Tasks       []*model.Task      `json:"tasks"`
	// Abandoned 覆盖确认时被废弃的旧工作任务数（不含拆分会话本身）。
	Abandoned int `json:"abandoned,omitempty"`
}

// Confirm 接受首次拆分提案：创建 planned Task、改写 depends_on 为真实 id、graph_version++。
//
// nodes 非空则先替换暂存提案再确认；为空则用已 Put 的提案。
func (s *RequirementStore) Confirm(requirementID string, nodes []model.ProposedTaskNode) (*ConfirmResult, error) {
	return s.applyProposal(requirementID, nodes, false)
}

// Expand 渐进追加 planned Task（ADR-0021）：DependsOn 可挂已有 Task；事件类型 expand。
func (s *RequirementStore) Expand(requirementID string, nodes []model.ProposedTaskNode) (*ConfirmResult, error) {
	return s.applyProposal(requirementID, nodes, true)
}

// WorkTaskIDs 同需求下尚未废弃的工作任务 id（不含拆分会话、不含已 cancelled）。
func (s *RequirementStore) WorkTaskIDs(requirementID string) []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	ts := s.tasks
	s.mu.RUnlock()
	if ts == nil || requirementID == "" {
		return nil
	}
	var ids []string
	for _, t := range filterOutSuggest(tasksInRequirement(ts, requirementID)) {
		if t == nil || t.ID == "" || t.Status == model.TaskCancelled {
			continue
		}
		ids = append(ids, t.ID)
	}
	return ids
}

// AbandonWorkTasks 将同需求下非拆分工作任务标为 cancelled（覆盖拆分前调用）。
// 已在跑的任务应先由调用方 Cancel；本方法只做状态落盘，幂等。
func (s *RequirementStore) AbandonWorkTasks(requirementID string) (int, error) {
	if s == nil {
		return 0, fmt.Errorf("requirement store not wired")
	}
	s.mu.RLock()
	ts := s.tasks
	s.mu.RUnlock()
	if ts == nil {
		return 0, fmt.Errorf("task store not wired")
	}
	const abandonReason = "abandoned: requirement re-split"
	n := 0
	now := time.Now()
	for _, t := range filterOutSuggest(tasksInRequirement(ts, requirementID)) {
		if t == nil {
			continue
		}
		// 上次覆盖已废弃的票跳过，避免二次确认把历史 cancelled 再计入 abandoned。
		if t.Status == model.TaskCancelled && strings.Contains(t.Error, abandonReason) {
			continue
		}
		_, err := ts.Update(t.ID, func(x *model.Task) bool {
			changed := false
			if x.Status != model.TaskCancelled {
				x.Status = model.TaskCancelled
				changed = true
			}
			if x.FinishedAt == nil {
				x.FinishedAt = &now
				changed = true
			}
			if !strings.Contains(x.Error, abandonReason) {
				x.Error = abandonReason
				changed = true
			}
			return changed
		})
		if err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// ConfirmReplace 覆盖确认：先废弃旧工作任务，再按首次 Confirm 落地新提案（不走 expand）。
// force=false 时若已有离开 planned 的工作票则拒绝（避免未交付施工被默默废弃）。
func (s *RequirementStore) ConfirmReplace(requirementID string, nodes []model.ProposedTaskNode, force bool) (*ConfirmResult, error) {
	if !force && s.HasProgressedWork(requirementID) {
		return nil, errReplaceNeedsForce
	}
	abandoned, err := s.AbandonWorkTasks(requirementID)
	if err != nil {
		return nil, err
	}
	out, err := s.applyProposal(requirementID, nodes, false)
	if err != nil {
		return nil, err
	}
	out.Abandoned = abandoned
	return out, nil
}

func (s *RequirementStore) applyProposal(requirementID string, nodes []model.ProposedTaskNode, expand bool) (*ConfirmResult, error) {
	if s == nil || s.backend == nil {
		return nil, fmt.Errorf("requirement store not wired")
	}
	s.mu.Lock()
	ts := s.tasks
	s.mu.Unlock()
	if ts == nil {
		return nil, fmt.Errorf("task store not wired for confirm")
	}

	if len(nodes) > 0 {
		if expand {
			if _, err := s.putExpandProposal(requirementID, nodes); err != nil {
				return nil, err
			}
		} else if _, err := s.PutProposal(requirementID, nodes); err != nil {
			return nil, err
		}
	}

	s.mu.RLock()
	p, ok := s.proposals[requirementID]
	s.mu.RUnlock()
	if !ok || p == nil {
		return nil, fmt.Errorf("no proposal to confirm")
	}

	r, ok := s.Get(requirementID)
	if !ok {
		return nil, fmt.Errorf("requirement not found: %s", requirementID)
	}

	existing := tasksInRequirement(ts, requirementID)
	if expand {
		if err := ValidateExpandProposal(r, p, existing); err != nil {
			return nil, err
		}
	} else {
		if err := ValidateSplitProposal(r, p); err != nil {
			return nil, err
		}
	}

	existingIDs := make(map[string]struct{}, len(existing))
	for _, t := range existing {
		existingIDs[t.ID] = struct{}{}
	}

	idMap := make(map[string]string, len(p.Nodes))
	for _, n := range p.Nodes {
		idMap[n.TempID] = uuid.New().String()
	}

	created := make([]*model.Task, 0, len(p.Nodes))
	for _, n := range p.Nodes {
		deps := make([]string, 0, len(n.DependsOn))
		for _, d := range n.DependsOn {
			d = strings.TrimSpace(d)
			if d == "" {
				continue
			}
			if real, ok := idMap[d]; ok {
				deps = append(deps, real)
			} else if _, ok := existingIDs[d]; ok {
				deps = append(deps, d)
			} else {
				return nil, fmt.Errorf("node %q: depends_on unknown id %q", n.TempID, d)
			}
		}
		title := strings.TrimSpace(n.Title)
		if title == "" {
			title = truncateRunes(n.Prompt, 40)
		}
		task := &model.Task{
			ID:            idMap[n.TempID],
			Source:        model.SourceHTTP,
			Status:        model.TaskPlanned,
			RequirementID: requirementID,
			ProjectID:     r.ProjectID,
			ProjectPath:   r.ProjectPath,
			Prompt:        AppendDeliveryDoD(n.Role, n.Prompt),
			Title:         title,
			Role:          n.Role,
			Skills:        append([]string(nil), n.Skills...),
			Agent:         strings.TrimSpace(n.Agent),
			Model:         strings.TrimSpace(n.Model),
			CoversAC:      append([]string(nil), n.CoversAC...),
			Verify:        n.Verify,
			DependsOn:     deps,
		}
		got, err := ts.Create(task)
		if err != nil {
			return nil, fmt.Errorf("create planned task %q: %w", n.TempID, err)
		}
		created = append(created, got)
	}

	snap, _ := json.Marshal(p)
	now := time.Now()
	evType := model.ReqEventConfirm
	evText := fmt.Sprintf("confirmed %d planned tasks", len(created))
	if expand {
		evType = model.ReqEventExpand
		evText = fmt.Sprintf("expanded %d planned tasks", len(created))
	}
	updated, err := s.Update(requirementID, func(req *model.Requirement) bool {
		req.GraphVersion++
		req.Status = model.RequirementActive
		seq := 1
		for _, ev := range req.Events {
			if ev.Seq >= seq {
				seq = ev.Seq + 1
			}
		}
		req.Events = append(req.Events, model.RequirementEvent{
			Seq:     seq,
			Type:    evType,
			Text:    evText,
			Payload: string(snap),
			At:      now,
		})
		return true
	})
	if err != nil {
		return nil, err
	}

	s.DiscardProposal(requirementID)
	return &ConfirmResult{Requirement: updated, Tasks: created}, nil
}

// putExpandProposal 与 PutProposal 相同，但用 Expand 校验（允许依赖已有 Task）。
func (s *RequirementStore) putExpandProposal(requirementID string, nodes []model.ProposedTaskNode) (*model.SplitProposal, error) {
	if s == nil || s.backend == nil {
		return nil, fmt.Errorf("requirement store not wired")
	}
	r, ok := s.Get(requirementID)
	if !ok {
		return nil, fmt.Errorf("requirement not found: %s", requirementID)
	}
	s.mu.Lock()
	ts := s.tasks
	s.mu.Unlock()
	p := &model.SplitProposal{
		ID:            uuid.New().String(),
		RequirementID: requirementID,
		Nodes:         nodes,
		CreatedAt:     time.Now(),
	}
	if err := ValidateExpandProposal(r, p, tasksInRequirement(ts, requirementID)); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.proposals == nil {
		s.proposals = make(map[string]*model.SplitProposal)
	}
	s.proposals[requirementID] = p
	s.mu.Unlock()
	cp := *p
	cp.Nodes = append([]model.ProposedTaskNode(nil), p.Nodes...)
	return &cp, nil
}

func tasksInRequirement(ts *TaskStore, requirementID string) []*model.Task {
	if ts == nil || requirementID == "" {
		return nil
	}
	var out []*model.Task
	for _, t := range ts.List() {
		if t.RequirementID == requirementID {
			out = append(out, t)
		}
	}
	return out
}

func truncateRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
