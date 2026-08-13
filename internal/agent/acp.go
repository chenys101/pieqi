package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"reflect"
	"sync"
	"time"

	"pieqi/internal/config"

	"github.com/coder/acp-go-sdk"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ACPAgent 基于 acp-go-sdk 的 ACP 协议适配器（M1 内核）。
//
// spawn 一个 ACP agent 进程（Claude Code 经 npx 官方 TS 适配器；其他 agent 经各自 --acp），
// 完成 capabilities handshake（acp.ProtocolVersionNumber=1），用 JSON-RPC over stdio 管理会话生命周期。
//
// SDK 真实 API 映射（spec 描述 → acp-go-sdk v0.13.5 实现，以 SDK 为准）：
//   - NewSession        → (*acp.ClientSideConnection).NewSession(acp.NewSessionRequest{Cwd, McpServers})
//   - SendPrompt        → (*acp.ClientSideConnection).Prompt(acp.PromptRequest{SessionId, Prompt: []acp.ContentBlock{acp.TextBlock(...)}})
//     Prompt 阻塞到 agent 结束该轮（返回 acp.PromptResponse{StopReason}），
//     期间 agent 推送的 SessionUpdate 经本实现的 acp.Client.SessionUpdate 回调到达。
//   - OnContentDelta    → 由 acp.Client.SessionUpdate 触发：u.AgentMessageChunk.Content.Text（回答）/ u.AgentThoughtChunk.Content.Text（思考）
//   - OnPermissionRequest → 由 acp.Client.RequestPermission 触发（M3 启用；M1 无回调时自动放行）
//   - Approve/Deny      → 投递到 pendingPerms[reqID] channel，唤醒 RequestPermission 返回
//     （映射 acp.NewRequestPermissionOutcomeSelected / NewRequestPermissionOutcomeCancelled）
//   - InjectToolResult  → ACP 不支持（agent 自行执行工具并报 ToolCallUpdate），返回 ErrNotSupported
//   - Cancel            → (*acp.ClientSideConnection).Cancel(acp.CancelNotification{SessionId})
//   - Close             → (*acp.ClientSideConnection).CloseSession（best-effort）+ cmd.Process.Kill
//   - Done              → (*acp.ClientSideConnection).Done()（连接断开时关闭）+ cmd.Wait()，合并到单一 channel
//
// 注：spec 提到 "SessionUpdate 回调" 与 "RequestPermission 回调"——SDK 把它们建模为
// acp.Client 接口的方法（agent→client 方向的 JSON-RPC 请求/通知），由本结构实现。
type ACPAgent struct {
	cfg     config.ACPConfig
	logger  *zap.Logger
	cmdName string
	cmdArgs []string

	// 进程与连接（Start 后填充；Start 之前为 nil）
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	conn   acpConn
	done   chan struct{} // 进程退出或连接断开时关闭（Done() 返回它）

	// agentCaps 握手时 agent 声明的能力（Initialize 后填充）。NewSession 据此决定
	// 续问走 session/load（LoadSession=true）还是 session/resume。
	agentCaps acp.AgentCapabilities

	startOnce sync.Once
	startErr  error
	started   bool

	// 回调（OnXxx 注册；并发读用 mu 保护）
	cbMu       sync.RWMutex
	onDelta    ContentDeltaFunc
	onPerm     PermissionRequestFunc
	onToolCall ToolCallUpdateFunc

	// 权限请求挂起表：reqID -> chan PermissionResponse。
	// RequestPermission 注册并阻塞等；Approve/Deny 投递响应后唤醒。
	permMu       sync.Mutex
	pendingPerms map[string]chan PermissionResponse

	// closeOnce 守护 Close 的幂等；doneOnce 守护 a.done 的关闭。
	// 两者分离，避免 Close 内调 markDone 时重入同一个 Once 导致死锁。
	closeOnce sync.Once
	doneOnce  sync.Once
}

