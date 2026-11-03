package core

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"pieqi/internal/model"

	"github.com/google/uuid"
)

// OpenIssueResult 开立 Issue 的结果（blocker 可能已带 Fix Task）。
type OpenIssueResult struct {
	Requirement *model.Requirement `json:"requirement"`
	Issue       *model.Issue       `json:"issue"`
	FixTask     *model.Task        `json:"fix_task,omitempty"`
}

// OpenIssue 登记缺陷（验证失败 → 再开发只经此入口）。
// blocker：自动建 Fix Task（planned）；major/minor：保持 open，等人 ConfirmFix。
func (s *RequirementStore) OpenIssue(requirementID string, draft model.Issue) (*OpenIssueResult, error) {
	if s == nil || s.backend == nil {
		return nil, fmt.Errorf("requirement store not wired")
	}
	s.mu.Lock()
	ts := s.tasks
	s.mu.Unlock()
	if ts == nil {
		return nil, fmt.Errorf("task store not wired")
	}
	r, ok := s.Get(requirementID)
	if !ok {
		return nil, fmt.Errorf("requirement not found: %s", requirementID)
	}
	if r.Status == model.RequirementCancelled || r.Status == model.RequirementDone {
		return nil, fmt.Errorf("requirement is %s", r.Status)
	}

	now := time.Now()
	iss := draft
	if strings.TrimSpace(iss.ID) == "" {
		iss.ID = uuid.New().String()
	}
	if strings.TrimSpace(iss.Title) == "" {
		return nil, fmt.Errorf("issue title required")
	}
	switch iss.Severity {
	case model.SeverityBlocker, model.SeverityMajor, model.SeverityMinor:
	default:
		iss.Severity = model.SeverityMajor
	}
	iss.Status = model.IssueOpen
	iss.CreatedAt = now
	iss.UpdatedAt = now
	iss.FixTaskID = ""
	iss.ReverifyTaskID = ""

	var fix *model.Task
	if iss.Severity == model.SeverityBlocker {
		ft, err := s.buildFixTask(r, &iss)
		if err != nil {
			return nil, err
		}
		got, err := ts.Create(ft)
		if err != nil {
			return nil, fmt.Errorf("create fix task: %w", err)
		}
		fix = got
		iss.Status = model.IssueFixing
		iss.FixTaskID = got.ID
	}

	snap, _ := json.Marshal(iss)
	updated, err := s.Update(requirementID, func(req *model.Requirement) bool {
		req.Issues = append(req.Issues, iss)
		appendReqEvent(req, model.ReqEventIssue, "opened issue "+iss.ID, string(snap), now)
		if req.Status == model.RequirementDraft {
			req.Status = model.RequirementActive
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	// 返回带最新 Issue 的指针
	var saved *model.Issue
	for i := range updated.Issues {
		if updated.Issues[i].ID == iss.ID {
			saved = &updated.Issues[i]
			break
		}
	}
	return &OpenIssueResult{Requirement: updated, Issue: saved, FixTask: fix}, nil
}

// ConfirmFix 人对 major/minor Issue 确认派修 → 建 Fix Task，状态 fixing。
func (s *RequirementStore) ConfirmFix(requirementID, issueID string) (*OpenIssueResult, error) {
	if s == nil || s.backend == nil {
		return nil, fmt.Errorf("requirement store not wired")
	}
	s.mu.Lock()
	ts := s.tasks
	s.mu.Unlock()
	if ts == nil {
		return nil, fmt.Errorf("task store not wired")
	}
	r, ok := s.Get(requirementID)
	if !ok {
		return nil, fmt.Errorf("requirement not found: %s", requirementID)
	}
	var target *model.Issue
	for i := range r.Issues {
		if r.Issues[i].ID == issueID {
			target = &r.Issues[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("issue not found: %s", issueID)
	}
	if target.Status != model.IssueOpen {
		return nil, fmt.Errorf("issue %s is %s, want open", issueID, target.Status)
	}
	if target.FixTaskID != "" {
		return nil, fmt.Errorf("issue already has fix task")
	}

	ft, err := s.buildFixTask(r, target)
	if err != nil {
		return nil, err
	}
	got, err := ts.Create(ft)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	updated, err := s.Update(requirementID, func(req *model.Requirement) bool {
		for i := range req.Issues {
			if req.Issues[i].ID != issueID {
				continue
			}
			req.Issues[i].Status = model.IssueFixing
			req.Issues[i].FixTaskID = got.ID
			req.Issues[i].UpdatedAt = now
			appendReqEvent(req, model.ReqEventIssue, "confirm fix "+issueID+" → "+got.ID, "", now)
			return true
		}
		return false
	})
	if err != nil {
		return nil, err
	}
	var saved *model.Issue
	for i := range updated.Issues {
		if updated.Issues[i].ID == issueID {
			saved = &updated.Issues[i]
			break
		}
	}
	return &OpenIssueResult{Requirement: updated, Issue: saved, FixTask: got}, nil
}

// ResolveIssue 将 Issue 标为 resolved（重验通过或人确认）。
func (s *RequirementStore) ResolveIssue(requirementID, issueID string) (*model.Requirement, error) {
	now := time.Now()
	return s.Update(requirementID, func(req *model.Requirement) bool {
		for i := range req.Issues {
			if req.Issues[i].ID != issueID {
				continue
			}
			if req.Issues[i].Status == model.IssueResolved {
				return false
			}
			req.Issues[i].Status = model.IssueResolved
			req.Issues[i].UpdatedAt = now
			// 对应 AC 人验 fail 清掉，允许转绿
			for j := range req.Acceptance {
				for _, ac := range req.Issues[i].CoversAC {
					if req.Acceptance[j].ID == ac && req.Acceptance[j].HumanResult == "fail" {
						req.Acceptance[j].HumanResult = "pass"
					}
				}
			}
			appendReqEvent(req, model.ReqEventIssue, "resolved "+issueID, "", now)
			return true
		}
		return false
	})
}

// HumanVerify 写入人验结论；fail 时自动 OpenIssue（major）。
func (s *RequirementStore) HumanVerify(requirementID, acID string, pass bool, detail string) (*OpenIssueResult, error) {
	acID = strings.TrimSpace(acID)
	if acID == "" {
		return nil, fmt.Errorf("ac_id required")
	}
	r0, ok := s.Get(requirementID)
	if !ok {
		return nil, fmt.Errorf("requirement not found: %s", requirementID)
	}
	found := false
	for _, ac := range r0.Acceptance {
		if ac.ID == acID {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("acceptance not found: %s", acID)
	}

	now := time.Now()
	result := "fail"
	if pass {
		result = "pass"
	}
	_, err := s.Update(requirementID, func(req *model.Requirement) bool {
		for i := range req.Acceptance {
			if req.Acceptance[i].ID != acID {
				continue
			}
			req.Acceptance[i].HumanResult = result
			appendReqEvent(req, model.ReqEventStatus, "human verify "+acID+"="+result, detail, now)
			return true
		}
		return false
	})
	if err != nil {
		return nil, err
	}
	if pass {
		r, _ := s.Get(requirementID)
		return &OpenIssueResult{Requirement: r}, nil
	}
	title := "AC " + acID + " human verify failed"
	if detail != "" {
		title = truncateRunes(detail, 60)
	}
	return s.OpenIssue(requirementID, model.Issue{
		Title:    title,
		Detail:   detail,
		Severity: model.SeverityMajor,
		CoversAC: []string{acID},
	})
}

// MarkDone 全部 AC 绿且无开放 Issue 时收口需求。
func (s *RequirementStore) MarkDone(requirementID string) (*model.Requirement, error) {
	s.mu.Lock()
	ts := s.tasks
	s.mu.Unlock()
	r, ok := s.Get(requirementID)
	if !ok {
		return nil, fmt.Errorf("requirement not found: %s", requirementID)
	}
	outcome := DeriveRequirementOutcome(r, tasksInRequirement(ts, requirementID))
	if outcome == nil || !outcome.CanMarkDone {
		return nil, fmt.Errorf("requirement not ready to mark done")
	}
	now := time.Now()
	return s.Update(requirementID, func(req *model.Requirement) bool {
		req.Status = model.RequirementDone
		appendReqEvent(req, model.ReqEventStatus, "marked done", "", now)
		return true
	})
}

// OnTaskCompleted 任务完成时的回流钩子：Fix → 建重验；重验完成 → Resolve。
func (s *RequirementStore) OnTaskCompleted(t *model.Task) {
	if s == nil || t == nil || t.RequirementID == "" {
		return
	}
	s.mu.Lock()
	ts := s.tasks
	s.mu.Unlock()
	if ts == nil {
		return
	}
	r, ok := s.Get(t.RequirementID)
	if !ok {
		return
	}

	// Fix Task 完成 → 建 reverify planned
	for _, iss := range r.Issues {
		if iss.FixTaskID == t.ID && iss.Status == model.IssueFixing && iss.ReverifyTaskID == "" {
			s.spawnReverify(r, &iss, t)
			return
		}
	}
	// Reverify 完成 → resolve
	for _, iss := range r.Issues {
		if iss.ReverifyTaskID == t.ID && iss.Status == model.IssueFixing {
			_, _ = s.ResolveIssue(t.RequirementID, iss.ID)
			return
		}
	}
}

func (s *RequirementStore) spawnReverify(r *model.Requirement, iss *model.Issue, fix *model.Task) {
	s.mu.Lock()
	ts := s.tasks
	s.mu.Unlock()
	if ts == nil {
		return
	}
	prompt := fmt.Sprintf("Re-verify after fix for issue %q (%s).\nAC: %s\n\nConfirm the acceptance criteria still hold.",
		iss.Title, iss.ID, strings.Join(iss.CoversAC, ", "))
	task := &model.Task{
		ID:            uuid.New().String(),
		Source:        model.SourceHTTP,
		Status:        model.TaskPlanned,
		RequirementID: r.ID,
		ProjectID:     r.ProjectID,
		ProjectPath:   r.ProjectPath,
		Prompt:        prompt,
		Title:         "reverify:" + iss.ID,
		Role:          model.RoleTest,
		CoversAC:      append([]string(nil), iss.CoversAC...),
		DependsOn:     []string{fix.ID},
		Verify:        model.VerifyAuto,
	}
	got, err := ts.Create(task)
	if err != nil {
		return
	}
	now := time.Now()
	_, _ = s.Update(r.ID, func(req *model.Requirement) bool {
		for i := range req.Issues {
			if req.Issues[i].ID != iss.ID {
				continue
			}
			req.Issues[i].ReverifyTaskID = got.ID
			req.Issues[i].UpdatedAt = now
			appendReqEvent(req, model.ReqEventIssue, "reverify "+iss.ID+" → "+got.ID, "", now)
			return true
		}
		return false
	})
}

func (s *RequirementStore) buildFixTask(r *model.Requirement, iss *model.Issue) (*model.Task, error) {
	if r == nil || iss == nil {
		return nil, fmt.Errorf("nil requirement/issue")
	}
	prompt := fmt.Sprintf("Fix issue: %s\n\n%s\n\nCovered AC: %s",
		iss.Title, iss.Detail, strings.Join(iss.CoversAC, ", "))
	return &model.Task{
		ID:            uuid.New().String(),
		Source:        model.SourceHTTP,
		Status:        model.TaskPlanned,
		RequirementID: r.ID,
		ProjectID:     r.ProjectID,
		ProjectPath:   r.ProjectPath,
		Prompt:        prompt,
		Title:         "fix:" + truncateRunes(iss.Title, 40),
		Role:          model.RoleFix,
		CoversAC:      append([]string(nil), iss.CoversAC...),
		Verify:        model.VerifyAuto,
	}, nil
}

func appendReqEvent(req *model.Requirement, typ model.RequirementEventType, text, payload string, at time.Time) {
	seq := 1
	for _, ev := range req.Events {
		if ev.Seq >= seq {
			seq = ev.Seq + 1
		}
	}
	req.Events = append(req.Events, model.RequirementEvent{
		Seq: seq, Type: typ, Text: text, Payload: payload, At: at,
	})
}

// FindRequirementIDForIssue 按 issue id 反查需求（API 用）。
func (s *RequirementStore) FindRequirementIDForIssue(issueID string) (string, bool) {
	if s == nil || issueID == "" {
		return "", false
	}
	for _, r := range s.List() {
		for _, iss := range r.Issues {
			if iss.ID == issueID {
				return r.ID, true
			}
		}
	}
	return "", false
}
