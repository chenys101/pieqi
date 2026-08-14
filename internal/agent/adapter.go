// Package agent 定义统一的 AgentAdapter 抽象与 ACP/Print 双实现。
//
// Phase 2（ACP 迁移）引入：把 Phase 1 TaskRunner 里绑死 claude -p 的 agent 驱动部分
// 抽象成接口，让桥接层（AgentManager，M4）无感切换 ACP / claude -p 回退路径。
//
// 本文件（M1）只定义接口与 DTO；ACPAgent 实现在 acp.go，PrintAgent/AgentManager 留给 M4。
package agent

import (
	"context"
	"encoding/json"
	"errors"
)

// ErrNotSupported 操作在当前 adapter 实现下不支持。
// 例：ACPAgent.InjectToolResult 返回它——ACP 路径下工具由 agent 自行执行并报 ToolCallUpdate，
// 客户端不注入 tool_result（仅 PrintAgent 回退路径需要写 stdin）。
var ErrNotSupported = errors.New("operation not supported by this agent adapter")

// 权限选项种类常量（对齐 acp.PermissionOptionKind）。
// ACP 协议用小写下划线串：allow_once / allow_always / reject_once / reject_always。
const (
	PermissionOptionAllowOnce    = "allow_once"
	PermissionOptionAllowAlways  = "allow_always"
	PermissionOptionRejectOnce   = "reject_once"
	PermissionOptionRejectAlways = "reject_always"
)

// MCPServer 一个 MCP（Model Context Protocol）服务器配置。
// ACPAgent.NewSession 内部转换为 acp.McpServer（Stdio/HTTP/SSE 传输），
// 这里只暴露最常用的 stdio 形态字段，避免接口层泄漏 SDK 类型。
type MCPServer struct {
	Name string `json:"name"` // server 标识
	URL  string `json:"url"`  // stdio 时为启动命令；http/sse 时为端点 URL
}

// SessionConfig NewSession 的参数。
type SessionConfig struct {
	Cwd string      // 工作目录（必须绝对路径，对应 acp.NewSessionRequest.Cwd）
	MCP []MCPServer // 可选 MCP servers
	// ResumeFrom 非空时表示续问：复用已有会话上下文而非创建新会话。
	//   ACPAgent：优先 session/load（agent 在 Initialize 声明 LoadSession 能力时），
	//             否则 session/resume。会话丢失（agent 报错）返回错误，由调用方 surface。
	//   PrintAgent：预填 printSession.realSessionID=ResumeFrom 且 ran=true，
	//               首次 SendPrompt 走 --resume <ResumeFrom>。若 claude 报 "No conversation found"，
	//               SendPrompt 返回 ErrNoConversation（调用方可据此回退为新会话重跑）。
	ResumeFrom string
}

// PermissionOption 一个权限选项（映射 acp.PermissionOption）。
type PermissionOption struct {
	ID   string // ACP optionId（Approve 时按它选中）
	Name string // 人类可读名
	Kind string // PermissionOptionAllowOnce / AllowAlways / RejectOnce / RejectAlways
}

// PermissionRequest 权限请求 DTO（映射 acp.RequestPermissionRequest）。
// ReqID 为本适配器生成的请求标识，调用方用它定位 Approve/Deny。
// 通常取自 ToolCallID；为空时 ACPAgent 生成 uuid。
type PermissionRequest struct {
	ReqID      string          // 适配器生成的请求 ID（基于 ToolCallID 或 uuid）
	SessionID  string          // ACP sessionId
	ToolCallID string          // ACP toolCallId
	ToolTitle  string          // 人类可读标题
	ToolKind   string          // 工具类别（可选）
	Status     string          // 工具当前状态（可选）
	RawInput   json.RawMessage // 工具原始入参（可选）
	Options    []PermissionOption
}

// PermissionResponse 审批响应。
// 对齐 ACP RequestPermissionOutcome 的两种变体：
//   - Selected=true  → outcome=selected，OptionID 为选中的 optionId
//   - Selected=false → outcome=cancelled（取消该轮 prompt turn 的所有待审批）
type PermissionResponse struct {
	Selected bool   // true=选中某 Option（Selected），false=取消（Cancelled）
	OptionID string // Selected=true 时的 optionId
}

// ContentDelta 内容增量 DTO。
// 承载 ACP SessionUpdate 的 AgentMessageChunk（IsThought=false）/ AgentThoughtChunk（IsThought=true）文本。
// M2 落地：增量 → EventBus → WS → 前端逐字追加，替换 Phase 1 整块 EventText。
type ContentDelta struct {
	SessionID string
	Text      string // 增量文本
	IsThought bool   // true=思考过程，false=回答正文
}

