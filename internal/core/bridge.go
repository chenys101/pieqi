package core

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"claude-bridge/internal/channel"
	"claude-bridge/internal/model"

	"go.uber.org/zap"
)

// Bridge 核心调度器。
// 审批流程通过 ApprovalGate 状态机管理 — Bridge 只负责编排：gate.Check/Approve/Deny → SessionRunner → reply。
type Bridge struct {
	logger        *zap.Logger
	userCtx       *UserContext
	sessionRunner *SessionRunner
	gate          *ApprovalGate
	timeout       time.Duration

	receivers []channel.MessageReceiver
	senders   map[string]channel.MessageSender

	userLocks map[string]*sync.Mutex
	locksMu   sync.Mutex

	// Din Agent 模式（din.enabled 时注入）
	din *dinMode
}

// dinMode Din Agent 模式的依赖集合。
type dinMode struct {
	store    *TaskStore
	runner   *TaskRunner
	bus      *EventBus
}

// EnableDin 注入 Din Agent 依赖，IM 消息走 TaskCreator 路径而非旧 SessionRunner。
func (b *Bridge) EnableDin(store *TaskStore, runner *TaskRunner, bus *EventBus) {
	b.din = &dinMode{store: store, runner: runner, bus: bus}
}

func NewBridge(logger *zap.Logger, userCtx *UserContext, sessionRunner *SessionRunner, timeout time.Duration) *Bridge {
	return &Bridge{
		logger:        logger,
		userCtx:       userCtx,
		sessionRunner: sessionRunner,
		gate:          NewApprovalGate("data/approvals.json", 30*time.Minute),
		timeout:       timeout,
		senders:       make(map[string]channel.MessageSender),
		userLocks:     make(map[string]*sync.Mutex),
	}
}

func (b *Bridge) RegisterReceiver(receiver channel.MessageReceiver) {
	b.receivers = append(b.receivers, receiver)
	if sender, ok := receiver.(channel.MessageSender); ok {
		b.senders[receiver.Name()] = sender
	}
	receiver.OnMessage(func(msg model.Message) {
		go b.handleMessage(msg)
	})
}

func (b *Bridge) RegisterSender(name string, sender channel.MessageSender) {
	b.senders[name] = sender
}

// -- Message handling --

func (b *Bridge) handleMessage(msg model.Message) {
	b.logger.Info("received",
		zap.String("channel", string(msg.Channel)),
		zap.String("content", msg.Content),
	)

	content := strings.TrimSpace(msg.Content)

	// Din Agent 模式：解析 #项目标签 -> 创建任务
	if b.din != nil {
		b.handleDinMessage(msg, content)
		return
	}

	sessionIndex := parseSessionIndex(content)

	identity, sessionID, err := b.userCtx.Resolve(msg, sessionIndex)
	if err != nil {
		b.logger.Error("resolve", zap.Error(err))
		return
	}
	if sessionID == "" {
		b.reply(msg, "❌ 活跃会话已达上限(50)，请等待旧会话过期")
		return
	}

	// Pending 期间拒绝新消息（同意/拒绝除外）
	if b.gate.Check(identity) && !isApprovalCmd(content) {
		b.reply(msg, "⚠️ 请先处理当前审批请求（回复 同意 或 拒绝）")
		return
	}

	b.lockUser(identity)
	defer b.unlockUser(identity)

	if handled, reply := b.handleCommand(msg, identity, &sessionID); handled {
		b.reply(msg, reply)
		return
	}

	bypass := b.gate.IsBypass(identity)
	mode := ""
	if bypass {
		mode = "bypassPermissions"
	}

	ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
	defer cancel()

	output, err := b.sessionRunner.Run(ctx, msg.Content, sessionID, mode)
	if err != nil && strings.Contains(err.Error(), "already in use") {
		b.logger.Warn("session id in use, creating new session", zap.String("old", sessionID))
		if newID := b.userCtx.NewSession(identity); newID != "" {
			sessionID = newID
			output, err = b.sessionRunner.Run(ctx, msg.Content, sessionID, mode)
		}
	}
	if err != nil {
		b.logger.Error("claude run", zap.Error(err))
		b.reply(msg, fmt.Sprintf("Claude error: %v", err))
		return
	}

	if !bypass && needsApproval(output) {
		b.gate.SetPending(identity, &PendingRequest{
			Prompt: msg.Content, SessionID: sessionID, Msg: msg, CreatedAt: time.Now(),
		})
		b.reply(msg, output+"\n\n---\n⚠️ 回复 同意 继续执行（同用户30min内不再询问）")
		return
	}

	b.reply(msg, output)
}

