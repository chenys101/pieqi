package core

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"pieqi/internal/model"

	"github.com/google/uuid"
)

func (s *RequirementStore) GetPlanView(requirementID string) (*model.PlanDraft, *model.ExecutionPin, []model.PlanRevision, []string, error) {
	r, ok := s.Get(requirementID)
	if !ok {
		return nil, nil, nil, nil, fmt.Errorf("requirement not found: %s", requirementID)
	}
	draft := r.PlanDraft
	if draft == nil {
		draft = &model.PlanDraft{RequirementID: requirementID, Stage: model.PlanStageIntent}
	}
	var blockers []string
	if len(draft.Nodes) > 0 || len(draft.Assertions) > 0 {
		blockers = PublishBlockers(draft)
	}
	return draft, r.ExecutionPin, append([]model.PlanRevision(nil), r.PlanRevisions...), blockers, nil
}

func normalizeAssertionBlocks(a *model.PlanAssertion) {
	if a == nil {
		return
	}
	switch strings.TrimSpace(a.Kind) {
	case model.AssertOpenQuestion:
		if !a.AcceptedRisk {
			a.BlocksPublish = true
		}
	case model.AssertAssumption:
		a.BlocksPublish = !a.AcceptedRisk
	}
}

func (s *RequirementStore) SavePlanDraft(requirementID string, draft *model.PlanDraft) (*model.PlanDraft, error) {
	if draft == nil {
		return nil, fmt.Errorf("nil draft")
	}
	now := time.Now()
	draft.RequirementID = requirementID
	draft.UpdatedAt = now
	if strings.TrimSpace(draft.Stage) == "" {
		draft.Stage = model.PlanStageIntent
	}
	for i := range draft.Assertions {
		if strings.TrimSpace(draft.Assertions[i].ID) == "" {
			draft.Assertions[i].ID = uuid.New().String()
		}
		normalizeAssertionBlocks(&draft.Assertions[i])
	}
	updated, err := s.Update(requirementID, func(req *model.Requirement) bool {
		cp := *draft
		req.PlanDraft = &cp
		return true
	})
	if err != nil {
		return nil, err
	}
	if updated.PlanDraft == nil {
		return draft, nil
	}
	out := *updated.PlanDraft
	return &out, nil
}

func (s *RequirementStore) PublishPlan(requirementID, actor string) (*model.PlanRevision, error) {
	r, ok := s.Get(requirementID)
	if !ok {
		return nil, fmt.Errorf("requirement not found: %s", requirementID)
	}
	if r.PlanDraft == nil {
		return nil, fmt.Errorf("no plan draft")
	}
	if err := CanPublish(r.PlanDraft); err != nil {
		return nil, err
	}
	now := time.Now()
	next := 1
	for _, rev := range r.PlanRevisions {
		if rev.Revision >= next {
			next = rev.Revision + 1
		}
	}
	rev := model.PlanRevision{
		ID:            uuid.New().String(),
		RequirementID: requirementID,
		Revision:      next,
		Nodes:         append([]model.NodeRevision(nil), r.PlanDraft.Nodes...),
		Assertions:    append([]model.PlanAssertion(nil), r.PlanDraft.Assertions...),
		PublishedAt:   now,
		PublishedBy:   strings.TrimSpace(actor),
		Immutable:     true,
	}
	snap, _ := json.Marshal(rev)
	updated, err := s.Update(requirementID, func(req *model.Requirement) bool {
		req.PlanRevisions = append(req.PlanRevisions, rev)
		req.ExecutionPin = &model.ExecutionPin{
			RequirementID: requirementID, PlanRevisionID: rev.ID, PinnedAt: now,
		}
		appendReqEvent(req, model.ReqEventPlan, fmt.Sprintf("plan published rev=%d", next), string(snap), now)
		return true
	})
	if err != nil {
		return nil, err
	}
	for i := range updated.PlanRevisions {
		if updated.PlanRevisions[i].ID == rev.ID {
			out := updated.PlanRevisions[i]
			return &out, nil
		}
	}
	return &rev, nil
}

func (s *RequirementStore) PinPlanRevision(requirementID, revisionID string) (*model.ExecutionPin, error) {
	revisionID = strings.TrimSpace(revisionID)
	if revisionID == "" {
		return nil, fmt.Errorf("revision_id required")
	}
	r, ok := s.Get(requirementID)
	if !ok {
		return nil, fmt.Errorf("requirement not found: %s", requirementID)
	}
	found := false
	for _, rev := range r.PlanRevisions {
		if rev.ID == revisionID {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("plan revision not found: %s", revisionID)
	}
	now := time.Now()
	updated, err := s.Update(requirementID, func(req *model.Requirement) bool {
		req.ExecutionPin = &model.ExecutionPin{
			RequirementID: requirementID, PlanRevisionID: revisionID, PinnedAt: now,
		}
		appendReqEvent(req, model.ReqEventPlan, "plan pinned "+revisionID, "", now)
		return true
	})
	if err != nil {
		return nil, err
	}
	pin := *updated.ExecutionPin
	return &pin, nil
}

func (s *RequirementStore) AddPlanChange(requirementID, reason, actor string) (*model.PlanChange, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, fmt.Errorf("reason required")
	}
	now := time.Now()
	ch := model.PlanChange{
		ID: uuid.New().String(), RequirementID: requirementID, Reason: reason,
		CreatedAt: now, CreatedBy: strings.TrimSpace(actor),
	}
	updated, err := s.Update(requirementID, func(req *model.Requirement) bool {
		req.PlanChanges = append(req.PlanChanges, ch)
		appendReqEvent(req, model.ReqEventPlan, "plan change: "+reason, ch.ID, now)
		return true
	})
	if err != nil {
		return nil, err
	}
	for i := range updated.PlanChanges {
		if updated.PlanChanges[i].ID == ch.ID {
			out := updated.PlanChanges[i]
			return &out, nil
		}
	}
	return &ch, nil
}

func (s *RequirementStore) PinnedRevision(requirementID string) (*model.PlanRevision, error) {
	r, ok := s.Get(requirementID)
	if !ok {
		return nil, fmt.Errorf("requirement not found: %s", requirementID)
	}
	if r.ExecutionPin == nil || strings.TrimSpace(r.ExecutionPin.PlanRevisionID) == "" {
		return nil, fmt.Errorf("no execution pin")
	}
	for _, rev := range r.PlanRevisions {
		if rev.ID == r.ExecutionPin.PlanRevisionID {
			out := rev
			return &out, nil
		}
	}
	return nil, fmt.Errorf("pinned revision missing: %s", r.ExecutionPin.PlanRevisionID)
}