// ToolCallUpdateInfo 工具调用更新 DTO。
// 承载 ACP SessionUpdate 的 ToolCall（新建，IsNew=true）/ ToolCallUpdate（状态变更，IsNew=false）。
// M3 落地：映射为 Phase 1 的 EventToolUse / EventToolResult。
type ToolCallUpdateInfo struct {
	SessionID  string
	ToolCallID string
	Title      string
	Status     string // pending / in_progress / completed / failed
	Kind       string
	IsNew      bool // true=ToolCall（新开始），false=ToolCallUpdate（状态变更）

	// RawInput 工具入参（取自 ACP RawInput）。
	// 映射到 EventToolUse.Input（原样 JSON）；为空时 nil。
	RawInput json.RawMessage
	// RawOutput 工具输出（取自 ACP RawOutput）。
	// 映射到 EventToolResult.Result（转 string）；为空时 nil。
	RawOutput json.RawMessage
}

// ContentDeltaFunc 内容增量回调（M2 落地：WS 逐字）。
type ContentDeltaFunc func(delta ContentDelta)

// PermissionRequestFunc 权限请求回调（M3 落地：IM/PWA 审批卡片）。
//
// 仅作"通知"用：实现应把 req 推送给 IM/PWA 后立即返回，不要在此阻塞等待用户决策——
// 用户决策经桥接层调用 Approve/Deny 投递，适配器内部阻塞在 pending channel 上等响应。
// 未注册回调时 ACPAgent 默认自动放行首个 allow 选项（M1 端到端文本测试用）。
type PermissionRequestFunc func(req PermissionRequest)

// ToolCallUpdateFunc 工具调用更新回调（M3 落地：EventToolUse/EventToolResult）。
type ToolCallUpdateFunc func(update ToolCallUpdateInfo)

// AgentAdapter 统一 agent 抽象。
//
// ACPAgent（acp.go，M1）与 PrintAgent（print.go，M4）双实现接口对齐。
// 桥接层（AgentManager，M4）只依赖本接口，底层 ACP / claude -p 切换对调用方无感。
//
// 方法语义对齐 spec：NewSession / SendPrompt / OnContentDelta / OnPermissionRequest /
// Approve / Deny / InjectToolResult / Cancel；额外加 Close / Done 承载进程生命周期
// （对应 Phase 1 TaskRunner.liveProc 的 done channel / cancel 语义）。
type AgentAdapter interface {
	// NewSession 创建会话，返回 sessionID（ACP sessionId / PrintAgent 的 claude session id）。
	NewSession(ctx context.Context, cfg SessionConfig) (sessionID string, err error)

	// RealSessionID 返回会话的真实 session ID（用于持久化与续问）。
	//   ACPAgent：与 sessionID 相同（ACP sessionId 即真实协议资源 ID）。
	//   PrintAgent：返回 system init 报告的真实 claude session ID（首次 SendPrompt 后才有；
	//               之前返回 sessionID 本身）。NewSession 时若 ResumeFrom 非空，返回 ResumeFrom。
	RealSessionID(sessionID string) string

	// SendPrompt 发送一轮 prompt，阻塞到该轮结束
	// （ACP 等 PromptResponse；PrintAgent 等 claude 进程退出）。
	SendPrompt(ctx context.Context, sessionID, prompt string) error

	// OnContentDelta 注册内容增量回调（真流式；M2 落地 WS 逐字）。
	OnContentDelta(fn ContentDeltaFunc)

	// OnPermissionRequest 注册权限请求回调（M3 落地 IM/PWA 审批卡片）。
	// 未注册时 ACPAgent 默认自动放行首个 allow 选项（M1 端到端文本测试用）。
	OnPermissionRequest(fn PermissionRequestFunc)

	// OnToolCallUpdate 注册工具调用更新回调（M3 落地 EventToolUse/EventToolResult）。
	OnToolCallUpdate(fn ToolCallUpdateFunc)

	// Approve 批准权限请求（按 ReqID + 选中 OptionID）。
	// 唤醒对应的 OnPermissionRequest 回调返回 Selected。
	Approve(ctx context.Context, reqID, optionID string) error

	// Deny 拒绝权限请求（按 ReqID）。
	// 映射为选中 reject 选项；若无 reject 选项则返回 Cancelled。
	Deny(ctx context.Context, reqID string) error

	// InjectToolResult 注入工具结果。
	// PrintAgent 路径：写 stream-json user 消息到 claude stdin（Phase 1 buildStreamUserMessage 语义）。
	// ACP 路径：不支持（agent 自行执行工具并报 ToolCallUpdate），返回 ErrNotSupported。
	InjectToolResult(ctx context.Context, sessionID, toolCallID string, result string, isError bool) error

	// Cancel 取消正在进行的 prompt turn。
	// ACP：发 session/cancel 通知；PrintAgent：杀 claude 进程。
	Cancel(ctx context.Context, sessionID string) error

	// Close 关闭 adapter：关闭会话、杀进程、清理资源。幂等。
	Close(ctx context.Context) error

	// Done 返回一个 channel，在 agent 进程退出 / 连接断开时关闭
	// （对应 Phase 1 TaskRunner.liveProc.done）。
	Done() <-chan struct{}
}
