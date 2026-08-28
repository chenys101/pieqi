// Package agent: session.go 定义中性 AgentSession 抽象（multi-agent 修订版 §3.1）。
//
// 业务层（Task / 会话编排 / 审批 UI）只依赖本文件里的 AgentSession + Caps + 中性事件；
// 厂商/传输品牌名（Print、SDKBridge、ACP、ClaudeCode…）不出现这些公开类型。
// 底层实现按 agent 名（"claude" / "qoder"）经工厂 Open 路由；传输（bridge / acp / print）
// 关在各 agent 实现包内部，调用方无感。
//
// 设计约束（评估文档 multi-agent-evaluation.md §2.1）：
//   - 事件投递定死为 callback（与现有 AgentAdapter 回调一致），每个事件带 SessionID + TurnSeq，
//     避免多轮 + 重连下的关联歧义。
//   - 接口含 RespondPermission（方案 §8 依赖它；§3.1 原始表漏了）。
//   - 取舍：不再暴露 InjectToolResult / Done —— 工具结果由 agent 自驱（ACP），
//     进程死亡信号由底层实现内部兜住（桥内子进程崩溃 resume / Error 事件），不进公开接口。
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// Caps 描述一个 agent 会话的能力；续问策略（§3.3）只看它做决策：
//
//	Prompt 追加：
//	  session 仍有效 && Caps.MultiTurnPersistent → 直接 Prompt
//	  else if Caps.ResumeSupported → 携带 resume 信息重新 Open 再 Prompt
//	  else → 新会话或明确失败
type Caps struct {
	MultiTurnPersistent bool // 同会话多轮且尽量保持底层执行器存活
	ResumeSupported     bool // 可凭会话 id 恢复上下文
	Streaming           bool // 增量文本事件（TextDelta）
}

// EventKind 中性事件种类（对应方案 §3.1 事件表）。
type EventKind string

const (
	EventTextDelta        EventKind = "text_delta"        // 助手增量文本
	EventThinkingDelta    EventKind = "thinking_delta"    // 思考过程增量（可选）
	EventToolStart        EventKind = "tool_start"        // 工具开始
	EventToolEnd          EventKind = "tool_end"          // 工具结束（含状态/结果）
	EventPermissionNeeded EventKind = "permission_needed" // 需用户审批
	EventTurnEnd          EventKind = "turn_end"          // 本轮结束（可带 usage / resume id）
	EventError            EventKind = "error"             // 错误
	EventStateChanged     EventKind = "state_changed"     // idle/running/waiting_permission/closed
)

// TurnInfo TurnEnd 的载荷：本轮结束信息（带底层 resume id，供持久化/续问）。
type TurnInfo struct {
	ResumeID string          // 底层可续问的会话 id（如 sdkSessionId / claude session id）
	Usage    json.RawMessage // 本轮用量（可选，透传）
	CostUSD  float64         // 本轮成本估算（可选）
}

// Event 一个中性事件（经 OnEvent 注册的 callback 投递）。
type Event struct {
	Kind       EventKind
	TurnSeq    int    // 轮次序号（第几轮；多轮 + 重连下用于关联）
	SessionID  string // 会话 id
	Text       string // TextDelta / ThinkingDelta 的增量文本
	IsThought  bool   // ThinkingDelta 标记
	ToolCallID string // 工具事件
	ToolTitle  string
	ToolStatus string // tool_start: pending/in_progress；tool_end: completed/failed
	ToolKind   string
	RawInput   json.RawMessage
	RawOutput  json.RawMessage
	Permission PermissionRequest // PermissionNeeded 载荷
	Turn       *TurnInfo         // TurnEnd 载荷
	Err        error             // Error 事件
	State      string            // StateChanged 的新状态
}

// AgentSession 中性 agent 会话接口（§3.1）。
//
// 一个 AgentSession = 一个逻辑会话（一个 task 的 agent 上下文）。方法/事件名保持中性，
// 不含任何厂商/传输类型。事件经 OnEvent 注册的 callback 投递（定死 callback 形态）。
//
// 注：turn 结束的判定 = Prompt 正常返回；TurnEnd 事件供实现提供额外载荷（usage/resume id），
// 不可用作唯一的结束信号。
type AgentSession interface {
	ID() string
	Prompt(ctx context.Context, text string) error
	Cancel(ctx context.Context) error
	Close(ctx context.Context) error
	// RespondPermission 对 PermissionNeeded 事件给出审批响应。
	// allow=true 批准（选中 optionID）；allow=false 拒绝。
	RespondPermission(ctx context.Context, reqID string, allow bool, optionID string) error
	OnEvent(fn func(Event))
	Caps() Caps
}

// OpenParams 打开会话的参数（§3.2）。
type OpenParams struct {
	Agent      string // "claude" / "qoder"（业务只认 agent 名）
	Cwd        string
	ResumeFrom string // 续问：复用已有会话上下文
	// Metadata 预留：taskId 等
}

