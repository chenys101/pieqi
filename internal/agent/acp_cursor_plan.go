package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
)

// pendingModeSwitch 是扩展 RPC 应答之后再执行的 session/set_mode。
type pendingModeSwitch struct {
	sessionID string
	modeID    string
}

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
// 统一路径（2026-11-06）：不论当前是否已在 plan，都先推清单，再弹「批准计划 /
// 批准并执行」卡。批准 → accepted + 切 agent；拒绝 → cancelled、保留预览，
// 并确保落在 plan 以便继续改（非 plan 来源会排队 set_mode(plan)）。
//
// 旧「非 plan → 只问是否进入计划模式」会在用户点批准后停在 plan、本轮随即
// end_turn → 任务 completed，却从未问过「是否执行」（本机会话 d09f1751 复现）。
//
// 无 OnPermissionRequest 时（单测 / 未接线）直接采纳并切 agent，避免挂死。
func (a *ACPAgent) handleCursorCreatePlan(ctx context.Context, params json.RawMessage) (any, error) {
	var p cursorCreatePlanParams
	_ = json.Unmarshal(params, &p)

	entries := cursorCreatePlanEntries(p)

	a.modeMu.RLock()
	cur := a.modeState.CurrentID
	a.modeMu.RUnlock()

	return a.acceptCursorCreatePlan(ctx, p, entries, cur)
}

// acceptCursorCreatePlan emit →「批准并执行」审批卡 → accepted + 切 agent。
//
// ⚠️ 禁止在此同步 applyMode：本函数跑在 cursorExtBridge.tryIntercept 栈上，
// 同步 SetSessionMode 会等 stdout 回包，而 stdout 正被本桥占用 → 死锁。
func (a *ACPAgent) acceptCursorCreatePlan(ctx context.Context, p cursorCreatePlanParams, entries []PlanEntry, fromMode string) (any, error) {
	a.emitPlan(entries)
	a.rememberCursorTodos(p.Todos)

	ok, err := a.awaitPlanConfirm(ctx, p, entries)
	if err != nil {
		// 清单已推给前端预览；超时/退出不强制清，方便下一轮续改。
		return cursorNestedOutcome(map[string]any{"outcome": "cancelled"}), err
	}
	if !ok {
		// 拒绝执行：保留预览；若还不在 plan，切过去继续改（勿清空清单）。
		if fromMode != "plan" {
			a.queueModeAfterExtReply(a.lastSessionID, "plan")
		}
		a.logger.Debug("acp extension default reply",
			zap.String("method", "cursor/create_plan"),
			zap.String("reply", "cancelled"),
			zap.String("reason", "user_declined_plan_accept"),
			zap.String("from_mode", fromMode),
			zap.String("path", "confirm_accept_plan"),
		)
		return cursorNestedOutcome(map[string]any{"outcome": "cancelled"}), nil
	}

	a.queueModeAfterExtReply(a.lastSessionID, "agent")

	a.logger.Debug("acp extension default reply",
		zap.String("method", "cursor/create_plan"),
		zap.String("reply", "accepted"),
		zap.Int("entries", len(entries)),
		zap.String("name", p.Name),
		zap.String("from_mode", fromMode),
		zap.String("path", "confirm_accept_plan"),
	)
	return cursorNestedOutcome(map[string]any{"outcome": "accepted"}), nil
}

// queueModeAfterExtReply 登记「扩展 JSON-RPC 结果写回后再 set_mode」。
func (a *ACPAgent) queueModeAfterExtReply(sessionID, modeID string) {
	sessionID = strings.TrimSpace(sessionID)
	modeID = strings.TrimSpace(modeID)
	if a == nil || sessionID == "" || modeID == "" {
		return
	}
	a.pendingModeMu.Lock()
	a.pendingModeAfterExt = &pendingModeSwitch{sessionID: sessionID, modeID: modeID}
	a.pendingModeMu.Unlock()
}

// flushPendingModeAfterExtReply 在扩展应答已写入 agent stdin 之后调用。
// 异步 applyMode，让 bridge Read 循环能继续泵 stdout（含 set_mode 回包）。
func (a *ACPAgent) flushPendingModeAfterExtReply() {
	if a == nil {
		return
	}
	a.pendingModeMu.Lock()
	p := a.pendingModeAfterExt
	a.pendingModeAfterExt = nil
	a.pendingModeMu.Unlock()
	if p == nil {
		return
	}
	go func() {
		timeout := a.cfg.InitTimeout
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := a.applyMode(ctx, p.sessionID, p.modeID); err != nil {
			a.logger.Debug("acp deferred set_mode failed",
				zap.String("mode", p.modeID),
				zap.String("session", p.sessionID),
				zap.Error(err))
		}
	}()
}

// awaitPlanConfirm 弹出「批准并执行」审批卡，阻塞到用户决策。
// 无 onPerm 时返回 true（与 RequestPermission 无回调自动放行同口径）。
func (a *ACPAgent) awaitPlanConfirm(ctx context.Context, p cursorCreatePlanParams, entries []PlanEntry) (bool, error) {
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
	reqID = "plan-accept:" + reqID

	ch := make(chan PermissionResponse, 1)
	a.permMu.Lock()
	a.pendingPerms[reqID] = ch
	a.permMu.Unlock()

	title := strings.TrimSpace(p.Name)
	if title == "" {
		title = "计划"
	}
	toolTitle, allowName, rejectName, lead := planConfirmCopy(title)
	summary := buildPlanConfirmSummary(lead, p, entries)
	onPerm(PermissionRequest{
		ReqID:       reqID,
		SessionID:   a.lastSessionID,
		ToolCallID:  p.ToolCallID,
		ToolTitle:   toolTitle,
		ToolKind:    "switch_mode", // L2：不会被 L0/L1 免审误放行
		ContentText: summary,
		Options: []PermissionOption{
			{ID: "allow-once", Name: allowName, Kind: PermissionOptionAllowOnce},
			{ID: "reject-once", Name: rejectName, Kind: PermissionOptionRejectOnce},
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

func planConfirmCopy(title string) (toolTitle, allowName, rejectName, lead string) {
	return "批准计划：" + title,
		"批准并执行",
		"继续改计划",
		"计划已写完。批准后切到执行模式按此清单推进；拒绝则留在计划模式继续修改。\n\n"
}

func buildPlanConfirmSummary(lead string, p cursorCreatePlanParams, entries []PlanEntry) string {
	var b strings.Builder
	b.WriteString(lead)
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
