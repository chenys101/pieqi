// Package core 的权限审批连接器：把 AgentAdapter 的 OnPermissionRequest 回调接到
// 任务状态机 + EventBus + IM 通知，构成 M3 协议级人工审批链路。
//
// 设计要点（仿 M2 的 WireContentDelta 连接器范式：core→agent 单向，一个 wire 绑一个 taskID）：
//   - 收到 agent.PermissionRequest 时把 task 置 waiting_input(approval)，建 Decision，
//     经 store.Update 持久化 + Publish task_updated（PWA 弹审批卡片，前端已支持 approve/deny），
//     并经 notify 回调往 IM 原渠道推送（仿 TaskRunner.notifyWaitingInput 文案）。
//   - 记录 reqID→options，供 Resolve 把用户的 approve/deny 映射为 ACP 的 allow/reject 选项 ID。
//   - 启动超时定时器（参考 hook_timeout，默认 30min）：到期调 adapter.Deny + IM 通知超时，
//     task 置回 running（agent 收到 Cancelled 后会改走它路，与 Phase 1 hook 超时语义一致）。
//   - Resolve 成功后 task 回 running，停掉定时器。
//
// 依赖方向：core → agent（单向）。internal/agent 不 import internal/core，无循环。
// M3 只让 PermissionWire.Resolve 可调用且单测覆盖；Intervene 路由（ACP Resolve vs hooks.Resolve）
// 留给 M4 的 AgentManager 决定。
package core

import (
	"context"
	"fmt"
	"sync"
	"time"

	"pieqi/internal/agent"
	"pieqi/internal/model"
)

// 默认审批超时上限（与 HookService 默认 30min 对齐）。WirePermission 传 timeout<=0 时用之。
const defaultPermissionTimeout = 30 * time.Minute

// PermissionWire WirePermission 返回的句柄。持有审批链路所需的依赖与 pending 表。
// 调用方在任务结束/取消时 Unwire 拆卸，避免回调悬挂。
type PermissionWire struct {
	adapter agent.AgentAdapter
	bus     *EventBus
	store   *TaskStore
	taskID  string
	notify  func(*model.Task, string)
	timeout time.Duration

	// mu 守护 pending 与 closed。每个 pending entry 自带 done 标志，
	// 保证 Resolve 与超时定时器之间只有一个能真正驱动 adapter（先到先得）。
	mu      sync.Mutex
	pending map[string]*permPending // reqID -> entry
	closed  bool

	// 并发审批排队（P5 修复）：task.CurrentDecision 是单槽位（前端单卡），并发到达的多个
	// 审批请求若都往里写会互相覆盖 → 被覆盖的请求在 UI 不可见、任务假死到超时。这里让任一
	// 时刻只有一个请求进 CurrentDecision（displayed），其余进 queue，当前卡被 Resolve/超时
	// 处理后才逐个提升展示，保证每个挂起审批最终都被用户看到。
	displayed string   // 当前已展示进 CurrentDecision 的 reqID；空 = 无展示
	queue     []string // 等待展示的 reqID（FIFO）
}

// permPending 一个待审批请求的本地状态。
type permPending struct {
	reqID     string
	toolTitle string            // 工具名（展示卡标题），排队提升时复用
	summary   string            // 决策摘要（buildPermSummary 结果），排队提升时复用
	options   []agent.PermissionOption // 记录的 ACP 选项，供 Resolve 映射 approve/deny
	timer     *time.Timer              // 超时定时器；到期调 adapter.Deny
	done      bool                     // 已被 Resolve 或超时处理（先到先得，另一方放弃）
}

