// Package claude 实现 AgentSession（multi-agent 修订版 §4.1）。
//
// 默认 transport = claude-sdk-bridge（官方 Agent SDK 常驻桥）；桥不可用时回退 print
// （factory.go）。本包只依赖 internal/agent 的中性接口，不把 bridge/HTTP 细节泄漏给业务。
package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"pieqi/internal/agent"
	"pieqi/internal/agent/claude/bridge"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// session 基于 claude-sdk-bridge 的 AgentSession 实现。
//
// 事件流：后台 goroutine 消费桥的 SSE（OpenEventStream），把桥事件翻译成中性
// agent.Event 投递给 OnEvent 回调，并维护 turn_end 结果表（按 clientRef）。
// Prompt 带 clientRef 发一轮，然后等自己那轮 turn_end 到达（bridge cancel 会补
// 合成 turn_end，故取消的轮也能返回，不挂死）。
type session struct {
	client *bridge.Client
	id     string
	cwd    string
	logger *zap.Logger

	ctx    context.Context
	cancel context.CancelFunc

	mu           sync.Mutex
	cond         *sync.Cond
	onEvent      func(agent.Event)
	closed       bool
	fatal        error
	turnEnds     map[string]turnOutcome // clientRef -> 结果
	lastResumeID string
}

// turnOutcome 一轮的结果：err 非空表示该轮失败/被取消。
type turnOutcome struct {
	err  error
	info *agent.TurnInfo
}

// 编译期断言：session 实现 AgentSession。
var _ agent.AgentSession = (*session)(nil)

func newSession(client *bridge.Client, id, cwd string, logger *zap.Logger) *session {
	if logger == nil {
		logger = zap.NewNop()
	}
	s := &session{
		client:   client,
		id:       id,
		cwd:      cwd,
		logger:   logger,
		turnEnds: make(map[string]turnOutcome),
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.cond = sync.NewCond(&s.mu)
	return s
}

// ID 返回桥会话 id（会话生命周期内的稳定句柄；续问 id 经 turn_end 事件携带）。
func (s *session) ID() string { return s.id }

// ResumeID 返回最近一轮 turn_end 携带的 SDK resume id（续问句柄；未跑过轮时为空）。
// sessionBackedAdapter.RealSessionID 据此持久化真实可续问 id。
func (s *session) ResumeID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastResumeID
}

// Caps 桥支持同进程多轮、可 resume、可流式。
func (s *session) Caps() agent.Caps {
	return agent.Caps{MultiTurnPersistent: true, ResumeSupported: true, Streaming: true}
}

// OnEvent 注册中性事件回调（最后一次为准）。
func (s *session) OnEvent(fn func(agent.Event)) {
	s.mu.Lock()
	s.onEvent = fn
	s.mu.Unlock()
}

// Prompt 发送一轮并阻塞到该轮结束（turn_end 按 clientRef 匹配）。
func (s *session) Prompt(ctx context.Context, text string) error {
	ref := uuid.New().String()
	if err := s.client.Prompt(ctx, s.id, text, ref); err != nil {
		return err
	}
	return s.waitTurn(ctx, ref)
}

// waitTurn 等 clientRef 对应轮的 turn_end（或致命错误/会话关闭/ctx 取消）。
func (s *session) waitTurn(ctx context.Context, ref string) error {
	// ctx 取消时唤醒 cond.Wait
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			s.cond.Broadcast()
		case <-stop:
		}
	}()
	defer close(stop)

	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		if o, ok := s.turnEnds[ref]; ok {
			return o.err
		}
		if s.fatal != nil {
			return s.fatal
		}
		if s.closed {
			return errors.New("claude: session closed")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		s.cond.Wait()
	}
}

// Cancel 取消当前轮（interrupt；桥会为被中断的轮补合成 turn_end）。
func (s *session) Cancel(ctx context.Context) error {
	return s.client.Cancel(ctx, s.id)
}

// RespondPermission 审批响应（allow=true 批准）。
func (s *session) RespondPermission(ctx context.Context, reqID string, allow bool, optionID string) error {
	return s.client.RespondPermission(ctx, s.id, reqID, allow, optionID)
}

// Close 关闭会话：删桥会话（杀子进程）+ 终止事件循环。幂等。
func (s *session) Close(ctx context.Context) error {
	s.mu.Lock()
	already := s.closed
	s.closed = true
	s.cond.Broadcast()
	s.mu.Unlock()
	if already {
		return nil
	}
	s.cancel() // 终止 SSE 读取
	err := s.client.CloseSession(ctx, s.id)
	if err != nil {
		s.logger.Warn("claude: close bridge session", zap.Error(err))
	}
	return nil
}

