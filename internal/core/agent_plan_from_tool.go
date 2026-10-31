// Package core：把 todo_write / TodoWrite 一类工具入参抬升为 Task.Plan 快照。
//
// 背景：Cursor / 标准 ACP 会走 session/update(plan) → WirePlan；但 dsh 的 ACP
// 桥故意不投影 todo，Claude 只有 TodoWrite 工具事件。时间线已有语义卡，用户
// 还需要 composer 上方的 sticky 清单 —— 那只认 Task.Plan。
//
// 纪律：
//   - 只写 Task.Plan（整表替换），**不**追加 EventPlan（避免与 tool 卡双份）；
//   - 认不出 / 空列表 → 不动现有 Plan（宁可保留上一份，也不用空表盖掉）。
package core

import (
	"encoding/json"
	"strings"

	"pieqi/internal/model"
)

// isTodoPlanTool 判断工具名是否为「整表替换式计划清单」工具。
//
// 取首词再小写：qoder 会把参数拼进 Title（"TodoWrite …"），Claude 是 TodoWrite，
// dsh 是 todo_write —— 归一后都能命中。
func isTodoPlanTool(name string) bool {
	if name == "" {
		return false
	}
	head := strings.ToLower(strings.Fields(strings.TrimSpace(name))[0])
	switch head {
	case "todo_write", "todowrite", "write_todos":
		return true
	default:
		return false
	}
}

// parseTodoPlanEntries 从工具入参抽出计划条目。
// 兼容 todos / items；status 归一到 ACP 三态；缺 content 的条目跳过。
func parseTodoPlanEntries(input json.RawMessage) []model.PlanEntry {
	if len(input) == 0 {
		return nil
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(input, &root) != nil {
		return nil
	}
	raw, ok := root["todos"]
	if !ok {
		raw = root["items"]
	}
	if len(raw) == 0 {
		return nil
	}
	var items []struct {
		Content string `json:"content"`
		Status  string `json:"status"`
		ID      string `json:"id"`
	}
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	out := make([]model.PlanEntry, 0, len(items))
	for _, it := range items {
		content := strings.TrimSpace(it.Content)
		if content == "" {
			content = strings.TrimSpace(it.ID)
		}
		if content == "" {
			continue
		}
		out = append(out, model.PlanEntry{
			Content:  content,
			Priority: model.PlanPriorityMedium,
			Status:   normalizeTodoStatus(it.Status),
		})
	}
	return out
}

func normalizeTodoStatus(s string) model.PlanEntryStatus {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "completed", "complete", "done":
		return model.PlanCompleted
	case "in_progress", "in-progress", "running", "active":
		return model.PlanInProgress
	default:
		return model.PlanPending
	}
}

// ApplyTodoToolAsPlan 若 toolName/input 是 todo 清单工具，则整表替换 t.Plan。
// 返回是否改动了 Task。空解析结果不覆盖已有 Plan。
func ApplyTodoToolAsPlan(t *model.Task, toolName string, input json.RawMessage) bool {
	if t == nil || !isTodoPlanTool(toolName) {
		return false
	}
	entries := parseTodoPlanEntries(input)
	if len(entries) == 0 {
		return false
	}
	t.Plan = &model.PlanPayload{Entries: entries}
	return true
}
