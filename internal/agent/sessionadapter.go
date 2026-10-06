// sessionadapter.go 把中性 AgentSession 桥接成现有 AgentAdapter 形状，并在其上提供
// session 驱动的 AgentManager 工厂——让现有 TaskRunner.runACP 状态机 / Wire* / reaper /
// 每项目并发信号量全部复用，任务驱动切到 agent.Open("claude"|"qoder")（multi-agent 修订版
// Step 3/4 的接线层）。
//
// 语义映射（与 ACPAgent 对齐，见 acp.go 注释）：
//   - NewSession → agent.Open(OpenParams{Agent, Cwd, ResumeFrom})（懒建桥会话；qoder 懒 spawn）
//   - SendPrompt → sess.Prompt（阻塞到 turn_end；桥权限挂起期间经 RespondPermission 放行）
//   - OnContentDelta / OnPermissionRequest / OnToolCallUpdate → OnEvent 翻译
//   - Approve / Deny → sess.RespondPermission
//   - Done → 会话事件流终止（桥崩 / 关停）；正常多轮保活不关
//   - RealSessionID → 桥会话的 SDK resume id（turn_end 后才有；无则回退 sessionID）
package agent

import (
	"context"
	"errors"
	"sync"

	"go.uber.org/zap"
)

// AgentKindSession 标识 sessionBackedAdapter（agent.Open 工厂驱动的会话）。
const AgentKindSession AgentKind = "session"

// 合成权限选项（**仅**用于底层不带选项的会话，如 claude 桥）。
// claude 桥的 RespondPermission(allow, optionID) 只认 allow、无 optionId 概念，
// 故给它一个 allow_once，让 PermissionWire.Resolve 的 approve 走到 Approve、
// deny 落到 Deny（pickRejectOption 无匹配 → callAdapterDeny → RespondPermission(allow=false)）。
//
// ACP 系会话（qoder）自带真实选项，必须原样透传，**不要**用这个覆盖 ——
// 详见 onEvent 的 EventPermissionNeeded 分支注释。
var sessionPermOptions = []PermissionOption{
	{ID: "allow", Name: "允许", Kind: PermissionOptionAllowOnce},
}

// sessionBackedAdapter 把 AgentSession 桥接成 AgentAdapter。
var _ AgentAdapter = (*sessionBackedAdapter)(nil)

type sessionBackedAdapter struct {
	name   string
	open   func(ctx context.Context, p OpenParams) (AgentSession, error)
	logger *zap.Logger

	mu      sync.Mutex
	sess    AgentSession
	sid     string
	onDelta ContentDeltaFunc
	onPerm  PermissionRequestFunc
	onTool  ToolCallUpdateFunc

	done     chan struct{}
	doneOnce sync.Once
}

// newSessionBackedAdapter 创建桥接 adapter。open 为 nil 时用全局 agent.Open。
func newSessionBackedAdapter(name string, open func(context.Context, OpenParams) (AgentSession, error)) *sessionBackedAdapter {
	if open == nil {
		open = Open
	}
	return &sessionBackedAdapter{name: name, open: open, done: make(chan struct{})}
}

func (a *sessionBackedAdapter) NewSession(ctx context.Context, cfg SessionConfig) (string, error) {
	a.mu.Lock()
	if a.sess != nil {
		a.mu.Unlock()
		return "", errors.New("session adapter: NewSession already called")
	}
	a.mu.Unlock()

	sess, err := a.open(ctx, OpenParams{Agent: a.name, Cwd: cfg.Cwd, ResumeFrom: cfg.ResumeFrom, TaskID: cfg.TaskID})
	if err != nil {
		return "", err
	}
	sess.OnEvent(a.onEvent)
	a.mu.Lock()
	a.sess = sess
	a.sid = sess.ID()
	a.mu.Unlock()
	return a.sid, nil
}

// RealSessionID 返回可续问的真实句柄：桥会话取 SDK resume id（turn_end 后才有），
// 无则回退 sessionID（qoder ACP 的 sessionID 即真实协议 id）。
func (a *sessionBackedAdapter) RealSessionID(sessionID string) string {
	if id := a.ResumeID(); id != "" {
		return id
	}
	return sessionID
}

// ResumeID 暴露底层会话的续问句柄（TaskRunner.refreshResumeID 轮末回写 ACPSessionID 用）。
// 底层是桥会话时返回 SDK resume id（turn_end 后才有）；否则（qoder sessionAdapter）返回 ""。
func (a *sessionBackedAdapter) ResumeID() string {
	a.mu.Lock()
	sess := a.sess
	a.mu.Unlock()
	if r, ok := sess.(interface{ ResumeID() string }); ok {
		return r.ResumeID()
	}
	return ""
}