// -- Commands --

func (b *Bridge) handleCommand(msg model.Message, identity string, sessionID *string) (bool, string) {
	c := strings.TrimSpace(msg.Content)

	switch {
	case c == "/whoami":
		return true, fmt.Sprintf("Identity: %s\nChannel: %s", identity, msg.Channel)

	case c == "/sessions" || c == "列表":
		ss := b.userCtx.ListSessions(identity)
		if len(ss) == 0 {
			return true, "暂无活跃会话"
		}
		var ls []string
		for _, s := range ss {
			ago := time.Since(s.LastUsed).Truncate(time.Minute)
			pv := s.Preview
			if len(pv) > 15 {
				pv = pv[:15] + "..."
			}
			ls = append(ls, fmt.Sprintf("会话%d: %s (%s前)%s", s.Index, pv, ago, func() string {
				if s.Expired {
					return " [已过期]"
				}
				return ""
			}()))
		}
		return true, strings.Join(ls, "\n")

	case c == "/status":
		return true, fmt.Sprintf("✅ Claude Bridge\nclaude --resume %s", *sessionID)

	case c == "/new" || c == "新对话":
		newID := b.userCtx.NewSession(identity)
		if newID == "" {
			return true, "❌ 活跃会话已达上限(50)，请等待旧会话过期或手动清理"
		}
		*sessionID = newID
		return true, "✅ 已开启新会话"

	case c == "/permit" || c == "同意" || c == "批准" ||
		strings.EqualFold(c, "approve") || strings.EqualFold(c, "yes"):
		req := b.gate.Approve(identity)
		if req == nil {
			return true, "没有待审批的操作"
		}
		go b.retryWithBypass(req)
		return true, "✅ 已批准（同用户30min内免审）"

	case c == "/deny" || c == "拒绝" ||
		strings.EqualFold(c, "deny") || strings.EqualFold(c, "no"):
		if b.gate.Deny(identity) {
			return true, "❌ 已拒绝"
		}
		return true, "没有待审批的操作"
	}
	return false, ""
}

// -- Reply --

func (b *Bridge) reply(msg model.Message, text string) {
	s, ok := b.senders[string(msg.Channel)]
	if !ok {
		return
	}
	const max = 4000
	if len(text) <= max {
		b.sendChunk(s, msg.ChatID, text)
		return
	}
	chunks := splitText(text, max)
	for i, c := range chunks {
		p := ""
		if len(chunks) > 1 {
			p = fmt.Sprintf("[%d/%d] ", i+1, len(chunks))
		}
		b.sendChunk(s, msg.ChatID, p+c)
	}
}

func (b *Bridge) sendChunk(sender channel.MessageSender, chatID, text string) {
	ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
	defer cancel()
	if err := sender.Send(ctx, model.ReplyTarget{ChatID: chatID}, text); err != nil {
		b.logger.Error("send", zap.Error(err))
	}
}

// NotifyOrigin 往 task 的 IM 原渠道推送通知（waiting_input/完成/失败回执）。
// 由 TaskRunner 通过 SetNotifier 回调调用。无对应 sender 时静默跳过。
func (b *Bridge) NotifyOrigin(task *model.Task, text string) {
	if task.OriginChannel == "" || task.OriginChatID == "" {
		return
	}
	s, ok := b.senders[task.OriginChannel]
	if !ok {
		return
	}
	const max = 4000
	if len(text) <= max {
		b.sendChunk(s, task.OriginChatID, text)
		return
	}
	chunks := splitText(text, max)
	for i, c := range chunks {
		p := ""
		if len(chunks) > 1 {
			p = fmt.Sprintf("[%d/%d] ", i+1, len(chunks))
		}
		b.sendChunk(s, task.OriginChatID, p+c)
	}
}