// WirePermission 把一个 AgentAdapter 的 OnPermissionRequest 回调接到任务状态机 + EventBus + IM 通知。
//
// 注册后，adapter 每产出一个权限请求（ACP RequestPermission），回调内：
//  1. 记录 reqID→options；
//  2. task 置 waiting_input，建 Decision{Kind:approval, Options:[approve,deny]}，store.Update 持久化；
//  3. Publish task_updated（PWA 经前端 renderDetail 弹审批卡片）；
//  4. notify 回调往 IM 原渠道推送「需要决策」（仿 notifyWaitingInput 文案）；
//  5. 启动超时定时器：到期 adapter.Deny + IM 通知超时 + task 回 running。
//
// notify 为 nil 表示无 IM 渠道（HTTP/CLI 来源），跳过 IM 推送。
// timeout<=0 时取 defaultPermissionTimeout（30min）。
//
// 返回 *PermissionWire，调用方经 Resolve 投递用户决策，结束时 Unwire 拆卸。
func WirePermission(adapter agent.AgentAdapter, bus *EventBus, store *TaskStore, taskID string, notify func(*model.Task, string), timeout time.Duration) *PermissionWire {
	if timeout <= 0 {
		timeout = defaultPermissionTimeout
	}
	pw := &PermissionWire{
		adapter: adapter,
		bus:     bus,
		store:   store,
		taskID:  taskID,
		notify:  notify,
		timeout: timeout,
		pending: make(map[string]*permPending),
	}
	adapter.OnPermissionRequest(pw.onPermissionRequest)
	return pw
}

// onPermissionRequest OnPermissionRequest 回调实现：置 waiting_input + 推送 + 启动超时。
//
// 注意：本回调由 adapter 在 RequestPermission 中调用，实现应快速返回（adapter 内部阻塞等 Approve/Deny），
// 不要在此阻塞等待用户决策——用户决策经 Resolve 投递。参考 adapter.PermissionRequestFunc 注释。
func (pw *PermissionWire) onPermissionRequest(req agent.PermissionRequest) {
	pw.mu.Lock()
	if pw.closed {
		// wire 已拆卸：不再处理，adapter 回调应已被置 nil，这里兜底直接拒。
		pw.mu.Unlock()
		return
	}
	entry := &permPending{
		reqID:     req.ReqID,
		toolTitle: req.ToolTitle,
		summary:   buildPermSummary(req),
		options:   req.Options,
	}
	pw.pending[req.ReqID] = entry

	// 已有展示中的决策：本请求进队，等当前卡被 Resolve/超时处理后逐个提升（P5 修复，
	// 避免并发审批互相覆盖 CurrentDecision 导致部分审批在 UI 不可见、任务假死到超时）。
	if pw.displayed != "" {
		pw.queue = append(pw.queue, req.ReqID)
		pw.mu.Unlock()
		return
	}
	pw.displayed = req.ReqID
	pw.mu.Unlock()

	if !pw.show(req.ReqID, entry.toolTitle, entry.summary) {
		// task 不存在或已终态：清掉 pending 并 Deny，避免 agent 永久阻塞。
		pw.mu.Lock()
		delete(pw.pending, req.ReqID)
		pw.displayed = ""
		pw.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = pw.adapter.Deny(ctx, req.ReqID)
		cancel()
	}
}

// show 把 reqID 展示为当前决策：置 waiting_input(approval) + Publish + IM 通知 + 启动超时定时器。
// 返回是否成功应用（task 不存在或已终态时为 false，调用方负责 Deny 并清理展示状态）。
func (pw *PermissionWire) show(reqID, toolTitle, summary string) bool {
	updated, applied := pw.setWaitingApproval(reqID, toolTitle, summary)
	if !applied {
		return false
	}
	// PWA 经 task_updated 自动显示审批卡片（前端 renderDetail 已支持 approve/deny 按钮）。
	pw.bus.Publish(Event{Type: "task_updated", TaskID: pw.taskID, Task: updated})
	// IM 原渠道推送「需要决策」。
	pw.notifyApproval(updated)

	// 启动超时定时器：到期 Deny + IM 通知超时 + task 回 running/提升下一个。
	pw.mu.Lock()
	if pw.closed {
		pw.mu.Unlock()
		return true
	}
	if entry, ok := pw.pending[reqID]; ok && entry.timer == nil {
		entry.timer = time.AfterFunc(pw.timeout, func() {
			pw.handleTimeout(reqID)
		})
	}
	pw.mu.Unlock()
	return true
}