// 编译期断言：ACPAgent 同时实现 AgentAdapter 与 acp.Client。
var (
	_ AgentAdapter = (*ACPAgent)(nil)
	_ acp.Client   = (*ACPAgent)(nil)
)

// acpConn 抽象 ACP 客户端连接（*acp.ClientSideConnection 的方法子集），让 ACPAgent.NewSession
// 的 load/resume 路径可单测：生产由 *acp.ClientSideConnection 实现，测试注入 fake。
// 仅收录 ACPAgent 实际调用的方法（Initialize/NewSession/LoadSession/ResumeSession/Prompt/
// Cancel/CloseSession/Done/SetLogger）。
type acpConn interface {
	Initialize(ctx context.Context, params acp.InitializeRequest) (acp.InitializeResponse, error)
	NewSession(ctx context.Context, params acp.NewSessionRequest) (acp.NewSessionResponse, error)
	LoadSession(ctx context.Context, params acp.LoadSessionRequest) (acp.LoadSessionResponse, error)
	ResumeSession(ctx context.Context, params acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error)
	Prompt(ctx context.Context, params acp.PromptRequest) (acp.PromptResponse, error)
	Cancel(ctx context.Context, params acp.CancelNotification) error
	CloseSession(ctx context.Context, params acp.CloseSessionRequest) (acp.CloseSessionResponse, error)
	Done() <-chan struct{}
	SetLogger(l *slog.Logger)
}

// 编译期断言：*acp.ClientSideConnection 满足 acpConn（生产路径）。
var _ acpConn = (*acp.ClientSideConnection)(nil)

// NewACPAgent 创建一个未启动的 ACPAgent。
// 仅做配置解析与 spawn 命令构建；不 spawn 进程，可安全用于测试 spawn 参数等。
// 调用 Start（或首个 NewSession 触发 ensureStarted）后才 spawn 进程与握手。
func NewACPAgent(cfg config.ACPConfig, logger *zap.Logger) *ACPAgent {
	if logger == nil {
		logger = zap.NewNop()
	}
	if cfg.InitTimeout <= 0 {
		cfg.InitTimeout = 30 * time.Second
	}
	name, args := buildSpawnCommand(cfg)
	return &ACPAgent{
		cfg:          cfg,
		logger:       logger,
		cmdName:      name,
		cmdArgs:      args,
		done:         make(chan struct{}),
		pendingPerms: make(map[string]chan PermissionResponse),
	}
}

// buildSpawnCommand 由 ACPConfig 推导 spawn 命令分词。
// cfg.SpawnCommand 非空则优先用它；否则按 AgentType 取默认。
func buildSpawnCommand(cfg config.ACPConfig) (string, []string) {
	if len(cfg.SpawnCommand) > 0 {
		return cfg.SpawnCommand[0], cfg.SpawnCommand[1:]
	}
	return defaultSpawnCommand(cfg.AgentType)
}

// defaultSpawnCommand 按 agent 类型返回默认 spawn 命令分词。
//   - claude-code：经官方 TS 适配器 @agentclientprotocol/claude-agent-acp（原 @zed-industries/claude-code-acp，
//     包名已迁移至 @agentclientprotocol 命名空间），由 npx 拉起 Node.js 进程。
//   - qodercli / codex 等：原生 ACP，直接 spawn 自身 --acp。
//   - 其他：按 "<agentType> --acp" 兜底。
func defaultSpawnCommand(agentType string) (string, []string) {
	switch agentType {
	case "", "claude-code":
		return "npx", []string{"-y", "@agentclientprotocol/claude-agent-acp@latest"}
	case "qodercli":
		return "qodercli", []string{"--acp"}
	case "codex":
		return "codex", []string{"--acp"}
	default:
		return agentType, []string{"--acp"}
	}
}

// CmdName 返回 spawn 命令名（测试与诊断用）。
func (a *ACPAgent) CmdName() string { return a.cmdName }

