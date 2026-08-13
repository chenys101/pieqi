package model

import (
	"encoding/json"
	"time"
)

// TaskStatus 任务生命周期状态。
//
// 迁移：pending -> running -> waiting_input <-> running -> completed | failed | cancelled
type TaskStatus string

const (
	TaskPending      TaskStatus = "pending"       // 已创建，worktree 未建
	TaskRunning      TaskStatus = "running"       // claude 进程活跃
	TaskWaitingInput TaskStatus = "waiting_input" // 卡在 hook 等人类决策
	TaskCompleted    TaskStatus = "completed"
	TaskFailed       TaskStatus = "failed"
	TaskCancelled    TaskStatus = "cancelled"
)

// TaskSource 任务来源。IM 渠道、HTTP/PWA、CLI/Electron 三类入口统一抽象。
type TaskSource string

const (
	SourceIM   TaskSource = "im"
	SourceHTTP TaskSource = "http"
	SourceCLI  TaskSource = "cli"
)

// DecisionKind 区分两种 waiting_input 的来源，决定恢复路径：
//   - approval：路径 A，PreToolUse hook 触发，claude 进程仍活着挂 hook channel，
//     恢复走 hooks.Resolve -> claude 继续。
//   - choice：路径 B，Claude 文本提问触发，claude 已 end_turn 退出（进程已死），
//     恢复走 Resume(--resume -p <选项>) -> 新进程。
//
// 空串兼容旧持久化任务（磁盘上无 kind 字段），兜底当 approval。
type DecisionKind string

const (
	DecisionKindApproval DecisionKind = "approval" // 路径 A：approve/deny
	DecisionKindChoice   DecisionKind = "choice"   // 路径 B：多选一
)

// Decision 任务卡在 hook 时的一次权限/决策中断。
// 路径 A（approval）：Claude 原生 permission 经 PreToolUse hook 上报，options 固定 ["approve","deny"]。
// 路径 B（choice）：Claude 输出 [CHOICE] 格式提问，options 为候选选项列表。
type Decision struct {
	ID        string       `json:"id"`                   // 关联 stream-json 的 tool_use id（路径 A）或新生成 uuid（路径 B）
	Kind      DecisionKind `json:"kind,omitempty"`       // approval | choice；空串兼容旧持久化
	ToolName  string       `json:"tool_name,omitempty"`  // 路径 A: Bash/Edit/...；路径 B: 空
	Summary   string       `json:"summary"`              // 路径 A: 工具摘要；路径 B: 问题文本
	Options   []string     `json:"options"`              // approval: ["approve","deny"]；choice: 候选项
	CreatedAt time.Time    `json:"created_at"`
}

// Intervention 用户对 waiting_input 任务的一次干预。
// kind=decision 时携带 Choice(approve/deny)；kind=append_prompt 时携带 Text（可含 /skill）。
type Intervention struct {
	ID         string     `json:"id"`
	TaskID     string     `json:"task_id"`
	Kind       string     `json:"kind"` // "decision" | "append_prompt"
	DecisionID string     `json:"decision_id,omitempty"`
	Choice     string     `json:"choice,omitempty"` // "approve" | "deny"
	Text       string     `json:"text,omitempty"`
	Source     TaskSource `json:"source"`
	CreatedAt  time.Time  `json:"created_at"`
}

// TaskEventType 执行事件的种类,供详情视图按类型渲染。
type TaskEventType string

const (
	EventText       TaskEventType = "text"        // claude 的文本输出(思考/回答)
	EventUser       TaskEventType = "user"        // 用户提交的 prompt/续问（前端渲染为右对齐气泡）
	EventThinking   TaskEventType = "thinking"    // claude 的 thinking 块(推理过程)
	EventToolUse    TaskEventType = "tool_use"    // claude 发起的工具调用
	EventToolResult TaskEventType = "tool_result" // 工具执行结果
	EventStatus     TaskEventType = "status"      // 状态变更(进入 waiting_input 等)
)

// TaskEvent 执行流中的一个事件,按时间顺序追加到 Task.Events。
// 前端详情视图按 Seq 顺序渲染,实时展示 claude code 执行过程。
type TaskEvent struct {
	Seq       int             `json:"seq"`                  // 单调递增序号,前端判断是否新增
	Type      TaskEventType   `json:"type"`
	Text      string          `json:"text,omitempty"`       // type=text 时
	ToolName  string          `json:"tool_name,omitempty"`  // tool_use/tool_result 的工具名
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`      // tool_use 的参数(原样 JSON)
	Result    string          `json:"result,omitempty"`     // tool_result 文本化内容
	IsError   bool            `json:"is_error,omitempty"`   // tool_result 是否失败
	At        time.Time       `json:"at"`
}

// Task 一次在 Git Worktree 中运行的编码任务。
type Task struct {
	ID              string     `json:"id"` // uuid
	Source          TaskSource `json:"source"`
	ProjectID       string     `json:"project_id"`
	ProjectPath     string     `json:"project_path"`     // repo root，REQ-01 分组依据
	WorktreePath    string     `json:"worktree_path"`    // worktree 建好后填
	ClaudeSessionID string     `json:"claude_session_id"` // uuid.New()，--resume 目标
	ACPSessionID    string     `json:"acp_session_id,omitempty"` // ACP 路径：真实协议 sessionId（session/load/resume 目标）。PrintAgent 回退路径仍用 ClaudeSessionID。
	Status          TaskStatus `json:"status"`
	Prompt          string     `json:"prompt"`
	Title           string     `json:"title,omitempty"` // 一句话标题（异步大模型摘要生成；缺失时前端用 prompt 智能截断兜底）
	Output          string      `json:"output,omitempty"` // 流式累积的最新文本
	Events          []TaskEvent `json:"events,omitempty"` // 执行事件流(文本/工具调用/结果),供详情视图
	CurrentDecision *Decision  `json:"current_decision,omitempty"`
	Error           string     `json:"error,omitempty"`

	// IM 来源回执：waiting_input 时通过原渠道 push 通知，让用户在手机上也能收到
	OriginChannel  string `json:"origin_channel,omitempty"`
	OriginChatID   string `json:"origin_chat_id,omitempty"`
	OriginIdentity string `json:"origin_identity,omitempty"`

	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// Project 一个代码项目。每个项目对应一个 git repo，是 worktree 与 task 分组的基准。
//
// 项目不再预注册：从 task 的 project_path 派生（deriveProjectID 取目录名），
// 仅在运行期构造，供 WorktreeManager.Create 使用。
type Project struct {
	ID         string `json:"id"`
	RepoPath   string `json:"repo_path"`    // 绝对路径，project_path 取此
	BaseBranch string `json:"base_branch"`  // 默认 "main"
}
