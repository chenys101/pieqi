package agent

import (
	"context"
	"encoding/json"
	"fmt"
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

// handleCursorCreatePlan 处理 Cursor 的 create_plan。
//
// 两条路径（2026-10-31 定，对应「plan 模式中断」排查）：
//
//  1. **已在 plan**：直接采纳清单、回 accepted，再切到 agent 以便后续执行
//     （hapi#1044：接受后停在 plan 会装死）。
//  2. **非 plan**（agent/ask/…）：agent 是在「建议进入计划模式」。先把清单
//     推到 Task.Plan 给人预览，再经审批卡问「是否进入计划模式」；确认后
//     set_mode(plan)+accepted；拒绝则清空预览并回 cancelled。
//
// 无 OnPermissionRequest 时（单测 / 未接线）非 plan 也直接采纳，避免挂死。
func (a *ACPAgent) handleCursorCreatePlan(ctx context.Context, params json.RawMessage) (any, error) {
	var p cursorCreatePlanParams
	_ = json.Unmarshal(params, &p)

	entries := cursorCreatePlanEntries(p)

	a.modeMu.RLock()
	cur := a.modeState.CurrentID
	a.modeMu.RUnlock()

	if cur == "plan" {
		return a.acceptCursorCreatePlan(ctx, p, entries, cur)
	}

	// 非 plan：先预览清单，再等人确认是否进入计划模式。
	a.emitPlan(entries)
	a.rememberCursorTodos(p.Todos)

	ok, err := a.awaitEnterPlanMode(ctx, p, entries)
	if err != nil {
		a.clearEmittedPlan()
		return cursorNestedOutcome(map[string]any{"outcome": "cancelled"}), err
	}
	if !ok {
		a.clearEmittedPlan()
		a.logger.Debug("acp extension default reply",
			zap.String("method", "cursor/create_plan"),
			zap.String("reply", "cancelled"),
			zap.String("reason", "user_declined_plan_mode"),
			zap.String("from_mode", cur),
		)
		return cursorNestedOutcome(map[string]any{"outcome": "cancelled"}), nil
	}

	sid := a.lastSessionID
	if sid != "" {
		if err := a.applyMode(ctx, sid, "plan"); err != nil {
			a.logger.Debug("acp cursor create_plan: enter plan mode failed",
				zap.String("from", cur), zap.Error(err))
		}
	}

	a.logger.Debug("acp extension default reply",
		zap.String("method", "cursor/create_plan"),
		zap.String("reply", "accepted"),
		zap.Int("entries", len(entries)),
		zap.String("name", p.Name),
		zap.String("from_mode", cur),
		zap.String("path", "confirm_enter_plan"),
	)
	return cursorNestedOutcome(map[string]any{"outcome": "accepted"}), nil
}

// acceptCursorCreatePlan plan 模式直通：emit → accepted → 切 agent。
func (a *ACPAgent) acceptCursorCreatePlan(ctx context.Context, p cursorCreatePlanParams, entries []PlanEntry, fromMode string) (any, error) {
	a.emitPlan(entries)
	a.rememberCursorTodos(p.Todos)

	sid := a.lastSessionID
	if sid != "" {
		if err := a.applyMode(ctx, sid, "agent"); err != nil {
			a.logger.Debug("acp cursor create_plan: switch to agent after accept failed",
				zap.String("from", fromMode), zap.Error(err))
		}
	}

	a.logger.Debug("acp extension default reply",
		zap.String("method", "cursor/create_plan"),
		zap.String("reply", "accepted"),
		zap.Int("entries", len(entries)),
		zap.String("name", p.Name),
		zap.String("from_mode", fromMode),
		zap.String("path", "already_plan"),
	)
	return cursorNestedOutcome(map[string]any{"outcome": "accepted"}), nil
}

// awaitEnterPlanMode 弹出「是否进入计划模式」审批卡，阻塞到用户决策。
// 无 onPerm 时返回 true（与 RequestPermission 无回调自动放行同口径）。
func (a *ACPAgent) awaitEnterPlanMode(ctx context.Context, p cursorCreatePlanParams, entries []PlanEntry) (bool, error) {
	a.cbMu.RLock()
	onPerm := a.onPerm
	a.cbMu.RUnlock()
	if onPerm == nil {
		return true, nil
	}

	reqID := strings.TrimSpace(p.ToolCallID)
	if reqID == "" {
		reqID = "cursor-create-plan"
	}
	// 与真实工具审批错开：避免盖掉同 id 的 RequestPermission。
	reqID = "plan-mode:" + reqID

	ch := make(chan PermissionResponse, 1)
	a.permMu.Lock()
	a.pendingPerms[reqID] = ch
	a.permMu.Unlock()

	title := strings.TrimSpace(p.Name)
	if title == "" {
		title = "计划"
	}
	summary := buildEnterPlanSummary(p, entries)
	onPerm(PermissionRequest{
		ReqID:       reqID,
		SessionID:   a.lastSessionID,
		ToolCallID:  p.ToolCallID,
		ToolTitle:   "进入计划模式：" + title,
		ToolKind:    "switch_mode", // L2：不会被 L0/L1 免审误放行
		ContentText: summary,
		Options: []PermissionOption{
			{ID: "allow-once", Name: "进入计划模式", Kind: PermissionOptionAllowOnce},
			{ID: "reject-once", Name: "保持当前模式", Kind: PermissionOptionRejectOnce},
		},
	})

	select {
	case resp := <-ch:
		return resp.Selected, nil
	case <-ctx.Done():
		a.takePending(reqID)
		return false, ctx.Err()
	case <-a.done:
		a.takePending(reqID)
		return false, fmt.Errorf("acp: agent exited during create_plan confirm")
	}
}

func buildEnterPlanSummary(p cursorCreatePlanParams, entries []PlanEntry) string {
	var b strings.Builder
	b.WriteString("Agent 建议进入计划模式。确认后采纳此计划；拒绝则保持当前模式。\n\n")
	if ov := strings.TrimSpace(p.Overview); ov != "" {
		b.WriteString(ov)
		b.WriteString("\n\n")
	}
	if pl := strings.TrimSpace(p.Plan); pl != "" {
		b.WriteString(pl)
		b.WriteString("\n\n")
	}
	if len(entries) > 0 {
		b.WriteString(fmt.Sprintf("清单 %d 项：\n", len(entries)))
		for i, e := range entries {
			if i >= 8 {
				b.WriteString(fmt.Sprintf("…另有 %d 项\n", len(entries)-8))
				break
			}
			b.WriteString("· ")
			b.WriteString(e.Content)
			b.WriteByte('\n')
		}
	}
	return strings.TrimSpace(b.String())
}

func cursorCreatePlanEntries(p cursorCreatePlanParams) []PlanEntry {
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
	return entries
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

// clearEmittedPlan 用户拒绝进入 plan 时清掉预览快照。
func (a *ACPAgent) clearEmittedPlan() {
	a.cbMu.RLock()
	pfn := a.onPlan
	a.cbMu.RUnlock()
	if pfn == nil {
		return
	}
	pfn(PlanUpdate{SessionID: a.lastSessionID, Entries: nil})
	a.rememberCursorTodos(nil)
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
