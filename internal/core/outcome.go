// outcome.go P1 Task Outcome（p1-design.md §6）：Task 结构化结果，手机端主验收面。
//
// 派生不存储（延续 ADR-0001）：Task 终态时规则生成完成度（completed/partial/failed），
// 聚合变更统计 / checks / 问题清单 / 回退审计。未终态时实时派生供中途验收。
package core

import (
	"encoding/json"
	"strconv"
	"time"

	"pieqi/internal/model"
)

// Outcome 完成度。
const (
	OutcomeCompleted = "completed" // 终态且无 failed check（或 agent 未跑 check）
	OutcomePartial   = "partial"   // 终态但存在 failed check
	OutcomeFailed    = "failed"    // task 本身 failed
)

// CheckSummary Outcome/Evidence 内嵌的 check 摘要。
type CheckSummary struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	ExitCode int    `json:"exit_code,omitempty"`
}

// RewindInfo 本 Task 发生过的回退（审计）。
type RewindInfo struct {
	ToTurn   int      `json:"to_turn"`
	Restored []string `json:"restored"`
	At       string   `json:"at"`
}

// TaskOutcome Task 的结构化结果（规划 §19 ⭐）。
type TaskOutcome struct {
	TaskID      string         `json:"task_id"`
	Status      string         `json:"status"` // completed | partial | failed
	Changes     ChangeSummary  `json:"changes"`
	Preview     *FeedbackPreview `json:"preview,omitempty"`
	Checks      []CheckSummary `json:"checks"`
	Issues      []string       `json:"issues"` // failed checks + task.error + 末轮 is_error
	Rewinds     []RewindInfo   `json:"rewinds"`
	GeneratedAt string         `json:"generated_at"`
}

// DeriveOutcome 规则派生 Outcome（不调模型，p1-design §14）。
//
// 输入：task（任意状态；未终态也派生供中途验收）、回填过 +/- 行数的 changes、
// 合并后的 checks（agent 派生 + rerun）、preview 状态（nil = 不可用）。
func DeriveOutcome(task *model.Task, changes []FileChange, checks []Check, preview *FeedbackPreview) TaskOutcome {
	outcome := TaskOutcome{
		TaskID:      task.ID,
		Changes:     SummarizeAll(changes),
		GeneratedAt: time.Now().Format(time.RFC3339),
	}
	if preview != nil {
		outcome.Preview = preview
	}

	// 完成度判定（§6.1 规则）
	hasFailedCheck := false
	for _, ck := range checks {
		outcome.Checks = append(outcome.Checks, CheckSummary{
			ID: ck.ID, Name: ck.Name, Status: ck.Status, ExitCode: ck.ExitCode,
		})
		if ck.Status == CheckFailed {
			hasFailedCheck = true
		}
	}
	switch {
	case task.Status == model.TaskFailed:
		outcome.Status = OutcomeFailed
	case hasFailedCheck:
		outcome.Status = OutcomePartial
	default:
		outcome.Status = OutcomeCompleted
	}

	// Issues：failed checks + task.Error + 末轮 is_error
	for _, ck := range checks {
		if ck.Status == CheckFailed {
			issue := "check 失败: " + ck.Name
			if ck.ExitCode != 0 {
				issue += " (exit " + strconv.Itoa(ck.ExitCode) + ")"
			}
			outcome.Issues = append(outcome.Issues, issue)
		}
	}
	if task.Error != "" {
		outcome.Issues = append(outcome.Issues, "task 错误: "+task.Error)
	}
	outcome.Issues = append(outcome.Issues, lastTurnErrors(task.Events)...)

	// 回退审计：rewind 事件载荷
	for _, ev := range task.Events {
		if ev.Type != model.EventRewind || len(ev.Input) == 0 {
			continue
		}
		var p struct {
			ToTurn   int      `json:"to_turn"`
			Restored []string `json:"restored"`
		}
		if json.Unmarshal(ev.Input, &p) == nil {
			outcome.Rewinds = append(outcome.Rewinds, RewindInfo{
				ToTurn: p.ToTurn, Restored: p.Restored, At: ev.At.Format(time.RFC3339),
			})
		}
	}
	return outcome
}

// lastTurnErrors 末轮（最后一个 EventUser 之后）失败的 tool_result 摘要。
func lastTurnErrors(events []model.TaskEvent) []string {
	lastUser := -1
	for i, ev := range events {
		if ev.Type == model.EventUser {
			lastUser = i
		}
	}
	var issues []string
	for i := lastUser + 1; i < len(events); i++ {
		ev := events[i]
		if ev.Type != model.EventToolResult || !ev.IsError {
			continue
		}
		name := ev.ToolName
		if name == "" {
			name = "tool"
		}
		issue := name + " 调用失败"
		if tail := tailTruncate(ev.Result, 200); tail != "" {
			issue += ": " + firstLine(tail)
		}
		issues = append(issues, issue)
	}
	return issues
}

// firstLine 取首行（issue 单行化）。
func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}