// fail 记录致命错误（子进程崩溃/事件流断开）并唤醒等待者。
// 同时以 EventError 通知订阅者（sessionBackedAdapter 据此关闭 Done()，识别会话已死）。
func (s *session) fail(err error) {
	s.mu.Lock()
	if s.fatal == nil {
		s.fatal = err
	}
	s.cond.Broadcast()
	fn := s.onEvent
	s.mu.Unlock()
	if fn != nil {
		fn(agent.Event{Kind: agent.EventError, SessionID: s.id, Err: err})
	}
}

func (s *session) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// runEventLoop 消费桥 SSE 事件流直到结束。会话创建后由 factory 启动。
func (s *session) runEventLoop() {
	body, err := s.client.OpenEventStream(s.ctx, s.id)
	if err != nil {
		s.fail(fmt.Errorf("claude: open event stream: %w", err))
		return
	}
	defer body.Close()
	bridge.ConsumeSSE(s.ctx, body, func(kind string, raw []byte) {
		s.handleEvent(kind, raw)
	})
	// 流正常走到头：会话被桥关闭或桥重启。
	if !s.isClosed() {
		s.fail(bridge.ErrStreamClosed)
	}
}

// handleEvent 处理一条桥事件：翻译为中性 agent.Event 并投递；维护 turn_end 表。
func (s *session) handleEvent(kind string, raw []byte) {
	var ev bridge.SSEEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		s.logger.Warn("claude: bad sse frame", zap.String("kind", kind), zap.Error(err))
		return
	}
	ge := s.toAgentEvent(ev)

	s.mu.Lock()
	switch ev.Kind {
	case "turn_end":
		out := turnOutcome{info: toTurnInfo(ev.Turn)}
		if ev.IsError || ev.Subtype != "" && ev.Subtype != "success" {
			msg := ev.Message
			if msg == "" {
				msg = fmt.Sprintf("turn %s", ev.Subtype)
			}
			out.err = fmt.Errorf("claude: %s", msg)
		}
		if ev.ClientRef != "" {
			s.turnEnds[ev.ClientRef] = out
		}
		if ev.Turn != nil && ev.Turn.ResumeID != "" {
			s.lastResumeID = ev.Turn.ResumeID
		}
		s.cond.Broadcast()
	case "error":
		if s.fatal == nil {
			s.fatal = fmt.Errorf("claude: %s", ev.Message)
		}
		s.cond.Broadcast()
	}
	fn := s.onEvent
	s.mu.Unlock()

	if fn != nil {
		fn(ge)
	}
}

// toTurnInfo 把桥的 turn 载荷转成中性 TurnInfo。
func toTurnInfo(t *bridge.SSETurn) *agent.TurnInfo {
	if t == nil {
		return nil
	}
	return &agent.TurnInfo{ResumeID: t.ResumeID, Usage: t.Usage, CostUSD: t.CostUSD}
}

// toAgentEvent 把桥事件翻译成中性 agent.Event。
func (s *session) toAgentEvent(ev bridge.SSEEvent) agent.Event {
	e := agent.Event{
		Kind:      agent.EventKind(ev.Kind),
		TurnSeq:   ev.TurnSeq,
		SessionID: s.id,
	}
	switch ev.Kind {
	case "text_delta":
		e.Text = ev.Text
	case "thinking_delta":
		e.Text = ev.Text
		e.IsThought = true
	case "tool_start":
		e.ToolCallID = ev.ToolCallID
		e.ToolTitle = ev.ToolTitle
		e.ToolKind = ev.ToolKind
		e.RawInput = ev.RawInput
	case "tool_end":
		e.ToolCallID = ev.ToolCallID
		e.ToolTitle = ev.ToolTitle
		e.ToolStatus = ev.ToolStatus
		e.RawOutput = ev.RawOutput
	case "permission_needed":
		e.Permission = agent.PermissionRequest{
			ReqID:      ev.ReqID,
			SessionID:  s.id,
			ToolCallID: ev.ToolUseID,
			ToolTitle:  ev.ToolName,
			RawInput:   ev.RawInput,
		}
	case "turn_end":
		e.Turn = toTurnInfo(ev.Turn)
		if ev.IsError || (ev.Subtype != "" && ev.Subtype != "success") {
			e.Err = fmt.Errorf("claude: turn %s", ev.Subtype)
		}
	case "error":
		e.Err = fmt.Errorf("claude: %s", ev.Message)
	case "state_changed":
		e.State = ev.State
	}
	return e
}
