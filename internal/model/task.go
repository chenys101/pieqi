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

// RiskLevel 审批风险分级（SPEC §5.4 组① / §6.2 待审批组）。
//
// 它**不是新造的一套并行语义**，而是给已有的「免审名单」一个可以说出口的名字：
// 底层仍然是 ACP ToolKind 白名单，分级只是把「哪些 ToolKind 归为一档」写清楚，
// 让用户不必知道 ToolKind 是什么。
//
// 定义在 model 而不是 core，是因为 Decision 要带它 —— model 是最底层，不能反向依赖 core。
type RiskLevel string

const (
	RiskL0 RiskLevel = "L0" // 只读探测
	RiskL1 RiskLevel = "L1" // 写入
	RiskL2 RiskLevel = "L2" // 执行命令
	RiskL3 RiskLevel = "L3" // 破坏性操作
)

// AutoApprovable 一档风险是否**允许**自动放行。
//
// L2 / L3 是**硬边界**：它们不是"默认关着的开关"，而是**根本不存在开关**。
// 能把自己配进坑里的选项，不要做成选项 —— 影响会溢出到工作区之外的操作
// （装依赖、跑脚本）与不可逆操作（删除、覆盖、强制推送）必须由人确认。
func (l RiskLevel) AutoApprovable() bool { return l == RiskL0 || l == RiskL1 }

// Decision 任务卡在 hook 时的一次权限/决策中断。
// 路径 A（approval）：Claude 原生 permission 经 PreToolUse hook 上报，options 固定 ["approve","deny"]。
// 路径 B（choice）：Claude 输出 [CHOICE] 格式提问，options 为候选选项列表。
type Decision struct {
	ID        string       `json:"id"`                   // 关联 stream-json 的 tool_use id（路径 A）或新生成 uuid（路径 B）
	Kind      DecisionKind `json:"kind,omitempty"`       // approval | choice；空串兼容旧持久化
	// Risk 本次决策的风险分级（L0–L3）。
	//
	// 只给**路径 A（工具审批）**打标 —— 风险是"这个操作会干什么"的属性，
	// 而路径 B（Claude 文本提问）没有工具语义，硬套一个等级只会给出假信息。
	// 空串 = 未知（旧持久化任务 / choice 类决策），前端按 L2 的视觉强度兜底：
	// 未定级不等于低风险，"不知道"必须往保守那侧倒。
	Risk      RiskLevel    `json:"risk,omitempty"`
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
	EventRewind     TaskEventType = "rewind"      // 用户回退代码（Feedback P0）：Input 载结构化载荷，Text 放人读摘要
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

// DiffStat 任务在**进入终态那一刻**对累计代码改动的快照。
//
// 为什么要在终态固化、而不是按需重算：这是**随时间丢失的数据**。
// worktree 清理后就再也取不到当时的 git diff，而这个快照要在「本周概览」
// 这类回顾性视图里长期可用。回头重算是拿不回来的，只能当场固存。
type DiffStat struct {
	Files      int       `json:"files"`
	Additions  int       `json:"additions"`
	Deletions  int       `json:"deletions"`
	CapturedAt time.Time `json:"captured_at"`
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
	// NextEventSeq 是**下一个**事件序号（单调递增，不从 0 开始计数）。
	//
	// 为什么不能继续用 len(Events)+1 推：事件保留上限会把最旧的裁掉，
	// 一旦裁过，len 就不再能推出"下一个序号"——重复的 Seq 会让所有按 seq
	// 定位的逻辑（Checkpoint 快照点、Evidence、rewindEventSeq）静默取到错的事件，
	// 而错误表现是"少了/多了几条"，不是报错。
	NextEventSeq   int         `json:"next_event_seq,omitempty"`
	CurrentDecision *Decision  `json:"current_decision,omitempty"`
	Error           string      `json:"error,omitempty"`

	// DiffStat 终态时的累计改动快照。**只有 completed 任务才有**（见 SnapshotDiffStat 的理由），
	// failed/cancelled 为 nil —— 它们的改动不是有效产出，计入会污染概览。
	DiffStat *DiffStat `json:"diff_stat,omitempty"`

	// Baseline Task 创建时记录的工作区起始状态（Feedback P0）。nil = 旧任务未捕获。
	Baseline *TaskBaseline `json:"baseline,omitempty"`

	// IM 来源回执：waiting_input 时通过原渠道 push 通知，让用户在手机上也能收到
	OriginChannel  string `json:"origin_channel,omitempty"`
	OriginChatID   string `json:"origin_chat_id,omitempty"`
	OriginIdentity string `json:"origin_identity,omitempty"`

	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// TaskBaseline Task 创建时记录的工作区起始状态（ADR-0002：只读 Git，绝不写用户分支）。
// 职责：作为「累计真实 Diff」的基准；与 Checkpoint（Rewind 恢复资产）分离。
type TaskBaseline struct {
	HeadSHA    string    `json:"head_sha"`           // git HEAD，只读参照；非 git 项目为空
	CapturedAt time.Time `json:"captured_at"`         // 捕获时间
	DirtyPaths []string  `json:"dirty_paths,omitempty"` // Task 起始与 HEAD 不一致的文件（含 untracked）
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
