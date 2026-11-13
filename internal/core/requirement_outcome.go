package core

import (
	"strings"

	"pieqi/internal/model"
)

// DeriveRequirementOutcome 按 covers_ac / Issue / 人验 / 证据汇总 AC 矩阵（ADR-0021 §3.5）。
//
// 证据闸（workflow-execution-efficiency §9）：有非 deferred AC 时，票绿 alone 不够；
// 须 human_result=pass 或非空 evidence_ref。零 AC 容器：无开放 Issue 即可 done。
func DeriveRequirementOutcome(req *model.Requirement, tasks []*model.Task) *model.RequirementOutcome {
	if req == nil {
		return nil
	}
	byAC := map[string][]*model.Task{}
	for _, t := range tasks {
		if t == nil || t.RequirementID != req.ID {
			continue
		}
		for _, ac := range t.CoversAC {
			if ac == "" {
				continue
			}
			byAC[ac] = append(byAC[ac], t)
		}
	}

	openByAC := map[string][]string{}
	var openIssues []model.Issue
	for _, iss := range req.Issues {
		if iss.Status != model.IssueOpen && iss.Status != model.IssueFixing {
			continue
		}
		openIssues = append(openIssues, iss)
		for _, ac := range iss.CoversAC {
			openByAC[ac] = append(openByAC[ac], iss.ID)
		}
	}

	out := &model.RequirementOutcome{
		RequirementID: req.ID,
		Status:        string(req.Status),
		OpenIssues:    openIssues,
	}
	allGreen := true
	var missingEv []string
	for _, ac := range req.Acceptance {
		row := model.ACOutcome{
			ID:            ac.ID,
			Text:          ac.Text,
			CoveringTasks: taskIDs(byAC[ac.ID]),
			OpenIssueIDs:  openByAC[ac.ID],
			HasEvidence:   acHasEvidence(ac),
		}
		switch {
		case ac.Deferred:
			row.State = model.ACDeferred
		case len(openByAC[ac.ID]) > 0:
			row.State = model.ACRed
			allGreen = false
		case ac.HumanResult == "fail":
			row.State = model.ACRed
			allGreen = false
		case acHasEvidence(ac):
			row.State = model.ACGreen
		case hasCompletedCover(byAC[ac.ID]):
			// 票绿是主张不是证据：展示 pending，计入证据缺口。
			row.State = model.ACPending
			allGreen = false
			missingEv = append(missingEv, ac.ID)
		default:
			row.State = model.ACPending
			allGreen = false
			missingEv = append(missingEv, ac.ID)
		}
		out.Acceptance = append(out.Acceptance, row)
	}
	// 零 AC：容器需求，无开放 Issue 即可收口（不得宣称 testing，但可 done）。
	if len(req.Acceptance) == 0 {
		allGreen = len(openIssues) == 0
	}
	out.MissingEvidence = missingEv
	out.CanMarkDone = allGreen && len(openIssues) == 0 && req.Status != model.RequirementCancelled
	return out
}

func acHasEvidence(ac model.AcceptanceItem) bool {
	if ac.HumanResult == "pass" {
		return true
	}
	return strings.TrimSpace(ac.EvidenceRef) != ""
}

func hasCompletedCover(tasks []*model.Task) bool {
	for _, t := range tasks {
		if t.Status == model.TaskCompleted {
			return true
		}
	}
	return false
}

func taskIDs(tasks []*model.Task) []string {
	if len(tasks) == 0 {
		return nil
	}
	ids := make([]string, 0, len(tasks))
	for _, t := range tasks {
		ids = append(ids, t.ID)
	}
	return ids
}