func (a *sessionBackedAdapter) SendPrompt(ctx context.Context, sessionID, prompt string) error {
	a.mu.Lock()
	sess := a.sess
	a.mu.Unlock()
	if sess == nil {
		return errors.New("session adapter: SendPrompt before NewSession")
	}
	return sess.Prompt(ctx, prompt)
}

func (a *sessionBackedAdapter) OnContentDelta(fn ContentDeltaFunc) {
	a.mu.Lock()
	a.onDelta = fn
	a.mu.Unlock()
}

func (a *sessionBackedAdapter) OnPermissionRequest(fn PermissionRequestFunc) {
	a.mu.Lock()
	a.onPerm = fn
	a.mu.Unlock()
}

func (a *sessionBackedAdapter) OnToolCallUpdate(fn ToolCallUpdateFunc) {
	a.mu.Lock()
	a.onTool = fn
	a.mu.Unlock()
}

func (a *sessionBackedAdapter) Approve(ctx context.Context, reqID, optionID string) error {
	a.mu.Lock()
	sess := a.sess
	a.mu.Unlock()
	if sess == nil {
		return errors.New("session adapter: Approve before NewSession")
	}
	return sess.RespondPermission(ctx, reqID, true, optionID)
}

func (a *sessionBackedAdapter) Deny(ctx context.Context, reqID string) error {
	a.mu.Lock()
	sess := a.sess
	a.mu.Unlock()
	if sess == nil {
		return errors.New("session adapter: Deny before NewSession")
	}
	return sess.RespondPermission(ctx, reqID, false, "")
}

// RespondPermission 直接透传审批（AgentSession 原生形态）。
func (a *sessionBackedAdapter) RespondPermission(ctx context.Context, reqID string, allow bool, optionID string) error {
	a.mu.Lock()
	sess := a.sess
	a.mu.Unlock()
	if sess == nil {
		return errors.New("session adapter: RespondPermission before NewSession")
	}
	return sess.RespondPermission(ctx, reqID, allow, optionID)
}

func (a *sessionBackedAdapter) Cancel(ctx context.Context, sessionID string) error {
	a.mu.Lock()
	sess := a.sess
	a.mu.Unlock()
	if sess == nil {
		return nil
	}
	return sess.Cancel(ctx)
}

// InjectToolResult 不适用：session 路径工具结果由 agent 自驱（桥内子进程自行执行），
// 与评估文档 §2.1 的取舍一致（AgentSession 不暴露 InjectToolResult）。no-op 满足接口。
func (a *sessionBackedAdapter) InjectToolResult(ctx context.Context, sessionID, toolCallID, result string, isError bool) error {
	return nil
}

func (a *sessionBackedAdapter) Close(ctx context.Context) error {
	a.mu.Lock()
	sess := a.sess
	a.sess = nil
	a.mu.Unlock()
	a.markDone()
	if sess == nil {
		return nil
	}
	return sess.Close(ctx)
}

// Done 会话终止信号：桥崩（Error 事件）/ 关停时关闭。正常多轮保活不关。
func (a *sessionBackedAdapter) Done() <-chan struct{} { return a.done }

func (a *sessionBackedAdapter) markDone() { a.doneOnce.Do(func() { close(a.done) }) }