// setWaitingApproval 把 task 置 waiting_input(approval) 并建 CurrentDecision。
// 返回更新后的 task 副本与是否应用（task 不存在或已终态时 applied=false）。
func (pw *PermissionWire) setWaitingApproval(reqID, toolTitle, summary string) (*model.Task, bool) {
	applied := false
	updated, err := pw.store.Update(pw.taskID, func(t *model.Task) bool {
		// 终态任务不再暂停（与 TaskRunner.transition 语义一致）。
		if t.Status == model.TaskCompleted || t.Status == model.TaskFailed || t.Status == model.TaskCancelled {
			return false
		}
		t.Status = model.TaskWaitingInput
		t.CurrentDecision = &model.Decision{
			ID:        reqID,
			Kind:      model.DecisionKindApproval,
			ToolName:  toolTitle,
			Summary:   summary,
			Options:   []string{"approve", "deny"},
			CreatedAt: time.Now(),
		}
		applied = true
		return true
	})
	if err != nil || !applied {
		return nil, false
	}
	return updated, true
}

// Resolve 投递用户审批决策。由 M4 的 AgentManager.Intervene 路由调用（ACP 路径）。
//
//   - choice="approve"：从记录的 options 选首个 allow（allow_once 优先，次 allow_always）→ adapter.Approve(reqID, optionID)
//   - choice="deny"：选 reject 选项（reject_once 优先）用 Approve 选中；无 reject 选项则 adapter.Deny（→Cancelled）
//
// 成功后 task 回 running（清 CurrentDecision + Publish task_updated），停掉超时定时器。
// reqID 已不存在或已被 Resolve/超时处理时返回错误。
func (pw *PermissionWire) Resolve(decisionID, choice string) error {
	pw.mu.Lock()
	entry, ok := pw.pending[decisionID]
	if !ok {
		pw.mu.Unlock()
		return fmt.Errorf("no pending permission for decision %q", decisionID)
	}
	if entry.done {
		pw.mu.Unlock()
		return fmt.Errorf("permission %q already resolved or timed out", decisionID)
	}
	// 先到先得：标记 done 并摘除 entry，停定时器，确保超时回调不会重复驱动 adapter。
	entry.done = true
	if entry.timer != nil {
		entry.timer.Stop()
	}
	delete(pw.pending, decisionID)
	// 若投递的是排队中（尚未展示）的请求：同样把它移出 queue，避免 advance 之后又把它
	// 提升展示成一个已被处理过的空决策卡（stale client 防御）。
	if pw.displayed != decisionID {
		for i, r := range pw.queue {
			if r == decisionID {
				pw.queue = append(pw.queue[:i], pw.queue[i+1:]...)
				break
			}
		}
	}
	options := entry.options
	pw.mu.Unlock()

	var adapterErr error
	switch choice {
	case "approve":
		optionID, ok := pickAllowOption(options)
		if !ok {
			return fmt.Errorf("no allow option to approve for decision %q", decisionID)
		}
		adapterErr = pw.callAdapterApprove(decisionID, optionID)
	case "deny":
		if optID, ok := pickRejectOption(options); ok {
			// 选中 reject 选项即拒绝（ACP Selected outcome 带该 optionId）。
			adapterErr = pw.callAdapterApprove(decisionID, optID)
		} else {
			// 无 reject 选项：投递 Cancelled。
			adapterErr = pw.callAdapterDeny(decisionID)
		}
	default:
		return fmt.Errorf("invalid choice %q (want approve/deny)", choice)
	}
	if adapterErr != nil {
		return fmt.Errorf("adapter: %w", adapterErr)
	}

	// 推进：有排队审批则提升下一个继续展示（task 仍 waiting_input），否则 task 回 running。
	pw.advance(decisionID)
	return nil
}

