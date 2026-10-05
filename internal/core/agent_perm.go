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
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"pieqi/internal/agent"
	"pieqi/internal/model"

	"go.uber.org/zap"
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
	logger  *zap.Logger

	// autoApprove 免审名单：ToolKind 命中即自动放行（选首个 allow 选项调 adapter.Approve），
	// 不置 waiting_input、不弹卡、不推 IM、不启动超时定时器。空 = 关闭（全部走人工审批）。
	autoApprove map[string]struct{}

	// sessionAlways 是用户在本次会话里点过"同类免审"的 ToolKind 集合。
	//
	// 为什么不让 agent 自己去记（ACP 的 allow_always / qodercli 的
	// proceed_always_and_save）：那等于把"这条以后都不用问了"写进 agent 的配置，
	// 而 pieqi 的 L2/L3 硬边界（model.RiskLevel.AutoApprovable）判据是**每一笔都要有人看**。
	// 透传 allow_always 之后 agent 不再发 RequestPermission，pieqi 连拦截的机会都没有 ——
	// 一次点击就把门禁从"人审"降级成"上次我点过"，而且跨会话、跨任务生效，无人能撤销。
	//
	// 所以这份记账留在 wire 里：受 pw.mu 保护、不落盘、随 wire.Unwire/会话销毁而消失。
	// 语义是"这个任务这一轮会话内，同类操作我认了"。
	sessionAlways map[string]struct{}

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
	toolTitle string                   // 工具名（展示卡标题），排队提升时复用
	toolKind  string                   // ACP ToolKind，排队提升时复用（决定 risk，见 RiskOfKind）
	summary   string                   // 决策摘要（buildPermSummary 结果），排队提升时复用
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
// autoApprove 为免审名单（按 ACP ToolKind 匹配）：命中的权限请求直接自动放行，不中断等人工审批。
// logger 为 nil 时用 zap.NewNop()（静默）。
//
// 返回 *PermissionWire，调用方经 Resolve 投递用户决策，结束时 Unwire 拆卸。
func WirePermission(adapter agent.AgentAdapter, bus *EventBus, store *TaskStore, taskID string, notify func(*model.Task, string), timeout time.Duration, autoApprove []string, logger *zap.Logger) *PermissionWire {
	if timeout <= 0 {
		timeout = defaultPermissionTimeout
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	pw := &PermissionWire{
		adapter:       adapter,
		bus:           bus,
		store:         store,
		taskID:        taskID,
		notify:        notify,
		timeout:       timeout,
		logger:        logger,
		pending:       make(map[string]*permPending),
		sessionAlways: make(map[string]struct{}),
	}
	pw.setAutoApprove(autoApprove)
	adapter.OnPermissionRequest(pw.onPermissionRequest)
	return pw
}

// setAutoApprove 把免审名单转成 map 供 O(1) 命中判定；空/nil = 关闭免审。
func (pw *PermissionWire) setAutoApprove(tools []string) {
	if len(tools) == 0 {
		pw.autoApprove = nil
		return
	}
	set := make(map[string]struct{}, len(tools))
	for _, t := range tools {
		set[t] = struct{}{}
	}
	pw.autoApprove = set
}

// onPermissionRequest OnPermissionRequest 回调实现：置 waiting_input + 推送 + 启动超时。
//
// 注意：本回调由 adapter 在 RequestPermission 中调用，实现应快速返回（adapter 内部阻塞等 Approve/Deny），
// 不要在此阻塞等待用户决策——用户决策经 Resolve 投递。参考 adapter.PermissionRequestFunc 注释。
func (pw *PermissionWire) onPermissionRequest(req agent.PermissionRequest) {
	// 免审名单命中（如 edit/delete/move 等文件改动类）：直接自动放行——
	// 不置 waiting_input、不弹卡、不推 IM、不启动超时，任务全程不中断。
	if pw.tryAutoApprove(req) {
		return
	}
	pw.mu.Lock()
	if pw.closed {
		// wire 已拆卸：不再处理，adapter 回调应已被置 nil，这里兜底直接拒。
		pw.mu.Unlock()
		return
	}
	entry := &permPending{
		reqID:     req.ReqID,
		toolTitle: req.ToolTitle,
		toolKind:  req.ToolKind,
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

	if !pw.show(req.ReqID, entry.toolTitle, entry.toolKind, entry.summary) {
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

// tryAutoApprove 免审名单命中判定与放行：ToolKind 在 autoApprove 中且请求带 allow 选项时，
// 选首个 allow 选项（allow_once 优先，次 allow_always）直接调 adapter.Approve 放行。
//
// 返回 true 表示已处理（不再走人工审批）；以下情况返回 false，调用方回退到正常人工审批：
//   - 免审名单为空 / ToolKind 未命中；
//   - 请求只有 reject 选项（无 allow 选项可放行）；
//   - adapter.Approve 失败（如请求已被另一路径解决）——回退走人工审批，由 30min 超时兜底，
//     不会永久卡死。
func (pw *PermissionWire) tryAutoApprove(req agent.PermissionRequest) bool {
	if !pw.autoApproves(req.ToolKind) {
		return false
	}
	optionID, ok := pickAllowOption(req.Options)
	if !ok {
		// 无 allow 选项无法自动放行：退回人工审批，让用户在卡上看到 reject 选项。
		pw.logger.Debug("auto-approve skipped: no allow option",
			zap.String("task", pw.taskID), zap.String("req", req.ReqID), zap.String("tool_kind", req.ToolKind))
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	err := pw.adapter.Approve(ctx, req.ReqID, optionID)
	cancel()
	if err != nil {
		pw.logger.Warn("auto-approve failed, fall back to manual approval",
			zap.String("task", pw.taskID), zap.String("req", req.ReqID),
			zap.String("tool_kind", req.ToolKind), zap.Error(err))
		return false
	}
	pw.logger.Debug("auto-approved permission (no review)",
		zap.String("task", pw.taskID), zap.String("req", req.ReqID),
		zap.String("tool_kind", req.ToolKind), zap.String("option", optionID))
	return true
}

// autoApproves 该 ToolKind 是否免审：配置名单（由 L0/L1 推导）命中，或用户本次会话点过
// "同类免审"。两个集合都在 pw.mu 下读，但**不持锁调 adapter**（那是一次网络往返）。
func (pw *PermissionWire) autoApproves(kind string) bool {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	if _, ok := pw.autoApprove[kind]; ok {
		return true
	}
	_, ok := pw.sessionAlways[kind]
	return ok
}

// rememberKind 记下"本会话同类免审"的 ToolKind。
//
// 空 kind 不记：它的意思是"我们不认识这个操作"（RiskOfKind 把它算作 L2 就是这个理由）。
// 记下空 kind 等于给以后所有叫不出名字的操作发通行证 —— 白名单会悄悄变成通配符。
func (pw *PermissionWire) rememberKind(kind string) {
	if kind == "" {
		return
	}
	pw.mu.Lock()
	pw.sessionAlways[kind] = struct{}{}
	pw.mu.Unlock()
	pw.logger.Info("same-kind approvals auto-passed for the rest of this session",
		zap.String("task", pw.taskID), zap.String("tool_kind", kind))
}

// show 把 reqID 展示为当前决策：置 waiting_input(approval) + Publish + IM 通知 + 启动超时定时器。
// 返回是否成功应用（task 不存在或已终态时为 false，调用方负责 Deny 并清理展示状态）。
func (pw *PermissionWire) show(reqID, toolTitle, toolKind, summary string) bool {
	updated, applied := pw.setWaitingApproval(reqID, toolTitle, toolKind, summary)
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
func (pw *PermissionWire) setWaitingApproval(reqID, toolTitle, toolKind, summary string) (*model.Task, bool) {
	applied := false
	updated, err := pw.store.Update(pw.taskID, func(t *model.Task) bool {
		// 终态任务不再暂停（与 TaskRunner.transition 语义一致）。
		if t.Status == model.TaskCompleted || t.Status == model.TaskFailed || t.Status == model.TaskCancelled {
			return false
		}
		t.Status = model.TaskWaitingInput
		t.CurrentDecision = &model.Decision{
			ID:   reqID,
			Kind: model.DecisionKindApproval,
			// 标题只放"一屏读得完的一句话"（见 permLabel）：qodercli 把整条命令塞进
			// toolCall.title，原样进标题会让标题栏和摘要栏渲染同一串文本（卡片长度翻倍）。
			// 完整内容始终在 Summary 里 —— 批准前必须能看到它将做什么。
			ToolName: permLabel(toolTitle, toolKind),
			// 风险分级在这里落定：**和自动放行判据共用同一张表**（riskLevelKinds）。
			// 两者必须是同一个真相 —— 若卡片按一张表显示"L3 破坏性"、
			// 而放行逻辑按另一张表认为它可以自动通过，用户看到的强度就是谎言。
			Risk:    RiskOfKind(toolKind),
			Summary: summary,
			// approve_session 只在这里出现 = 只有 ACP 路径支持"同类免审"。
			// hook 路径（claude PreToolUse）的 Resolve 不认这个值，前端按这份列表决定
			// 长第三个按钮 —— 用 Options 当能力声明，而不是让前端去猜"这是哪条路径"。
			Options:   []string{"approve", "deny", "approve_session"},
			CreatedAt: time.Now(),
		}
		applied = true
		return true
	})
	if !applied || updated == nil {
		// 任务不存在或已终态：这次暂停请求不该展示（也不该留下悬挂状态）。
		return nil, false
	}
	if err != nil {
		// 内存已生效（waiting_input + CurrentDecision 都置上了），只是没落盘。
		// **绝不能**当成「不展示」：那会让调用方静默 Deny（用户看不到卡就被拒），
		// 同时内存里留下一个永远没人能 Resolve 的 waiting_input。
		// 内存是运行时唯一真相，卡片照常展示；磁盘留旧快照，重启后由 load() 的
		// 孤儿恢复兜底。
		pw.logger.Warn("persist waiting approval failed (card shown anyway)",
			zap.String("task", pw.taskID), zap.String("req", reqID), zap.Error(err))
	}
	return updated, true
}

// Resolve 投递用户审批决策。由 M4 的 AgentManager.Intervene 路由调用（ACP 路径）。
//
//   - choice="approve"：从记录的 options 选首个 allow（allow_once 优先，次 allow_always）→ adapter.Approve(reqID, optionID)
//   - choice="approve_session"：同 approve，另外把该请求的 ToolKind 记进本会话免审集合
//     （pieqi 侧记账，不写 agent 配置；见 sessionAlways 的注释）
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
	toolKind := entry.toolKind
	pw.mu.Unlock()

	var adapterErr error
	switch choice {
	case "approve", "approve_session":
		optionID, ok := pickAllowOption(options)
		if !ok {
			return fmt.Errorf("no allow option to approve for decision %q", decisionID)
		}
		adapterErr = pw.callAdapterApprove(decisionID, optionID)
		// "同类免审"只在**这次真的放行了**之后记账：approve 失败说明这次没批，
		// 记下去会让以后同类静默通过。
		// 注意回给 agent 的仍是 allow_once —— 会话内的免审由 pieqi 自己判，
		// agent 侧那份"始终允许"不会被写（理由见 PermissionWire.sessionAlways）。
		if adapterErr == nil && choice == "approve_session" {
			pw.rememberKind(toolKind)
		}
	case "deny":
		if optID, ok := pickRejectOption(options); ok {
			// 选中 reject 选项即拒绝（ACP Selected outcome 带该 optionId）。
			adapterErr = pw.callAdapterApprove(decisionID, optID)
		} else {
			// 无 reject 选项：投递 Cancelled。
			adapterErr = pw.callAdapterDeny(decisionID)
		}
	default:
		return fmt.Errorf("invalid choice %q (want approve/approve_session/deny)", choice)
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
	toolTitle, toolKind, summary := "", "", ""
	if e, ok := pw.pending[next]; ok {
		toolTitle, toolKind, summary = e.toolTitle, e.toolKind, e.summary
	}
	pw.mu.Unlock()

	if pw.show(next, toolTitle, toolKind, summary) {
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
	if !applied || updated == nil {
		return nil, false
	}
	if err != nil {
		// 同 setWaitingApproval：内存已经回到 running，只是没落盘 —— 照常推送，
		// 否则前端会一直停在上一张审批卡上（状态在内存里已经变了，UI 不同步）。
		pw.logger.Warn("persist back-to-running failed (publishing anyway)",
			zap.String("task", pw.taskID), zap.String("decision", decisionID), zap.Error(err))
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
//
// 文案刻意短：手机上这条要能一眼看完"什么任务、什么操作、多重"，然后直接回 /approve。
// 风险等级带上（L2/L3 比"需要决策"更能提示该不该细看），但标题行不再重复工具名
// —— 摘要里通常已有。
func (pw *PermissionWire) notifyApproval(t *model.Task) {
	if pw.notify == nil || t == nil || t.OriginChannel == "" || t.OriginChatID == "" || t.CurrentDecision == nil {
		return
	}
	id := shortID(t.ID)
	summary := t.CurrentDecision.Summary
	if summary == "" {
		summary = t.CurrentDecision.ToolName
	}
	risk := t.CurrentDecision.Risk
	if risk == "" {
		risk = RiskL2 // 与 RiskOfKind / 前端 riskOf 同一条判据：缺省即 L2
	}
	text := fmt.Sprintf("⚠️ 任务 #%s · %s %s\n%s\n\n回复 /approve 或 /deny，或打开 PWA", id, risk, RiskLabel(risk), summary)
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

// buildPermSummary 由 PermissionRequest 构造决策摘要：**全文，不截断**。
//
// 摘要栏是用户点"批准"前唯一能看到"将发生什么"的地方，而飞书那条 `/approve` 就印在同一段
// 文本下面 —— 在这里截断等于让人盲批。长度问题交给前端折叠展示（ApprovalCard/Banner 的
// "展开全文"），不靠删内容解决。
//
// 优先级：ToolTitle → 从 RawInput 抽出的人读内容（命令/描述）→ KindLabel → 原始 JSON → ID。
//
// **RawInput 不能直接原样倒出来**：那是给机器看的 JSON，用户看到的是
// `{"command":"$p=\"$env:USERPROFILE\\...\"","description":"Check if task file exists",
// "justification":"沙箱两次拒绝...","sandbox_permissions":"danger-full-access"}` 这种
// 转义后的乱码（转义符、字段名、英文说明混在一起，看不到"要执行什么"）。
// dsh 的 ACP 实现恰好不发 title 也不发 kind，一旦走到兜底分支，卡片上就只剩这串 JSON。
func buildPermSummary(req agent.PermissionRequest) string {
	if req.ToolTitle != "" {
		return req.ToolTitle
	}
	// ToolTitle 缺失（dsh 常态）：从 RawInput 里挑人读的内容。
	if s := summaryFromRawInput(req.RawInput); s != "" {
		return s
	}
	if req.ToolKind != "" {
		return KindLabel(req.ToolKind)
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

// summaryFromRawInput 从工具入参里挑出**给人看**的内容，而不是把 JSON 原样倒出来。
//
// 各 agent 的入参形态不同，这里按"信息量从高到低"取第一个非空的已知字段：
//   - `command`（shell 工具）：直接就是要执行什么，最重要；
//   - `description`（dsh/claude 的意图说明）：command 缺失时的次选；
//   - `file_path` / `path`（编辑类工具）：要改哪个文件；
//   - `pattern` / `query` / `url`：找什么、访问哪里。
//
// 拼装规则：取到 command 时若同时有 description，把 description 作为前缀
// （"检查任务文件是否存在：<命令>"）—— 说明文字比命令原文更能一眼读懂意图。
// 都不认识则返回空串，由调用方回退到原始 JSON（不认识就不猜）。
func summaryFromRawInput(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	str := func(k string) string {
		s, _ := m[k].(string) // 非字符串（数组/对象）一律视作没有
		return strings.TrimSpace(s)
	}

	command := str("command")
	desc := str("description")

	if command != "" {
		if desc != "" && desc != command {
			return desc + "：" + command
		}
		return command
	}
	for _, k := range []string{"description", "file_path", "path", "pattern", "query", "url"} {
		if v := str(k); v != "" {
			return v
		}
	}
	return ""
}

// permLabelMaxRunes 审批卡标题的长度上限。超过就不如不说 —— 换成人读操作类型。
const permLabelMaxRunes = 40

// permLabel 审批卡标题：title 是"一行、够短"才用它，否则退化成 kind 的人读标签。
//
// 两条路各有得失，这里选"够短就说人话，否则只报类别"：
//   - claude-code / dsh 适配器给的可能是 "Bash" / "pwsh" 这种短名 → 原样保留；
//   - qodercli 给的是整条命令（实测最长 1109 字符，且与摘要栏是同一串文本）→ 只报"执行命令"，
//     完整命令留给摘要栏（可展开），标题不再重复一遍。
//
// 含换行的一律不用 —— 多行命令的**首行**往往没有信息量（`python -c "` 后面才是正文），
// 拿它当标题比只报类别更容易误导。
func permLabel(toolTitle, toolKind string) string {
	if toolTitle == "" || strings.ContainsAny(toolTitle, "\r\n") {
		return KindLabel(toolKind)
	}
	if len([]rune(toolTitle)) <= permLabelMaxRunes {
		return toolTitle
	}
	return KindLabel(toolKind)
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