// CmdArgs 返回 spawn 命令参数（测试与诊断用）。
func (a *ACPAgent) CmdArgs() []string {
	out := make([]string, len(a.cmdArgs))
	copy(out, a.cmdArgs)
	return out
}

// Start spawn agent 进程、建立 ClientSideConnection、完成 initialize 握手。
// 非接口方法：供 AgentManager（M4）显式启动；NewSession 也会经 ensureStarted 懒启动。
// 幂等：重复调用返回首次的结果。
func (a *ACPAgent) Start(ctx context.Context) error {
	a.startOnce.Do(func() {
		a.startErr = a.startInternal(ctx)
		if a.startErr == nil {
			a.started = true
		}
	})
	return a.startErr
}

func (a *ACPAgent) startInternal(ctx context.Context) error {
	if a.cmdName == "" {
		return fmt.Errorf("acp: empty spawn command (agent_type=%q)", a.cfg.AgentType)
	}
	cmd := exec.CommandContext(ctx, a.cmdName, a.cmdArgs...)
	cmd.Stderr = newLineCollector(a.logger, "acp agent stderr")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("acp: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("acp: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("acp: start %s: %w", a.cmdName, err)
	}
	a.cmd = cmd
	a.stdin = stdin
	a.stdout = stdout

	// 用本结构作为 acp.Client 实现：SessionUpdate/RequestPermission 等回调落到下面的方法。
	a.conn = acp.NewClientSideConnection(a, stdin, stdout)
	a.conn.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil))) // 静音 SDK 自带日志；后续可桥接 zap

	// 合并进程退出与连接断开到 a.done（对应 Phase 1 liveProc.done）。
	go a.watchExit()

	// initialize 握手（capabilities handshake），用 InitTimeout 兜底防止永久挂起。
	initCtx, cancel := context.WithTimeout(ctx, a.cfg.InitTimeout)
	defer cancel()
	initResp, err := a.conn.Initialize(initCtx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientInfo: &acp.Implementation{
			Name:    "pieqi",
			Title:   acp.Ptr("Pieqi Bridge"),
			Version: "0.1.0",
		},
		ClientCapabilities: acp.ClientCapabilities{
			Fs: acp.FileSystemCapabilities{ReadTextFile: true, WriteTextFile: true},
		},
	})
	if err != nil {
		// 握手失败：统一走 Close 清理（杀进程 + markDone + 清 pending），避免与 watchExit 双重 close done。
		a.Close(ctx)
		return fmt.Errorf("acp: initialize (protocol v%d): %w", acp.ProtocolVersionNumber, err)
	}
	// 存下 agent 声明的能力，供 NewSession 决定续问走 session/load 还是 session/resume。
	a.agentCaps = initResp.AgentCapabilities
	a.logger.Debug("acp initialized",
		zap.Any("protocol_version", initResp.ProtocolVersion),
		zap.Any("agent_info", initResp.AgentInfo))
	return nil
}

// watchExit 等进程退出或连接断开，关闭 a.done（仅一次）。
func (a *ACPAgent) watchExit() {
	waitCh := make(chan struct{})
	go func() {
		_ = a.cmd.Wait()
		close(waitCh)
	}()
	select {
	case <-waitCh:
	case <-a.conn.Done():
		// 连接先断（agent 关了 stdout）：杀进程促其退出。
		_ = a.cmd.Process.Kill()
		<-waitCh
	}
	a.markDone()
}

func (a *ACPAgent) markDone() {
	a.doneOnce.Do(func() { close(a.done) })
}

// ensureStarted 懒启动：首个 NewSession 触发 Start。
func (a *ACPAgent) ensureStarted(ctx context.Context) error {
	if a.started {
		return nil
	}
	return a.Start(ctx)
}

// --- AgentAdapter 实现 ---