// handleTimeout 超时定时器回调：Deny agent + IM 通知超时 + task 回 running。
// 先到先得：若已被 Resolve 处理则放弃。
func (pw *PermissionWire) handleTimeout(reqID string) {
	pw.mu.Lock()
	entry, ok := pw.pending[reqID]
	if !ok || entry.done {
		pw.mu.Unlock()
		return
	}
	entry.done = true
	delete(pw.pending, reqID)
	pw.mu.Unlock()

	// Deny agent（→ Cancelled），让 RequestPermission 不再死等。
	if err := pw.callAdapterDeny(reqID); err != nil {
		// best-effort：即便 Deny 失败也继续推进本地状态，避免 task 永久卡住。
	}

	// 推进：有排队审批则提升下一个继续展示，否则 task 回 running。
	updated, applied := pw.advance(reqID)
	if applied {
		pw.notifyTimeout(updated)
	}
}

// advance 在当前展示决策 reqID 被 Resolve 或超时处理完毕后推进：
//   - 有排队请求：提升队首为当前决策继续展示（task 仍 waiting_input，PWA 弹下一张卡，
//     新定时器），返回其 task 副本与 true；
//   - 否则：清空展示并让 task 回 running（现有 backToRunning），返回其结果。
//
// 仅当 reqID 仍是当前展示决策时生效；展示已切换（异常防御）或已关闭时 no-op 返回 nil,false。
// 提升失败（task 已终态等）：该请求无法展示，Deny 掉避免 agent 死等，并清空剩余队列。
func (pw *PermissionWire) advance(reqID string) (*model.Task, bool) {
	pw.mu.Lock()
	if pw.closed || pw.displayed != reqID {
		pw.mu.Unlock()
		return nil, false
	}
	if len(pw.queue) == 0 {
		pw.displayed = ""
		pw.mu.Unlock()
		return pw.backToRunning(reqID)
	}
	next := pw.queue[0]
	pw.queue = pw.queue[1:]
	pw.displayed = next
	toolTitle, summary := "", ""
	if e, ok := pw.pending[next]; ok {
		toolTitle, summary = e.toolTitle, e.summary
	}
	pw.mu.Unlock()

	if pw.show(next, toolTitle, summary) {
		t, _ := pw.store.Get(pw.taskID)
		return t, true
	}
	// 提升失败：task 已终态等，无法展示该请求。Deny 它并清空剩余队列（避免 agent 死等）。
	pw.mu.Lock()
	delete(pw.pending, next)
	rest := pw.queue
	pw.queue = nil
	pw.displayed = ""
	for _, r := range rest {
		delete(pw.pending, r)
	}
	pw.mu.Unlock()
	pw.denyRequests(append([]string{next}, rest...))
	return nil, false
}

// denyRequests 对一组 reqID 调 adapter.Deny（带超时上下文），逐个尽力而为。
func (pw *PermissionWire) denyRequests(reqIDs []string) {
	for _, rid := range reqIDs {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = pw.adapter.Deny(ctx, rid)
		cancel()
	}
}

// backToRunning 把 task 从 waiting_input 置回 running（清 CurrentDecision），并 Publish task_updated。
// 仅当 task 仍卡在 decisionID 这个决策上时才改；返回更新后的 task 与是否应用。
func (pw *PermissionWire) backToRunning(decisionID string) (*model.Task, bool) {
	applied := false
	updated, err := pw.store.Update(pw.taskID, func(t *model.Task) bool {
		if t.Status != model.TaskWaitingInput {
			return false
		}
		if t.CurrentDecision == nil || t.CurrentDecision.ID != decisionID {
			return false // 已切到新决策，不覆盖
		}
		t.Status = model.TaskRunning
		t.CurrentDecision = nil
		applied = true
		return true
	})
	if err != nil || !applied {
		return nil, false
	}
	pw.bus.Publish(Event{Type: "task_updated", TaskID: pw.taskID, Task: updated})
	return updated, true
}