// onEvent 把中性事件翻译成 AgentAdapter 回调。
func (a *sessionBackedAdapter) onEvent(ev Event) {
	switch ev.Kind {
	case EventTextDelta:
		a.emitDelta(ContentDelta{SessionID: a.sid, Text: ev.Text, IsThought: false})
	case EventThinkingDelta:
		a.emitDelta(ContentDelta{SessionID: a.sid, Text: ev.Text, IsThought: true})
	case EventToolStart:
		a.emitTool(ToolCallUpdateInfo{
			SessionID: a.sid, ToolCallID: ev.ToolCallID, Title: ev.ToolTitle,
			Status: "in_progress", IsNew: true, RawInput: ev.RawInput,
		})
	case EventToolEnd:
		a.emitTool(ToolCallUpdateInfo{
			SessionID: a.sid, ToolCallID: ev.ToolCallID, Title: ev.ToolTitle,
			Status: ev.ToolStatus, IsNew: false, RawOutput: ev.RawOutput,
		})
	case EventPermissionNeeded:
		p := ev.Permission
		// ⚠️ 选项必须透传底层 agent 的真实 optionId，不能无条件换成 sessionPermOptions：
		// ACP 的 selected outcome 是**按 optionId 回选**的，agent 只认自己给出的那几个 id
		// （qoder 是 proceed_always / proceed_once / cancel）。塞一个它不认识的 id（历史上的
		// 合成 "allow"），agent 会当作"没有批准"处理 —— 表现就是用户点了"批准"，
		// 工具却回一句 "The user doesn't want to proceed with this tool use"，编辑不落盘。
		//
		// 只有底层确实不带选项（claude 桥只传 allow bool，无 optionId 概念）时才合成兜底，
		// 否则 pickAllowOption 找不到 allow 选项，Resolve("approve") 会直接报错。
		opts := p.Options
		if len(opts) == 0 {
			opts = sessionPermOptions
		}
		a.emitPerm(PermissionRequest{
			ReqID: p.ReqID, SessionID: a.sid, ToolCallID: p.ToolCallID,
			ToolTitle: p.ToolTitle,
			// ToolKind 同样必须透传：PermissionWire 的免审名单是**按 ToolKind 匹配**的，
			// 丢了它等于让所有请求都退化成人工审批（本该自动放行的 edit 也要弹卡）。
			ToolKind: p.ToolKind,
			RawInput: p.RawInput,
			Options:  opts,
		})
	case EventError:
		a.markDone()
	}
}

func (a *sessionBackedAdapter) emitDelta(d ContentDelta) {
	a.mu.Lock()
	fn := a.onDelta
	a.mu.Unlock()
	if fn != nil {
		fn(d)
	}
}

func (a *sessionBackedAdapter) emitPerm(p PermissionRequest) {
	a.mu.Lock()
	fn := a.onPerm
	a.mu.Unlock()
	if fn != nil {
		fn(p)
	}
}

func (a *sessionBackedAdapter) emitTool(t ToolCallUpdateInfo) {
	a.mu.Lock()
	fn := a.onTool
	a.mu.Unlock()
	if fn != nil {
		fn(t)
	}
}

// NewAgentSessionManager 以 agent.Open(name) 为 primary 的 AgentManager（#2 默认驱动）。
//
// primary 工厂产出 sessionBackedAdapter（NewSession 时 agent.Open）；fallback=nil——
// agent.Open 内部已做 bridge→print 回退，不需要 AgentManager 层再回退。
// AgentManager 的每项目并发信号量 / reaper 空闲回收 / 会话登记 / onSessionClosed 语义全复用。
func NewAgentSessionManager(name string, cfg ManagerConfig, logger *zap.Logger) *AgentManager {
	if logger == nil {
		logger = zap.NewNop()
	}
	m := &AgentManager{
		logger:   logger,
		cfg:      cfg,
		sessions: make(map[string]*managedSession),
	}
	m.primary = func() (AgentAdapter, AgentKind, error) {
		return newSessionBackedAdapter(name, nil), AgentKindSession, nil
	}
	m.fallback = nil
	return m
}

// NewMultiAgentSessionManager 多 agent 版：任务可在 Open 时按 cfg.Agent 选 agent
// （新任务页的 agent 选择器落点），defaultAgent 为未指定/未知名时的兜底。
//
// 语义与 NewAgentSessionManager 的差异仅限「primary 工厂从哪个 agent 取」：
// 每项目并发信号量 / reaper 空闲回收 / 会话登记 / onSessionClosed 全部共享同一份 ——
// 并发上限仍然是「每项目」而不是「每项目 × 每 agent」，这是刻意的（换 agent 不该偷偷放大并发）。
//
// available 为空时退化为单 agent 管理器（defaultAgent 即唯一 agent）。
func NewMultiAgentSessionManager(available []string, defaultAgent string, cfg ManagerConfig, logger *zap.Logger) *AgentManager {
	if logger == nil {
		logger = zap.NewNop()
	}
	if defaultAgent == "" && len(available) > 0 {
		defaultAgent = available[0]
	}
	m := NewAgentSessionManager(defaultAgent, cfg, logger)
	if len(available) <= 1 {
		return m
	}
	factories := make(map[string]adapterFactory, len(available))
	for _, name := range available {
		n := name // 闭包捕获副本（循环变量在 Go 1.22 前是复用的）
		factories[n] = func() (AgentAdapter, AgentKind, error) {
			return newSessionBackedAdapter(n, nil), AgentKindSession, nil
		}
	}
	m.SetAgentFactories(factories, defaultAgent)
	return m
}