// SessionProvider 创建 AgentSession 的工厂（按 agent 名注册）。
type SessionProvider func(ctx context.Context, p OpenParams) (AgentSession, error)

// ErrUnknownAgent Open 收到未注册的 agent 名。
var ErrUnknownAgent = errors.New("agent: unknown agent")

var (
	sessionProviders   = map[string]SessionProvider{}
	sessionProvidersMu sync.RWMutex
)

// RegisterSessionProvider 注册按 agent 名创建 AgentSession 的工厂（claude / qoder 包 init 调用）。
func RegisterSessionProvider(agentName string, p SessionProvider) {
	sessionProvidersMu.Lock()
	defer sessionProvidersMu.Unlock()
	sessionProviders[agentName] = p
}

// Open 按 agent 名路由创建 AgentSession（§3.2 工厂）。
func Open(ctx context.Context, p OpenParams) (AgentSession, error) {
	sessionProvidersMu.RLock()
	prov, ok := sessionProviders[p.Agent]
	sessionProvidersMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownAgent, p.Agent)
	}
	return prov(ctx, p)
}

// sessionAdapter 把现有 AgentAdapter + sessionID 桥接为 AgentSession（不重写现有实现）。
// 供当前 ACP/print 路径在 bridge 落地前使用；bridge 客户端将原生实现 AgentSession。
//
// 仅桥接 delta / tool / permission 三类事件（现有 adapter 的能力边界）；TurnEnd/Error/
// StateChanged 由 Prompt 返回 + 上层驱动判定，bridge 客户端原生实现时补齐。
type sessionAdapter struct {
	adapter   AgentAdapter
	sessionID string
	caps      Caps

	eventMu sync.RWMutex
	onEvent func(Event)
}

var _ AgentSession = (*sessionAdapter)(nil)

// NewSessionAdapter 用现有 AgentAdapter + sessionID 构造 AgentSession 桥接。
// caps 由调用方按 adapter 类型提供（如 ACP 保活 MultiTurnPersistent=true）。
func NewSessionAdapter(adapter AgentAdapter, sessionID string, caps Caps) AgentSession {
	return &sessionAdapter{adapter: adapter, sessionID: sessionID, caps: caps}
}

// ID 返回会话的真实 id（可用于持久化与续问）。
func (s *sessionAdapter) ID() string { return s.adapter.RealSessionID(s.sessionID) }

// Prompt 发送一轮 prompt（阻塞到该轮结束）。
func (s *sessionAdapter) Prompt(ctx context.Context, text string) error {
	return s.adapter.SendPrompt(ctx, s.sessionID, text)
}

// Cancel 取消当前轮。
func (s *sessionAdapter) Cancel(ctx context.Context) error {
	return s.adapter.Cancel(ctx, s.sessionID)
}

// Close 关闭会话（幂等）。
func (s *sessionAdapter) Close(ctx context.Context) error {
	return s.adapter.Close(ctx)
}

// RespondPermission 审批响应：allow=true→Approve，allow=false→Deny。
func (s *sessionAdapter) RespondPermission(ctx context.Context, reqID string, allow bool, optionID string) error {
	if allow {
		return s.adapter.Approve(ctx, reqID, optionID)
	}
	return s.adapter.Deny(ctx, reqID)
}

// OnEvent 注册中性事件回调，把现有 split 回调桥接为统一 Event。
// 重复调用以最后一次为准；fn=nil 时停止投递（不解除 adapter 回调，投递侧判空）。
func (s *sessionAdapter) OnEvent(fn func(Event)) {
	s.eventMu.Lock()
	s.onEvent = fn
	s.eventMu.Unlock()
	if fn == nil {
		return
	}
	s.adapter.OnContentDelta(func(d ContentDelta) {
		k := EventTextDelta
		if d.IsThought {
			k = EventThinkingDelta
		}
		s.fire(Event{Kind: k, SessionID: d.SessionID, Text: d.Text, IsThought: d.IsThought})
	})
	s.adapter.OnToolCallUpdate(func(u ToolCallUpdateInfo) {
		k := EventToolStart
		if !u.IsNew {
			k = EventToolEnd
		}
		s.fire(Event{Kind: k, SessionID: u.SessionID, ToolCallID: u.ToolCallID,
			ToolTitle: u.Title, ToolStatus: u.Status, ToolKind: u.Kind,
			RawInput: u.RawInput, RawOutput: u.RawOutput})
	})
	s.adapter.OnPermissionRequest(func(req PermissionRequest) {
		s.fire(Event{Kind: EventPermissionNeeded, SessionID: req.SessionID, Permission: req})
	})
}

// Caps 返回会话能力。
func (s *sessionAdapter) Caps() Caps { return s.caps }

func (s *sessionAdapter) fire(ev Event) {
	s.eventMu.RLock()
	fn := s.onEvent
	s.eventMu.RUnlock()
	if fn != nil {
		fn(ev)
	}
}