// Unwire 拆卸：停所有定时器，Deny 挂起的请求（让 adapter 的 RequestPermission 不再死等），
// 注销回调（恢复 adapter 默认自动放行）。幂等。
func (pw *PermissionWire) Unwire() {
	pw.mu.Lock()
	if pw.closed {
		pw.mu.Unlock()
		return
	}
	pw.closed = true
	entries := pw.pending
	pw.pending = make(map[string]*permPending)
	pw.queue = nil
	pw.displayed = ""
	pw.mu.Unlock()

	for _, e := range entries {
		if e.timer != nil {
			e.timer.Stop()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = pw.adapter.Deny(ctx, e.reqID)
		cancel()
	}
	pw.adapter.OnPermissionRequest(nil)
}

// --- IM 通知文案（仿 TaskRunner.notifyWaitingInput 的 approval 分支） ---

// notifyApproval 往 IM 原渠道推送「需要决策」。无 IM 渠道或无 notify 时静默跳过。
func (pw *PermissionWire) notifyApproval(t *model.Task) {
	if pw.notify == nil || t == nil || t.OriginChannel == "" || t.OriginChatID == "" || t.CurrentDecision == nil {
		return
	}
	id := shortID(t.ID)
	summary := t.CurrentDecision.Summary
	if summary == "" {
		summary = t.CurrentDecision.ToolName
	}
	text := fmt.Sprintf("⚠️ 任务 #%s 需要决策\n%s\n\n回复 /approve 或 /deny，或打开 PWA 处理", id, summary)
	pw.notify(t, text)
}

// notifyTimeout 往 IM 原渠道推送「审批超时已自动拒绝」。
func (pw *PermissionWire) notifyTimeout(t *model.Task) {
	if pw.notify == nil || t == nil || t.OriginChannel == "" || t.OriginChatID == "" {
		return
	}
	text := fmt.Sprintf("⌛ 任务 #%s 审批超时，已自动拒绝", shortID(t.ID))
	pw.notify(t, text)
}

// --- adapter 调用包装（带超时上下文） ---

func (pw *PermissionWire) callAdapterApprove(reqID, optionID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return pw.adapter.Approve(ctx, reqID, optionID)
}

func (pw *PermissionWire) callAdapterDeny(reqID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return pw.adapter.Deny(ctx, reqID)
}

// --- 辅助 ---

// buildPermSummary 由 PermissionRequest 构造决策摘要：ToolTitle (+ToolKind) 优先，
// 次 ToolKind，再次 RawInput 截断，兜底 ToolCallID。参考 setWaitingInput 的 summary 语义。
func buildPermSummary(req agent.PermissionRequest) string {
	if req.ToolTitle != "" {
		if req.ToolKind != "" {
			return req.ToolTitle + " (" + req.ToolKind + ")"
		}
		return req.ToolTitle
	}
	if req.ToolKind != "" {
		return req.ToolKind
	}
	if len(req.RawInput) > 0 {
		const max = 200
		s := string(req.RawInput)
		r := []rune(s)
		if len(r) > max {
			s = string(r[:max]) + "…"
		}
		return s
	}
	return req.ToolCallID
}

// pickAllowOption 选首个 allow 选项（allow_once 优先，次 allow_always），返回其 optionId。
func pickAllowOption(opts []agent.PermissionOption) (string, bool) {
	for _, o := range opts {
		if o.Kind == agent.PermissionOptionAllowOnce {
			return o.ID, true
		}
	}
	for _, o := range opts {
		if o.Kind == agent.PermissionOptionAllowAlways {
			return o.ID, true
		}
	}
	return "", false
}

// pickRejectOption 选首个 reject 选项（reject_once 优先，次 reject_always），返回其 optionId。
func pickRejectOption(opts []agent.PermissionOption) (string, bool) {
	for _, o := range opts {
		if o.Kind == agent.PermissionOptionRejectOnce {
			return o.ID, true
		}
	}
	for _, o := range opts {
		if o.Kind == agent.PermissionOptionRejectAlways {
			return o.ID, true
		}
	}
	return "", false
}

// shortID 取任务 ID 的前 8 字符用于 IM 文案（与 notifyWaitingInput/notifyFinished 一致）。
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
