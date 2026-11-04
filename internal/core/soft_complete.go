package core

import (
	"fmt"

	"pieqi/internal/model"
)

// 软完成收紧（V2.0.1 / docs/notes/approval-miss-and-soft-complete.md）：
// Prompt 已返回且有产出时，不得把「未决审批 / 悬挂 tool_use」静默标成 completed。
// 文案前缀是稳定检索锚点——单测钉死，实现票不得随意改。
const (
	errApprovalUnresolved    = "approval unresolved"
	errToolHungWithoutResult = "tool hung without result"
)

// completeAfterTurn 是 ACP / print 轮末「有产出 → 终态」的唯一入口。
//
// 在 completeTask 之前做中断探测；命中则 failTask（仍可 Resume）。
// 路径 B（choice waiting_input）直接返回，行为不变。
func (tr *TaskRunner) completeAfterTurn(taskID, output string) {
	t, ok := tr.store.Get(taskID)
	if !ok || t == nil {
		return
	}
	// choice 路径 B：result 已拦截为 waiting_input(choice)，轮末兜底不得改写。
	if t.Status == model.TaskWaitingInput && t.CurrentDecision != nil &&
		t.CurrentDecision.Kind == model.DecisionKindChoice {
		return
	}
	if reason := tr.softCompleteInterruptReason(taskID, t); reason != "" {
		tr.failTask(taskID, reason)
		return
	}
	tr.completeTask(taskID, output)
}

// softCompleteInterruptReason 返回不得 completed 的稳定 Error 前缀；空串 = 可 completed。
// 优先级：未 settle 的 pending permission → 悬挂 tool_use。不用 plan 完成度作闸。
func (tr *TaskRunner) softCompleteInterruptReason(taskID string, t *model.Task) string {
	if t == nil {
		return ""
	}
	if ph := tr.permWire(taskID); ph != nil {
		if reqID, ok := ph.firstUnsettledPending(); ok {
			if reqID != "" {
				return fmt.Sprintf("%s: %s", errApprovalUnresolved, reqID)
			}
			return errApprovalUnresolved
		}
	}
	if toolHungWithoutResult(t.Events) {
		return errToolHungWithoutResult
	}
	return ""
}

// firstUnsettledPending 报告 wire 是否仍有未 settle 的 pending permission。
// 返回任意一个仍挂着的 reqID（用于 Error 附注）；ok=false 表示没有。
func (pw *PermissionWire) firstUnsettledPending() (reqID string, ok bool) {
	if pw == nil {
		return "", false
	}
	pw.mu.Lock()
	defer pw.mu.Unlock()
	for id := range pw.pending {
		return id, true
	}
	return "", false
}

// toolHungWithoutResult：末条相关事件（tool_use / tool_result）为 tool_use，
// 且事件流中无匹配 ToolUseID 的 tool_result。
func toolHungWithoutResult(events []model.TaskEvent) bool {
	lastIdx := -1
	for i := len(events) - 1; i >= 0; i-- {
		switch events[i].Type {
		case model.EventToolUse, model.EventToolResult:
			lastIdx = i
		default:
			continue
		}
		break
	}
	if lastIdx < 0 || events[lastIdx].Type != model.EventToolUse {
		return false
	}
	id := events[lastIdx].ToolUseID
	if id == "" {
		return true
	}
	for _, ev := range events {
		if ev.Type == model.EventToolResult && ev.ToolUseID == id {
			return false
		}
	}
	return true
}
