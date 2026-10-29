package agent

import (
	"context"
	"encoding/json"
	"strings"

	"go.uber.org/zap"
)

// Cursor 扩展方法的应答必须是**嵌套** outcome（官方 docs/cli/acp）：
//
//	{ "outcome": { "outcome": "accepted", ... } }
//
// 平铺 `{ "outcome": "accepted" }` 会被 Cursor 读成 undefined → 当成 cancelled
//（见 tiann/hapi#1044）。本文件只服务 cursor/create_plan 与 cursor/update_todos。

// cursorNestedOutcome 包一层 Cursor 要求的嵌套信封。
func cursorNestedOutcome(inner map[string]any) map[string]any {
	return map[string]any{"outcome": inner}
}

type cursorTodo struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Status  string `json:"status"`
}

type cursorCreatePlanParams struct {
	ToolCallID string `json:"toolCallId"`
	Name       string `json:"name"`
	Overview   string `json:"overview"`
	Plan       string `json:"plan"`
	Todos      []cursorTodo `json:"todos"`
	IsProject  bool   `json:"isProject"`
	Phases     []struct {
		Name  string       `json:"name"`
		Todos []cursorTodo `json:"todos"`
	} `json:"phases"`
}

type cursorUpdateTodosParams struct {
	ToolCallID string       `json:"toolCallId"`
	Todos      []cursorTodo `json:"todos"`
	Merge      bool         `json:"merge"`
}

// handleCursorCreatePlan 接受计划、写入 Task.Plan 时间线，并回 nested accepted。
//
// 策略（P2 列表渲染优先）：客户端直接 accept，立刻渲染清单；不弹审批卡。
// 若当前模式是 plan/ask，切到 agent，避免「计划通过却停住不执行」（hapi#1044 同款）。
func (a *ACPAgent) handleCursorCreatePlan(ctx context.Context, params json.RawMessage) (any, error) {
	var p cursorCreatePlanParams
	_ = json.Unmarshal(params, &p)

	entries := cursorTodosToPlanEntries(p.Todos)
	if len(entries) == 0 {
		for _, ph := range p.Phases {
			for _, t := range ph.Todos {
				content := t.Content
				if ph.Name != "" && content != "" {
					content = ph.Name + " · " + content
				} else if ph.Name != "" {
					content = ph.Name
				}
				entries = append(entries, PlanEntry{
					Content:  content,
					Status:   cursorTodoStatus(t.Status),
					Priority: "medium",
				})
			}
		}
	}
	// 没有 todos 时，用 overview / plan 正文顶一条，保证时间线至少有卡片。
	if len(entries) == 0 {
		text := strings.TrimSpace(p.Overview)
		if text == "" {
			text = strings.TrimSpace(p.Name)
		}
		if text == "" {
			text = strings.TrimSpace(firstLine(p.Plan))
		}
		if text == "" {
			text = "（空计划）"
		}
		entries = []PlanEntry{{Content: text, Status: "pending", Priority: "medium"}}
	}

	a.emitPlan(entries)
	a.rememberCursorTodos(p.Todos)

	// plan/ask → agent，让接受计划后能继续改代码。
	a.modeMu.RLock()
	cur := a.modeState.CurrentID
	a.modeMu.RUnlock()
	sid := a.lastSessionID
	if sid != "" && (cur == "plan" || cur == "ask" || cur == "architect" || cur == "chat") {
		if err := a.applyMode(ctx, sid, "agent"); err != nil {
			a.logger.Debug("acp cursor create_plan: switch to agent after accept failed",
				zap.String("from", cur), zap.Error(err))
		}
	}

	a.logger.Debug("acp extension default reply",
		zap.String("method", "cursor/create_plan"),
		zap.String("reply", "accepted"),
		zap.Int("entries", len(entries)),
		zap.String("name", p.Name),
	)
	return cursorNestedOutcome(map[string]any{"outcome": "accepted"}), nil
}

// handleCursorUpdateTodos 把 todos 刷进 Plan（merge 时与上次缓存合并）。
func (a *ACPAgent) handleCursorUpdateTodos(params json.RawMessage, asRequest bool) (any, error) {
	var p cursorUpdateTodosParams
	_ = json.Unmarshal(params, &p)

	todos := p.Todos
	if p.Merge {
		todos = mergeCursorTodos(a.cursorTodosSnapshot(), todos)
	}
	entries := cursorTodosToPlanEntries(todos)
	a.emitPlan(entries)
	a.rememberCursorTodos(todos)

	if !asRequest {
		a.logger.Debug("acp extension notification",
			zap.String("method", "cursor/update_todos"),
			zap.Int("todos", len(todos)),
			zap.Bool("merge", p.Merge),
		)
		return nil, nil
	}
	a.logger.Debug("acp extension default reply",
		zap.String("method", "cursor/update_todos"),
		zap.String("reply", "accepted"),
		zap.Int("todos", len(todos)),
	)
	outTodos := make([]map[string]any, 0, len(todos))
	for _, t := range todos {
		outTodos = append(outTodos, map[string]any{
			"id": t.ID, "content": t.Content, "status": cursorTodoStatus(t.Status),
		})
	}
	return cursorNestedOutcome(map[string]any{
		"outcome": "accepted",
		"todos":   outTodos,
	}), nil
}

func (a *ACPAgent) emitPlan(entries []PlanEntry) {
	a.cbMu.RLock()
	pfn := a.onPlan
	a.cbMu.RUnlock()
	if pfn == nil || len(entries) == 0 {
		return
	}
	pfn(PlanUpdate{SessionID: a.lastSessionID, Entries: entries})
}

func (a *ACPAgent) rememberCursorTodos(todos []cursorTodo) {
	a.cursorTodoMu.Lock()
	defer a.cursorTodoMu.Unlock()
	cp := make([]cursorTodo, len(todos))
	copy(cp, todos)
	a.cursorTodos = cp
}

func (a *ACPAgent) cursorTodosSnapshot() []cursorTodo {
	a.cursorTodoMu.Lock()
	defer a.cursorTodoMu.Unlock()
	cp := make([]cursorTodo, len(a.cursorTodos))
	copy(cp, a.cursorTodos)
	return cp
}

func mergeCursorTodos(base, patch []cursorTodo) []cursorTodo {
	byID := make(map[string]cursorTodo, len(base)+len(patch))
	order := make([]string, 0, len(base)+len(patch))
	for _, t := range base {
		id := t.ID
		if id == "" {
			id = t.Content
		}
		if _, ok := byID[id]; !ok {
			order = append(order, id)
		}
		byID[id] = t
	}
	for _, t := range patch {
		id := t.ID
		if id == "" {
			id = t.Content
		}
		if _, ok := byID[id]; !ok {
			order = append(order, id)
		}
		byID[id] = t
	}
	out := make([]cursorTodo, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out
}

func cursorTodosToPlanEntries(todos []cursorTodo) []PlanEntry {
	out := make([]PlanEntry, 0, len(todos))
	for _, t := range todos {
		content := strings.TrimSpace(t.Content)
		if content == "" {
			content = t.ID
		}
		if content == "" {
			continue
		}
		out = append(out, PlanEntry{
			Content:  content,
			Status:   cursorTodoStatus(t.Status),
			Priority: "medium",
		})
	}
	return out
}

func cursorTodoStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "completed", "complete", "done":
		return "completed"
	case "in_progress", "in-progress", "running", "active":
		return "in_progress"
	case "cancelled", "canceled":
		// ACP PlanEntry 没有 cancelled；降成 pending，避免未知状态。
		return "pending"
	default:
		return "pending"
	}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}