// NewSession 创建 ACP 会话，返回 sessionId。
// 首次调用会懒触发 Start（spawn + initialize 握手）。
//
// cfg.ResumeFrom 非空时为续问路径：复用已有会话上下文而非新建。优先 session/load
// （agent 在 Initialize 声明 LoadSession 能力时），否则 session/resume。会话丢失
// （agent 报错）返回错误，由调用方 surface（不静默失败）。
func (a *ACPAgent) NewSession(ctx context.Context, cfg SessionConfig) (string, error) {
	// 先校验入参，避免无效请求也去 spawn 进程。
	if cfg.Cwd == "" {
		return "", errors.New("acp: NewSession requires Cwd")
	}
	if err := a.ensureStarted(ctx); err != nil {
		return "", err
	}
	sessCtx, cancel := context.WithTimeout(ctx, a.cfg.InitTimeout)
	defer cancel()

	// 续问路径：ResumeFrom 非空时复用已有会话
	if cfg.ResumeFrom != "" {
		sid := acp.SessionId(cfg.ResumeFrom)
		mcpServers := toACPMcpServers(cfg.MCP)
		// 优先 session/load（agent 声明 LoadSession 能力时），否则 session/resume。
		// 两者都要求 Cwd；load 还要求 McpServers 非 nil（已由 toACPMcpServers 保证）。
		if a.agentCaps.LoadSession {
			if _, err := a.conn.LoadSession(sessCtx, acp.LoadSessionRequest{
				Cwd:        cfg.Cwd,
				SessionId:  sid,
				McpServers: mcpServers,
			}); err != nil {
				return "", fmt.Errorf("acp: load session %s: %w", cfg.ResumeFrom, err)
			}
		} else {
			if _, err := a.conn.ResumeSession(sessCtx, acp.ResumeSessionRequest{
				Cwd:        cfg.Cwd,
				SessionId:  sid,
				McpServers: mcpServers,
			}); err != nil {
				return "", fmt.Errorf("acp: resume session %s: %w", cfg.ResumeFrom, err)
			}
		}
		return cfg.ResumeFrom, nil
	}

	// 新建会话路径（原逻辑）
	resp, err := a.conn.NewSession(sessCtx, acp.NewSessionRequest{
		Cwd:        cfg.Cwd,
		McpServers: toACPMcpServers(cfg.MCP),
	})
	if err != nil {
		return "", fmt.Errorf("acp: new session: %w", err)
	}
	return string(resp.SessionId), nil
}

// RealSessionID 返回会话的真实 session ID（用于持久化与续问）。
// ACPAgent 的 sessionID 即真实协议资源 ID，直接返回入参。
func (a *ACPAgent) RealSessionID(sessionID string) string { return sessionID }

// SendPrompt 发送一轮 prompt，阻塞到 agent 结束该轮（PromptResponse 返回）。
// 期间 agent 推送的 AgentMessageChunk/AgentThoughtChunk 经 SessionUpdate 回调到达 OnContentDelta。
func (a *ACPAgent) SendPrompt(ctx context.Context, sessionID, prompt string) error {
	if !a.started {
		return errors.New("acp: SendPrompt before Start")
	}
	if _, err := a.conn.Prompt(ctx, acp.PromptRequest{
		SessionId: acp.SessionId(sessionID),
		Prompt:    []acp.ContentBlock{acp.TextBlock(prompt)},
	}); err != nil {
		return fmt.Errorf("acp: prompt: %w", err)
	}
	return nil
}

// OnContentDelta 注册内容增量回调。
func (a *ACPAgent) OnContentDelta(fn ContentDeltaFunc) {
	a.cbMu.Lock()
	a.onDelta = fn
	a.cbMu.Unlock()
}

// OnPermissionRequest 注册权限请求回调（M3 启用；M1 不注册时自动放行）。
func (a *ACPAgent) OnPermissionRequest(fn PermissionRequestFunc) {
	a.cbMu.Lock()
	a.onPerm = fn
	a.cbMu.Unlock()
}