// -- Retry with bypass --

func (b *Bridge) retryWithBypass(req *PendingRequest) {
	id, _, _ := b.userCtx.Resolve(req.Msg, "")
	b.lockUser(id)
	defer b.unlockUser(id)

	sessionID := req.SessionID
	ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
	defer cancel()

	output, err := b.sessionRunner.Run(ctx, req.Prompt, sessionID, "bypassPermissions")
	if err != nil && strings.Contains(err.Error(), "already in use") {
		b.logger.Warn("session in use for retry, creating new", zap.String("old", sessionID))
		if newID := b.userCtx.NewSession(id); newID != "" {
			sessionID = newID
			output, err = b.sessionRunner.Run(ctx, req.Prompt, sessionID, "bypassPermissions")
		}
	}
	if err != nil {
		b.reply(req.Msg, fmt.Sprintf("执行失败: %v", err))
		return
	}
	b.reply(req.Msg, output)
}

// -- Locks --

func (b *Bridge) lockUser(identity string) {
	b.locksMu.Lock()
	mu, ok := b.userLocks[identity]
	if !ok {
		mu = &sync.Mutex{}
		b.userLocks[identity] = mu
	}
	b.locksMu.Unlock()
	mu.Lock()
}

func (b *Bridge) unlockUser(identity string) {
	b.locksMu.Lock()
	mu := b.userLocks[identity]
	b.locksMu.Unlock()
	mu.Unlock()
}

// -- Helpers --

func isApprovalCmd(content string) bool {
	c := strings.TrimSpace(content)
	switch c {
	case "/permit", "同意", "批准", "/deny", "拒绝":
		return true
	}
	if strings.EqualFold(c, "approve") || strings.EqualFold(c, "yes") ||
		strings.EqualFold(c, "deny") || strings.EqualFold(c, "no") {
		return true
	}
	return false
}

func parseSessionIndex(content string) string {
	for _, p := range []string{"@", "会话", "切"} {
		if !strings.HasPrefix(content, p) {
			continue
		}
		rest := strings.TrimPrefix(content, p)
		end := 0
		for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
			end++
		}
		if end > 0 {
			if _, err := strconv.Atoi(rest[:end]); err == nil {
				return rest[:end]
			}
		}
	}
	return ""
}

func needsApproval(output string) bool {
	indicators := []string{
		"Bash(", "Grep(", "Glob(", "Edit(", "Write(", "Read(",
		"python ", "bash ", "kubectl ", "docker ",
		"Approve", "批准", "审批",
	}
	lower := strings.ToLower(output)
	for _, ind := range indicators {
		if strings.Contains(lower, strings.ToLower(ind)) {
			return true
		}
	}
	return false
}

func splitText(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}
	var chunks []string
	for len(text) > maxLen {
		cut := maxLen
		if idx := strings.LastIndex(text[:maxLen], "\n"); idx > maxLen/2 {
			cut = idx + 1
		}
		chunks = append(chunks, text[:cut])
		text = text[cut:]
	}
	if len(text) > 0 {
		chunks = append(chunks, text)
	}
	return chunks
}


// handleDinMessage Din 模式下处理 IM 消息。
//
// 项目不再预注册，IM 渠道无法用 #标签 解析目标项目，故 IM 仅作通知通道：
// 回复固定提示，引导用户在 PWA 新建任务。任务执行结果仍会通过 OriginChannel
// 回推到原 IM 会话（由 TaskRunner.notify 触发）。
func (b *Bridge) handleDinMessage(msg model.Message, content string) {
	b.reply(msg, "💡 请在 PWA 新建任务（选择项目 + 填写描述），任务进展会推送到这里。")
}
