package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"pieqi/internal/model"

	"github.com/google/uuid"
)

func (s *RequirementStore) MaterializePinnedPlan(requirementID string, force bool) (*ConfirmResult, error) {
	if s == nil || s.backend == nil {
		return nil, fmt.Errorf("requirement store not wired")
	}
	s.mu.Lock()
	ts := s.tasks
	s.mu.Unlock()
	if ts == nil {
		return nil, fmt.Errorf("task store not wired")
	}
	rev, err := s.PinnedRevision(requirementID)
	if err != nil {
		return nil, err
	}
	if !rev.Immutable {
		return nil, fmt.Errorf("pinned revision is not immutable")
	}
	if len(rev.Nodes) == 0 {
		return nil, fmt.Errorf("pinned revision has no nodes")
	}
	r, ok := s.Get(requirementID)
	if !ok {
		return nil, fmt.Errorf("requirement not found: %s", requirementID)
	}
	if !force && s.HasProgressedWork(requirementID) {
		return nil, errReplaceNeedsForce
	}
	abandoned, err := s.AbandonWorkTasks(requirementID)
	if err != nil {
		return nil, err
	}
	ids := make(map[string]struct{}, len(rev.Nodes))
	for _, n := range rev.Nodes {
		id := strings.TrimSpace(n.NodeID)
		if id == "" {
			return nil, fmt.Errorf("node missing node_id")
		}
		ids[id] = struct{}{}
	}
	for _, n := range rev.Nodes {
		for _, d := range n.DependsOnNodes {
			d = strings.TrimSpace(d)
			if d == "" {
				continue
			}
			if _, ok := ids[d]; !ok {
				return nil, fmt.Errorf("node %q: depends_on unknown node %q", n.NodeID, d)
			}
		}
	}
	idMap := make(map[string]string, len(rev.Nodes))
	for _, n := range rev.Nodes {
		idMap[strings.TrimSpace(n.NodeID)] = uuid.New().String()
	}
	created := make([]*model.Task, 0, len(rev.Nodes))
	for _, n := range rev.Nodes {
		nodeID := strings.TrimSpace(n.NodeID)
		deps := make([]string, 0, len(n.DependsOnNodes))
		for _, d := range n.DependsOnNodes {
			d = strings.TrimSpace(d)
			if d == "" {
				continue
			}
			deps = append(deps, idMap[d])
		}
		title := strings.TrimSpace(n.Title)
		prompt := strings.TrimSpace(n.Prompt)
		if prompt == "" {
			prompt = title
		}
		if title == "" {
			title = truncateRunes(prompt, 40)
		}
		dodImpl, dodTest := "", ""
		if pack, err := s.PackFor(r); err == nil && pack != nil {
			dodImpl, dodTest = pack.DoDImplement, pack.DoDTest
		}
		task := &model.Task{
			ID: idMap[nodeID], Source: model.SourceHTTP, Status: model.TaskPlanned,
			RequirementID: requirementID, ProjectID: r.ProjectID, ProjectPath: r.ProjectPath,
			Prompt: AppendDeliveryDoDFrom(n.Role, prompt, dodImpl, dodTest),
			Title: title, Role: n.Role, Agent: strings.TrimSpace(n.Agent), Model: strings.TrimSpace(n.Model),
			CoversAC: append([]string(nil), n.CoversAC...), Verify: n.Verify, DependsOn: deps,
			NodeID: nodeID, Attempt: 1,
		}
		got, err := ts.Create(task)
		if err != nil {
			return nil, fmt.Errorf("create planned task %q: %w", nodeID, err)
		}
		created = append(created, got)
	}
	integBranch := strings.TrimSpace(r.IntegrationBranch)
	if integBranch == "" {
		integBranch = IntegrationBranchName(requirementID)
		if err := EnsureIntegrationBranch(context.Background(), r.ProjectPath, integBranch); err != nil {
			return nil, fmt.Errorf("ensure integration branch %q: %w", integBranch, err)
		}
	}
	snap, _ := json.Marshal(rev)
	now := time.Now()
	updated, err := s.Update(requirementID, func(req *model.Requirement) bool {
		req.GraphVersion++
		req.Status = model.RequirementActive
		if strings.TrimSpace(req.IntegrationBranch) == "" && integBranch != "" {
			req.IntegrationBranch = integBranch
		}
		appendReqEvent(req, model.ReqEventConfirm,
			fmt.Sprintf("materialized plan rev=%d → %d planned tasks", rev.Revision, len(created)),
			string(snap), now)
		return true
	})
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	delete(s.proposals, requirementID)
	delete(s.blocks, requirementID)
	s.mu.Unlock()
	return &ConfirmResult{Requirement: updated, Tasks: created, Abandoned: abandoned}, nil
}

func (s *RequirementStore) PublishAndMaterialize(requirementID, actor string, force bool) (*model.PlanRevision, *ConfirmResult, error) {
	rev, err := s.PublishPlan(requirementID, actor)
	if err != nil {
		return nil, nil, err
	}
	out, err := s.MaterializePinnedPlan(requirementID, force)
	if err != nil {
		return rev, nil, err
	}
	return rev, out, nil
}
