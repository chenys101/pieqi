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
	// PromptRich 发一轮带图（可 0 张）的 prompt：images 为空时语义等价于 Prompt。
	//
	// 放在接口上（而不是像 TurnModelSetter 那样做可选接口）是因为**它的降级语义
	// 必须在接口层定死**：底层不支持图片时，实现要么明确报错、要么明确拒收 ——
	// 绝不能静默丢掉图继续发文本（那会让用户以为 agent"看过图了"）。
	// 各实现照此自决，调用方只需处理返回的 error。
	PromptRich(ctx context.Context, text string, images []ImageInput) error
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
	// TaskID 透传给 SessionConfig.TaskID —— 注入 PIEQI_TASK_ID 的**唯一**来源。
	// 空 = 非任务场景（如标题生成），此时不注入环境变量。
	TaskID string
	// Model 透传给 SessionConfig.Model —— 本次会话首轮要用的模型（不透明选择值，
	// 空 = 用 agent 自己的默认）。语义与取值见 SessionConfig.Model。
	Model string
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
// 桥接的事件：delta / tool / permission 三类由现有 adapter 回调直译；**error 由底层
// adapter.Done()（进程退出/连接断开）翻译而来**（见 watchExit）；TurnEnd/StateChanged
// 由 Prompt 返回 + 上层驱动判定，bridge 客户端原生实现时补齐。
type sessionAdapter struct {
	adapter   AgentAdapter
	sessionID string
	caps      Caps

	// eventMu 守护 onEvent 与三个生命周期标记。注意：投递事件前必须先在锁内取出 fn、
	// 解锁后再调用，不可持锁调 fire（RWMutex 不可重入）。
	//   closed       会话已显式 Close（正常关停，不上报 EventError）。
	//   exited       进程/连接已异常终止（Done 关闭且非 Close 所致）。
	//   errDelivered EventError 是否已投递（保证只投一次；注册晚于死亡时由 OnEvent 补投）。
	eventMu      sync.RWMutex
	onEvent      func(Event)
	closed       bool
	exited       bool
	errDelivered bool

	watchOnce sync.Once
}

var _ AgentSession = (*sessionAdapter)(nil)

// NewSessionAdapter 用现有 AgentAdapter + sessionID 构造 AgentSession 桥接。
// caps 由调用方按 adapter 类型提供（如 ACP 保活 MultiTurnPersistent=true）。
func NewSessionAdapter(adapter AgentAdapter, sessionID string, caps Caps) AgentSession {
	s := &sessionAdapter{adapter: adapter, sessionID: sessionID, caps: caps}
	s.watchExit()
	return s
}

// ID 返回会话的真实 id（可用于持久化与续问）。
func (s *sessionAdapter) ID() string { return s.adapter.RealSessionID(s.sessionID) }

// Prompt 发送一轮 prompt（阻塞到该轮结束）。
func (s *sessionAdapter) Prompt(ctx context.Context, text string) error {
	return s.adapter.SendPrompt(ctx, s.sessionID, text)
}

// PromptRich 发一轮带图的 prompt。
//
// 底层 adapter 实现 RichPromptSender 时透传；否则：
//   - images 为空 → 回落到纯文本 Prompt（与调用方不传图完全一致，不是降级）；
//   - images 非空 → **明确报错**。静默丢掉图继续发文本是最坏的选择：
//     用户会以为 agent 看过图，而 agent 只会对着没图的上下文说些不相干的话。
func (s *sessionAdapter) PromptRich(ctx context.Context, text string, images []ImageInput) error {
	if len(images) == 0 {
		return s.adapter.SendPrompt(ctx, s.sessionID, text)
	}
	sender, ok := s.adapter.(RichPromptSender)
	if !ok {
		return fmt.Errorf("%w: 当前 agent 传输不支持图片（%T）", ErrImageNotSupported, s.adapter)
	}
	return sender.SendRichPrompt(ctx, s.sessionID, text, images)
}

// SupportsImagePrompt 透传底层的图片入站能力；底层不支持时返回 false。
// 供上层（API）决定要不要把"可发图"暴露给前端。
func (s *sessionAdapter) SupportsImagePrompt() bool {
	capable, ok := s.adapter.(ImagePromptCapable)
	return ok && capable.SupportsImagePrompt()
}

// SetTurnModel 把本轮模型选择转交给底层 adapter（仅实现了 TurnModelSetter 的会生效，
// 即 ACP 系；claude 桥/print 静默忽略）。见 TurnModelSetter 的说明。
func (s *sessionAdapter) SetTurnModel(model string) {
	if setter, ok := s.adapter.(TurnModelSetter); ok {
		setter.SetTurnModel(model)
	}
}

// Cancel 取消当前轮。
func (s *sessionAdapter) Cancel(ctx context.Context) error {
	return s.adapter.Cancel(ctx, s.sessionID)
}

// Close 关闭会话（幂等）。先置 closed（让 watchExit 把随后的 Done 视作正常关停而非异常）。
func (s *sessionAdapter) Close(ctx context.Context) error {
	s.eventMu.Lock()
	s.closed = true
	s.eventMu.Unlock()
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
	// 死亡早于注册（watchExit 判定异常终止时还没有回调可投）：补投一次，避免信号丢失。
	pending := fn != nil && s.exited && !s.errDelivered
	if pending {
		s.errDelivered = true
	}
	s.eventMu.Unlock()
	if fn == nil {
		return
	}
	if pending {
		fn(s.exitEvent())
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

// watchExit 把底层 adapter 的进程死亡信号（Done 关闭）翻译为中性 EventError 事件。
//
// 为什么必须在这一层兜住：AgentSession 的公开接口按设计不暴露 Done（见文件头"进程死亡
// 信号由底层实现内部兜住"），而上层 sessionBackedAdapter 只在收到 EventError 时才
// markDone()，TaskRunner.adapterDead 才据此摘除死会话。若 ACP 侧死亡只关到
// ACPAgent.done 而不上报，信号就断在 adapter 内部——续问会复用已死会话，写向已关闭的
// stdin 失败（"acp: prompt: write |1: file already closed"）。
//
// 会话被显式 Close 时 Done 同样会关闭，那不是异常终止，故用 closed 标记区分。
func (s *sessionAdapter) watchExit() {
	s.watchOnce.Do(func() {
		go func() {
			<-s.adapter.Done()
			s.eventMu.Lock()
			if s.closed {
				s.eventMu.Unlock()
				return
			}
			s.exited = true
			fn := s.onEvent
			deliver := fn != nil && !s.errDelivered
			if deliver {
				s.errDelivered = true
			}
			s.eventMu.Unlock()
			if deliver {
				fn(s.exitEvent())
			}
		}()
	})
}

// exitEvent 构造"底层进程异常退出"的中性事件。
func (s *sessionAdapter) exitEvent() Event {
	return Event{Kind: EventError, SessionID: s.ID(), Err: errors.New("agent process exited")}
}

func (s *sessionAdapter) fire(ev Event) {
	s.eventMu.RLock()
	fn := s.onEvent
	s.eventMu.RUnlock()
	if fn != nil {
		fn(ev)
	}
}