// OnToolCallUpdate 注册工具调用更新回调。
func (a *ACPAgent) OnToolCallUpdate(fn ToolCallUpdateFunc) {
	a.cbMu.Lock()
	a.onToolCall = fn
	a.cbMu.Unlock()
}

// Approve 批准权限请求：按 ReqID 选中指定 OptionID，唤醒等待中的 RequestPermission。
// 未找到 ReqID（已超时/取消/不存在）返回错误。
func (a *ACPAgent) Approve(ctx context.Context, reqID, optionID string) error {
	ch, ok := a.takePending(reqID)
	if !ok {
		return fmt.Errorf("acp: no pending permission for req %q", reqID)
	}
	select {
	case ch <- PermissionResponse{Selected: true, OptionID: optionID}:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

// Deny 拒绝权限请求：按 ReqID 投递拒绝响应。
// 选 reject 选项的语义由调用方在 Approve(reqID, rejectOptionID) 时体现；
// 本方法投递 Cancelled（适用于无 reject 选项或要求中止该轮的情况）。
func (a *ACPAgent) Deny(ctx context.Context, reqID string) error {
	ch, ok := a.takePending(reqID)
	if !ok {
		return fmt.Errorf("acp: no pending permission for req %q", reqID)
	}
	select {
	case ch <- PermissionResponse{Selected: false}:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

// takePending 取出并删除一个 pending channel（投递完后不再保留）。
func (a *ACPAgent) takePending(reqID string) (chan PermissionResponse, bool) {
	a.permMu.Lock()
	defer a.permMu.Unlock()
	ch, ok := a.pendingPerms[reqID]
	if ok {
		delete(a.pendingPerms, reqID)
	}
	return ch, ok
}

// InjectToolResult ACP 路径不支持：工具由 agent 自行执行并报 ToolCallUpdate。
// PrintAgent 回退路径（M4）才实现真正的 stdin 注入。
func (a *ACPAgent) InjectToolResult(ctx context.Context, sessionID, toolCallID string, result string, isError bool) error {
	return ErrNotSupported
}

// Cancel 取消正在进行的 prompt turn：发 session/cancel 通知。
func (a *ACPAgent) Cancel(ctx context.Context, sessionID string) error {
	if !a.started {
		return errors.New("acp: Cancel before Start")
	}
	if err := a.conn.Cancel(ctx, acp.CancelNotification{SessionId: acp.SessionId(sessionID)}); err != nil {
		return fmt.Errorf("acp: cancel: %w", err)
	}
	return nil
}

// Close 关闭 adapter：best-effort 关会话 + 杀进程 + 标记结束。幂等。
func (a *ACPAgent) Close(ctx context.Context) error {
	a.closeOnce.Do(func() {
		// best-effort 关会话（agent 不支持 close 能力时会报错，忽略）
		if a.conn != nil && a.started {
			closeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			_, _ = a.conn.CloseSession(closeCtx, acp.CloseSessionRequest{})
			cancel()
		}
		if a.cmd != nil && a.cmd.Process != nil {
			_ = a.cmd.Process.Kill()
		}
		// 取消所有挂起的权限请求（让 RequestPermission 不再死等）
		a.permMu.Lock()
		for id, ch := range a.pendingPerms {
			select {
			case ch <- PermissionResponse{Selected: false}:
			default:
			}
			delete(a.pendingPerms, id)
		}
		a.permMu.Unlock()
		a.markDone()
	})
	return nil
}

// Done 返回进程退出/连接断开的信号 channel（对应 Phase 1 liveProc.done）。
func (a *ACPAgent) Done() <-chan struct{} { return a.done }

// --- acp.Client 实现（agent→client 方向的 JSON-RPC 请求/通知） ---

// SessionUpdate 处理 agent 推送的会话更新（内容增量 / 工具调用 / 计划等）。
// 这是 M1 端到端出文本的核心入口：AgentMessageChunk.Content.Text → OnContentDelta 回调。
func (a *ACPAgent) SessionUpdate(ctx context.Context, params acp.SessionNotification) error {
	u := params.Update
	sid := string(params.SessionId)

	switch {
	case u.AgentMessageChunk != nil:
		// 回答正文增量
		if t := u.AgentMessageChunk.Content.Text; t != nil && t.Text != "" {
			a.cbMu.RLock()
			fn := a.onDelta
			a.cbMu.RUnlock()
			if fn != nil {
				fn(ContentDelta{SessionID: sid, Text: t.Text, IsThought: false})
			}
		}
	case u.AgentThoughtChunk != nil:
		// 思考过程增量（M2 同链路推送）
		if t := u.AgentThoughtChunk.Content.Text; t != nil && t.Text != "" {
			a.cbMu.RLock()
			fn := a.onDelta
			a.cbMu.RUnlock()
			if fn != nil {
				fn(ContentDelta{SessionID: sid, Text: t.Text, IsThought: true})
			}
		}
	case u.ToolCall != nil:
		// 新工具调用开始（M3 映射 EventToolUse）
		a.cbMu.RLock()
		fn := a.onToolCall
		a.cbMu.RUnlock()
		if fn != nil {
			fn(ToolCallUpdateInfo{
				SessionID: sid, ToolCallID: string(u.ToolCall.ToolCallId),
				Title: u.ToolCall.Title, Status: string(u.ToolCall.Status),
				Kind: string(u.ToolCall.Kind), IsNew: true,
				// 工具入参/输出（RawOutput 在开始事件里通常为空，留 nil 即可）。
				RawInput:  rawAnyToJSON(u.ToolCall.RawInput),
				RawOutput: rawAnyToJSON(u.ToolCall.RawOutput),
			})
		}
	case u.ToolCallUpdate != nil:
		// 工具调用状态变更（M3 映射 EventToolResult）
		a.cbMu.RLock()
		fn := a.onToolCall
		a.cbMu.RUnlock()
		if fn != nil {
			info := ToolCallUpdateInfo{
				SessionID: sid, ToolCallID: string(u.ToolCallUpdate.ToolCallId),
				IsNew: false,
			}
			if u.ToolCallUpdate.Title != nil {
				info.Title = *u.ToolCallUpdate.Title
			}
			if u.ToolCallUpdate.Status != nil {
				info.Status = string(*u.ToolCallUpdate.Status)
			}
			if u.ToolCallUpdate.Kind != nil {
				info.Kind = string(*u.ToolCallUpdate.Kind)
			}
			// 工具入参/输出（RawOutput 在 completed/failed 时承载结果，→ EventToolResult.Result）。
			info.RawInput = rawAnyToJSON(u.ToolCallUpdate.RawInput)
			info.RawOutput = rawAnyToJSON(u.ToolCallUpdate.RawOutput)
			fn(info)
		}
	}
	// 其他更新类型（Plan/UserMessageChunk/UsageUpdate 等）M1 暂不处理，留给后续里程碑。
	return nil
}

// RequestPermission 处理 agent 的权限请求（M3 启用；M1 无回调时自动放行）。
//
// 流程：
//  1. 由 ToolCallId 生成 ReqID（空则 uuid），注册 pending channel。
//  2. 若注册了 OnPermissionRequest 回调：通知（非阻塞）桥接层，桥接层随后调 Approve/Deny 投递响应。
//     若未注册：自动放行首个 allow 选项（M1 端到端文本测试用，等价 example 的 --yolo）。
//  3. 阻塞等响应，转换为 acp.RequestPermissionResponse 返回。
func (a *ACPAgent) RequestPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	reqID := string(params.ToolCall.ToolCallId)
	if reqID == "" {
		reqID = uuid.New().String()
	}
	opts := toPermissionOptions(params.Options)

	a.cbMu.RLock()
	onPerm := a.onPerm
	a.cbMu.RUnlock()

	// 无回调：自动放行（M1 默认行为）
	if onPerm == nil {
		outcome := autoApproveOutcome(opts)
		return acp.RequestPermissionResponse{Outcome: outcome}, nil
	}

	// 有回调：注册 pending，通知，阻塞等 Approve/Deny
	ch := make(chan PermissionResponse, 1)
	a.permMu.Lock()
	a.pendingPerms[reqID] = ch
	a.permMu.Unlock()

	title := ""
	if params.ToolCall.Title != nil {
		title = *params.ToolCall.Title
	}
	onPerm(PermissionRequest{
		ReqID:      reqID,
		SessionID:  string(params.SessionId),
		ToolCallID: string(params.ToolCall.ToolCallId),
		ToolTitle:  title,
		ToolKind:   toolKindString(params.ToolCall.Kind),
		Status:     toolCallStatusString(params.ToolCall.Status),
		RawInput:   rawAnyToJSON(params.ToolCall.RawInput),
		Options:    opts,
	})

	select {
	case resp := <-ch:
		return acp.RequestPermissionResponse{Outcome: toACPOutcome(resp, opts)}, nil
	case <-ctx.Done():
		a.takePending(reqID)
		return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, ctx.Err()
	case <-a.done:
		a.takePending(reqID)
		return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, errors.New("acp: agent exited during permission request")
	}
}

// ReadTextFile 实现 acp.Client：M1 暂不真正读文件，返回不支持。
// 后续里程碑按需实现（agent 经 fs/readTextFile 让客户端代读）。
func (a *ACPAgent) ReadTextFile(ctx context.Context, params acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, ErrNotSupported
}

// WriteTextFile 实现 acp.Client：M1 暂不真正写文件，返回不支持。
func (a *ACPAgent) WriteTextFile(ctx context.Context, params acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, ErrNotSupported
}

// CreateTerminal 实现 acp.Client：M1 不支持 terminal 系列。
func (a *ACPAgent) CreateTerminal(ctx context.Context, params acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, ErrNotSupported
}

// KillTerminal 实现 acp.Client。
func (a *ACPAgent) KillTerminal(ctx context.Context, params acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, ErrNotSupported
}

// TerminalOutput 实现 acp.Client。
func (a *ACPAgent) TerminalOutput(ctx context.Context, params acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, ErrNotSupported
}

// ReleaseTerminal 实现 acp.Client。
func (a *ACPAgent) ReleaseTerminal(ctx context.Context, params acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, ErrNotSupported
}

// WaitForTerminalExit 实现 acp.Client。
func (a *ACPAgent) WaitForTerminalExit(ctx context.Context, params acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, ErrNotSupported
}

// --- 辅助 ---

// toACPMcpServers 把我们的 MCPServer DTO 转为 acp.McpServer（仅 stdio 形态）。
// 非 stdio 传输（http/sse/acp）M1 不需要，留给后续按需扩展。
func toACPMcpServers(in []MCPServer) []acp.McpServer {
	if len(in) == 0 {
		return []acp.McpServer{}
	}
	out := make([]acp.McpServer, 0, len(in))
	for _, s := range in {
		out = append(out, acp.McpServer{Stdio: &acp.McpServerStdio{
			Name:    s.Name,
			Command: s.URL,
		}})
	}
	return out
}

// toPermissionOptions 把 acp.PermissionOption 列表转为本适配器 DTO。
func toPermissionOptions(in []acp.PermissionOption) []PermissionOption {
	out := make([]PermissionOption, 0, len(in))
	for _, o := range in {
		out = append(out, PermissionOption{ID: string(o.OptionId), Name: o.Name, Kind: string(o.Kind)})
	}
	return out
}

// autoApproveOutcome 无回调时的自动放行：选首个 allow_once，次选 allow_always，
// 再次选首个选项，都没有则 Cancelled（等价 example/claude-code 的 --yolo）。
func autoApproveOutcome(opts []PermissionOption) acp.RequestPermissionOutcome {
	pick := func(kind string) (string, bool) {
		for _, o := range opts {
			if o.Kind == kind {
				return o.ID, true
			}
		}
		return "", false
	}
	if id, ok := pick(PermissionOptionAllowOnce); ok {
		return acp.NewRequestPermissionOutcomeSelected(acp.PermissionOptionId(id))
	}
	if id, ok := pick(PermissionOptionAllowAlways); ok {
		return acp.NewRequestPermissionOutcomeSelected(acp.PermissionOptionId(id))
	}
	if len(opts) > 0 {
		return acp.NewRequestPermissionOutcomeSelected(acp.PermissionOptionId(opts[0].ID))
	}
	return acp.NewRequestPermissionOutcomeCancelled()
}

// toACPOutcome 把审批响应转为 ACP outcome。
// Selected=true → 选中该 OptionID；Selected=false → Cancelled。
func toACPOutcome(resp PermissionResponse, opts []PermissionOption) acp.RequestPermissionOutcome {
	if resp.Selected && resp.OptionID != "" {
		return acp.NewRequestPermissionOutcomeSelected(acp.PermissionOptionId(resp.OptionID))
	}
	return acp.NewRequestPermissionOutcomeCancelled()
}

// toolKindString 安全取 *acp.ToolKind 的字符串值（空指针返回空串）。
// RequestPermission 的 ToolCall 字段是 acp.ToolCallUpdate，其 Kind/Status 为指针。
func toolKindString(k *acp.ToolKind) string {
	if k == nil {
		return ""
	}
	return string(*k)
}

// toolCallStatusString 安全取 *acp.ToolCallStatus 的字符串值（空指针返回空串）。
func toolCallStatusString(s *acp.ToolCallStatus) string {
	if s == nil {
		return ""
	}
	return string(*s)
}

// rawAnyToJSON 把 any 序列化为 json.RawMessage，供 EventToolUse.Input / 审批 RawInput 等使用。
//   - nil 或 typed nil（如装在 any 里的 *T(nil)）返回 nil，避免输出 "null"。
//   - string 视作已序列化的 JSON，直接转，避免 json.Marshal 二次编码加引号。
//   - 其他类型走 json.Marshal，失败返回 nil。
func rawAnyToJSON(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	// 排除 typed nil（指针/map/slice/chan/func/interface 装在 any 里为 nil 的情形）。
	if rv := reflect.ValueOf(v); rv.IsValid() {
		switch rv.Kind() {
		case reflect.Ptr, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func, reflect.Interface:
			if rv.IsNil() {
				return nil
			}
		}
	}
	if s, ok := v.(string); ok {
		return json.RawMessage(s)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

// newLineCollector 构造一个把每行输出写到 logger（warn 级）的 io.Writer，
// 用作 agent 子进程的 stderr 收集器，避免 stderr 输出丢失又不会刷屏。
func newLineCollector(logger *zap.Logger, label string) io.Writer {
	return &lineCollector{logger: logger, label: label}
}

type lineCollector struct {
	logger *zap.Logger
	label  string
	mu     sync.Mutex
	buf    []byte
}

func (lc *lineCollector) Write(p []byte) (int, error) {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	lc.buf = append(lc.buf, p...)
	for {
		idx := -1
		for i, b := range lc.buf {
			if b == '\n' {
				idx = i
				break
			}
		}
		if idx < 0 {
			break
		}
		line := string(lc.buf[:idx])
		lc.buf = lc.buf[idx+1:]
		if line != "" {
			lc.logger.Warn(lc.label, zap.String("line", line))
		}
	}
	return len(p), nil
}
