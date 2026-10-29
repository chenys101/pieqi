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
	ID   string       `json:"id"`             // 关联 stream-json 的 tool_use id（路径 A）或新生成 uuid（路径 B）
	Kind DecisionKind `json:"kind,omitempty"` // approval | choice；空串兼容旧持久化
	// Risk 本次决策的风险分级（L0–L3）。
	//
	// 只给**路径 A（工具审批）**打标 —— 风险是"这个操作会干什么"的属性，
	// 而路径 B（Claude 文本提问）没有工具语义，硬套一个等级只会给出假信息。
	// 空串 = 未知（旧持久化任务 / choice 类决策），前端按 L2 的视觉强度兜底：
	// 未定级不等于低风险，"不知道"必须往保守那侧倒。
	Risk      RiskLevel `json:"risk,omitempty"`
	ToolName  string    `json:"tool_name,omitempty"` // 路径 A: Bash/Edit/...；路径 B: 空
	Summary   string    `json:"summary"`             // 路径 A: 工具摘要；路径 B: 问题文本
	Options   []string  `json:"options"`             // approval: ["approve","deny"]；choice: 候选项
	CreatedAt time.Time `json:"created_at"`
}

// Intervention 用户对 waiting_input 任务的一次干预。
// kind=decision 时携带 Choice(approve/approve_session/deny)；kind=append_prompt 时携带 Text（可含 /skill）。
type Intervention struct {
	ID         string     `json:"id"`
	TaskID     string     `json:"task_id"`
	Kind       string     `json:"kind"` // "decision" | "append_prompt"
	DecisionID string     `json:"decision_id,omitempty"`
	Choice     string     `json:"choice,omitempty"` // "approve" | "approve_session" | "deny"
	Text       string     `json:"text,omitempty"`
	Source     TaskSource `json:"source"`
	CreatedAt  time.Time  `json:"created_at"`
}

// TaskEventType 执行事件的种类,供详情视图按类型渲染。
type TaskEventType string

const (
	EventText        TaskEventType = "text"         // claude 的文本输出(思考/回答)
	EventUser        TaskEventType = "user"         // 用户提交的 prompt/续问（前端渲染为右对齐气泡）
	EventThinking    TaskEventType = "thinking"     // claude 的 thinking 块(推理过程)
	EventToolUse     TaskEventType = "tool_use"     // claude 发起的工具调用
	EventToolResult  TaskEventType = "tool_result"  // 工具执行结果
	EventStatus      TaskEventType = "status"       // 状态变更(进入 waiting_input 等)
	EventRewind      TaskEventType = "rewind"       // 用户回退代码（Feedback P0）：Input 载结构化载荷，Text 放人读摘要
	EventModelSwitch TaskEventType = "model_switch" // 会话内切换模型：Input 载 {from,to}，Text 放人读摘要
	EventModeSwitch  TaskEventType = "mode_switch"  // 会话内切换模式：Input 载 {from,to}，Text 放人读摘要
	EventPlan        TaskEventType = "plan"         // ACP Agent Plan：Input 载 PlanPayload（完整 entries 替换）
)

// ModelSwitchPayload 是 EventModelSwitch 的 Input 结构化载荷。
//
// From/To 都是 agent 下发的**不透明选择值**（如 ["magpie","workbuddy/glm-5.3-flash"]），
// 原样存取 —— 前端要显示人读名时拿它去比对 GET /api/agents/{agent}/models 的清单，
// **不要**在这里解析或拼串（拼出来的值 agent 认不出，见 model_catalog.go 顶部）。
//
// From 为空表示"此前用的是 agent 自己的默认路由"，不是"切到默认"。
type ModelSwitchPayload struct {
	From string `json:"from,omitempty"`
	To   string `json:"to"`
}

// ModeSwitchPayload 是 EventModeSwitch 的 Input 结构化载荷。
//
// From/To 是 ACP SessionModeId（不透明）；人读名比对 Task.Modes。
type ModeSwitchPayload struct {
	From string `json:"from,omitempty"`
	To   string `json:"to"`
}

// ModeOption 会话可选的一种运行模式（映射 ACP SessionMode）。
type ModeOption struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// PlanEntryStatus / PlanEntryPriority 与 ACP 协议同字面值。
type PlanEntryStatus string

const (
	PlanPending    PlanEntryStatus = "pending"
	PlanInProgress PlanEntryStatus = "in_progress"
	PlanCompleted  PlanEntryStatus = "completed"
)

type PlanEntryPriority string

const (
	PlanPriorityHigh   PlanEntryPriority = "high"
	PlanPriorityMedium PlanEntryPriority = "medium"
	PlanPriorityLow    PlanEntryPriority = "low"
)

// PlanEntry 执行计划中的一条（映射 ACP PlanEntry）。
type PlanEntry struct {
	Content  string            `json:"content"`
	Priority PlanEntryPriority `json:"priority"`
	Status   PlanEntryStatus   `json:"status"`
}

// PlanPayload 是 EventPlan 的 Input，也是 Task.Plan 的快照。
//
// 协议要求每次 session/update(plan) 都带**完整** entries，客户端整表替换。
type PlanPayload struct {
	Entries []PlanEntry `json:"entries"`
}

// TaskEvent 执行流中的一个事件,按时间顺序追加到 Task.Events。
// 前端详情视图按 Seq 顺序渲染,实时展示 claude code 执行过程。
type TaskEvent struct {
	Seq       int             `json:"seq"` // 单调递增序号,前端判断是否新增
	Type      TaskEventType   `json:"type"`
	Text      string          `json:"text,omitempty"`      // type=text 时
	ToolName  string          `json:"tool_name,omitempty"` // tool_use/tool_result 的工具名
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`    // tool_use 的参数(原样 JSON)
	Result    string          `json:"result,omitempty"`   // tool_result 文本化内容
	IsError   bool            `json:"is_error,omitempty"` // tool_result 是否失败
	// Images 该事件随附的图片。
	//
	// 两类来源，**前端按 Stored 决定画不画**：
	//   - user 事件：用户随消息发出的图（旧数据只有元数据，Stored=false）；
	//   - tool_result 事件：读图类工具（read_image）结果里的图，已落盘（Stored=true）。
	//
	// ⚠️ **只存元数据，绝不存 base64**：一张图动辄几百 KB~几 MB，而 Task.Events
	// 是要整体序列化进任务 JSON、并在 WS task_updated 里全量下发的。把 base64
	// 写进来会让单任务文件膨胀到几十 MB —— 这正是 TaskSummary 当初从列表里
	// 摘掉 events 的原因（见 TaskSummary 注释里那次 11.25MB 的实测）。
	// 前端要展示缩略图时按需从专用接口取。
	Images []TaskImage `json:"images,omitempty"`
	At     time.Time   `json:"at"`
}

// TaskImage 随消息发出的图片的**元数据**（不含图片本体）。
//
// 为什么留 Hash：它是"同一张图被重复发出"的判据，也是日后接内容寻址存储
// （dsh-acp 的 attachment store 就是这个形态）时回查图片的键。只存张数就没法
// 在 UI 上正确复原"这条消息带的是哪几张图"。
type TaskImage struct {
	// MimeType 图片类型（image/png 等）。
	MimeType string `json:"mime_type"`
	// Bytes 解码后的字节数（供 UI 显示大小，也便于排查"为什么这条特别大"）。
	Bytes int `json:"bytes"`
	// Hash 图片内容的 SHA-256（hex）。空 = 未计算（旧数据/降级路径）。
	Hash string `json:"hash,omitempty"`

	// Name 图片的文件名（工具结果里的图才有，如 `A.png`；用户上传的通常为空）。
	// 只用于标题/无障碍文案，不参与寻址。
	Name string `json:"name,omitempty"`

	// Stored 图片**本体**是否已落盘、可按 Hash 取回。
	//
	// ⚠️ 它解决的是一个会**静默说谎**的界面问题：旧版用户上传的图只记了 Hash/Bytes
	// 却没存字节（见 ResumeRich 的说明），前端若按"有 Image 就画 <img>"去做，
	// 那些历史事件会渲染成一堆永远加载不出来的破图 —— 看起来是"图丢了"，
	// 实际是"从来没过存过"。**只有 Stored=true 才允许渲染缩略图。**
	Stored bool `json:"stored,omitempty"`
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

// TaskSummary 列表/快照用的**轻量**任务视图：除事件流外与 Task 同构。
//
// 为什么要单独一个类型：`GET /api/tasks` 原先直接序列化 []*Task，把每个任务的
// **完整 events** 都塞进列表响应。实测（2026-10-06，24 个任务）单次列表
// **11.25 MB / 300ms+**，且随事件累积线性劣化 —— 这就是"列表越来越慢"的根因；
// WS 快照同样用它，重连时要先推 11MB，前端解析期间订阅缓冲必然溢出。
//
// 事件流是**详情页**才需要的重载荷，应走 GET /api/tasks/{id} 按需拉取。
// 列表只需要"有没有新事件"这一位信息，故以 EventCount/NextEventSeq 代替。
//
// 用内嵌 *Task 而非复制字段：字段有 25 个，复制会立刻产生漂移
// （新增字段忘了同步 = 前端静默拿到 undefined）。内嵌 + 遮蔽 Events 让
// 「加字段」自动生效，只有事件流被显式替换。
type TaskSummary struct {
	*Task
	// Events 遮蔽内嵌的同名字段（omitempty + nil 即不下发）。
	Events []TaskEvent `json:"events,omitempty"`
	// EventCount 事件总数，供列表显示"进度"而不必传全文。
	EventCount int `json:"event_count"`
}

// NewTaskSummary 由 Task 构造轻量视图。t 为 nil 时返回 nil。
// eventCount 是事件总数（调用方量好的；-1 表示"就地数"，见 NewTaskSummaries）。
//
// ⚠️ **返回的是浅拷贝**（`*t`），不是原指针。两条原因，都不许改回去：
//
//  1. **必须裁掉 Baseline**（2026-10-22 性能修复）。`Baseline.DirtyPaths` 是任务起始时
//     整个工作区的脏文件清单，实测单个任务最多 **14 212 条**，而 87 个任务的列表响应
//     里它占 **2.27 MB / 5.42 MB（42%）** —— 而前端**根本不读它**（唯一读 baseline 的
//     反馈面板走 `GET /api/tasks/:id/feedback`，直读 task.Baseline，不经过本视图）。
//     落盘的 Task.Baseline 不动：它是 diff 计算的输入（ADR-0002），只是不该下行。
//
//  2. **不能原地改 t**：传入的指针就是 store 里的那份对象（`TaskStore.List` 返回的是
//     快照，但调用方也可能直接传活体）。原地置 nil 会改到落盘数据，属于静默的数据损坏。
//
//  3. **必须裁掉 Output**（2026-10-22，同批性能修复）。Output 是最近一次流式输出的
//     **累积全文**，实测 89 个任务里它占列表响应的 **1.70 MB / 2.36 MB（72%）**，
//     单条任务 4.3KB 里它独占 3.8KB。而列表**从不渲染它**：唯一的读取点是会话页
//     （useSession / SessionTimeline 在"没有事件"时把它当兜底文本），那条路径的数据
//     来自 GET /api/tasks/:id，不经过本视图。落盘的 Task.Output 不动（详情页要用）。
//
// 用浅拷贝而不是新增遮蔽字段：字段有 25 个，逐个遮蔽必然漂移（见上方 TaskSummary 的说明）；
// 这里只改字段，拷贝后重赋值最不容易漏。
func NewTaskSummary(t *Task, eventCount int) *TaskSummary {
	if t == nil {
		return nil
	}
	if eventCount < 0 {
		eventCount = len(t.Events)
	}
	cp := *t
	// 事件流由遮蔽字段替代（序列化时不下发）；Baseline / Output 见上。
	cp.Events = nil
	cp.Baseline = nil
	cp.Output = ""
	return &TaskSummary{Task: &cp, EventCount: eventCount}
}

// NewTaskSummaries 批量构造。事件总数用调用方量的值（同序一一对应）；
// counts 为 nil 时退回就地数 len(t.Events)。
//
// 为什么要外部传入计数：快照构造函数拿不到"被裁掉的事件有多少条"，
// 而为了数它去深拷贝整个事件流，正是列表接口变慢的根因（见 core.TaskStore.ListSummaryEvents）。
func NewTaskSummaries(ts []*Task, counts []int) []*TaskSummary {
	out := make([]*TaskSummary, 0, len(ts))
	for i, t := range ts {
		n := -1
		if i < len(counts) {
			n = counts[i]
		}
		out = append(out, NewTaskSummary(t, n))
	}
	return out
}

// Task 一次在 Git Worktree 中运行的编码任务。
type Task struct {
	ID     string     `json:"id"` // uuid
	Source TaskSource `json:"source"`
	// Agent 本任务使用的 agent 名（"claude" / "qoder"），由新建任务时用户选择。
	// 空串 = 旧任务（本字段上线前创建），按默认 agent 处理 —— 语义等同于显式 "claude"。
	Agent string `json:"agent,omitempty"`
	// Model 本任务使用的模型（空 = 用 agent 自己的默认）。取值是 agent 下发的
	// **不透明选择值**（如 `["magpie","group/auto-deepseek-v4-1-flash"]`），来自
	// GET /api/agents/{name}/models —— 前端下拉选，原样存、原样经 ACP 请求 _meta 传给
	// agent，不做解析。
	//
	// 为什么不能只存展示名：同一个串就是 agent 认的选路标识，自己拼会被判为未知模型
	// （建会话直接失败）。跨 agent 无意义 —— 换 agent 就等于换一套清单，故随 agent 一起改。
	//
	// 注意这里存的是**首轮**的模型；会话续问时用户还可以在发提示词时另选一个（按轮生效，
	// 不落库，见 api.interveneReq.Model）。
	Model string `json:"model,omitempty"`
	// Mode 本任务会话当前 ACP 运行模式（SessionModeId，空 = agent 未声明 / 未协商）。
	// 来自 session/new 的 modes.currentModeId，或用户 POST /tasks/:id/mode，或
	// agent 的 current_mode_update。与 Model 一样是不透明串，原样存取。
	Mode string `json:"mode,omitempty"`
	// Modes 本会话可选模式清单（session/new 下发；空 = 该 agent 不暴露 mode UI）。
	// 随会话协商刷新，不跨 agent 复用。
	Modes []ModeOption `json:"modes,omitempty"`
	// Plan 当前 ACP Agent Plan 快照（整表替换；nil = 尚未收到 / 已清空）。
	Plan *PlanPayload `json:"plan,omitempty"`
	ProjectID       string      `json:"project_id"`
	ProjectPath     string      `json:"project_path"`             // repo root，REQ-01 分组依据
	WorktreePath    string      `json:"worktree_path"`            // worktree 建好后填
	ClaudeSessionID string      `json:"claude_session_id"`        // uuid.New()，--resume 目标
	ACPSessionID    string      `json:"acp_session_id,omitempty"` // ACP 路径：真实协议 sessionId（session/load/resume 目标）。PrintAgent 回退路径仍用 ClaudeSessionID。
	Status          TaskStatus  `json:"status"`
	Prompt          string      `json:"prompt"`
	Title           string      `json:"title,omitempty"`  // 一句话标题（异步大模型摘要生成；缺失时前端用 prompt 智能截断兜底）
	Output          string      `json:"output,omitempty"` // 流式累积的最新文本
	Events          []TaskEvent `json:"events,omitempty"` // 执行事件流(文本/工具调用/结果),供详情视图
	// NextEventSeq 是**下一个**事件序号（单调递增，不从 0 开始计数）。
	//
	// 为什么不能继续用 len(Events)+1 推：事件保留上限会把最旧的裁掉，
	// 一旦裁过，len 就不再能推出"下一个序号"——重复的 Seq 会让所有按 seq
	// 定位的逻辑（Checkpoint 快照点、Evidence、rewindEventSeq）静默取到错的事件，
	// 而错误表现是"少了/多了几条"，不是报错。
	NextEventSeq    int       `json:"next_event_seq,omitempty"`
	CurrentDecision *Decision `json:"current_decision,omitempty"`
	Error           string    `json:"error,omitempty"`

	// DiffStat 终态时的累计改动快照。**只有 completed 任务才有**（见 SnapshotDiffStat 的理由），
	// failed/cancelled 为 nil —— 它们的改动不是有效产出，计入会污染概览。
	DiffStat *DiffStat `json:"diff_stat,omitempty"`

	// Baseline Task 创建时记录的工作区起始状态（Feedback P0）。nil = 旧任务未捕获。
	Baseline *TaskBaseline `json:"baseline,omitempty"`

	// Interventions 用户干预记录（R6 / S3 §2.1 M3）。**挂在 Task 上**与任务同生命周期
	// —— 口径是"该任务是否被打断"，独立存储会引入两处数据要同步的第二来源。
	// 单条极小（kind/source/created_at），次数受人工操作频次约束。
	Interventions []Intervention `json:"interventions,omitempty"`

	// IM 来源回执：waiting_input 时通过原渠道 push 通知，让用户在手机上也能收到
	OriginChannel  string `json:"origin_channel,omitempty"`
	OriginChatID   string `json:"origin_chat_id,omitempty"`
	OriginIdentity string `json:"origin_identity,omitempty"`

	// Usage 最近一次上下文用量快照（agent 上报；nil = 该 agent 不上报，或还没报过）。
	//
	// 为什么是**覆盖式的最新值**而不是 History：它表达的是"此刻上下文有多满"，
	// 这是个瞬时量；ACP 的 usage_update 一轮内会随每条消息到达多次，攒成历史只会
	// 让 task JSON 无谓膨胀，而用户要看的就是最新的那一条。
	//
	// 只用**指针**、配 omitempty：nil 与零值必须可区分 —— "agent 不上报用量"
	// 与 "用量是 0" 在 UI 上是两种显示（隐藏 vs 显示 0%）。
	Usage *TaskUsage `json:"usage,omitempty"`

	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// TaskUsage 上下文用量快照（映射 agent.UsageInfo）。
//
// 数字全部由 agent 给（dsh 走 tokenMeter + contextWindow），pieqi 不估算：
// 估算值与 agent 实际截断行为不一致，显示出来只会误导。
type TaskUsage struct {
	// Used 当前已占用的上下文 tokens。
	Used int `json:"used"`
	// Size 上下文窗口总量（tokens）。> 0 才是有意义的快照。
	Size int `json:"size"`
	// CostUSD 会话累计成本（可选）。HasCost=false 时该字段无意义，
	// UI 不应显示 "$0.00" —— 那是"没报成本"被误读成"免费"。
	CostUSD float64 `json:"cost_usd,omitempty"`
	HasCost bool    `json:"has_cost,omitempty"`
	// At 该快照的采集时刻（供 UI 判断"这条还新鲜吗"）。
	At time.Time `json:"at"`
}

// TaskBaseline Task 创建时记录的工作区起始状态（ADR-0002：只读 Git，绝不写用户分支）。
// 职责：作为「累计真实 Diff」的基准；与 Checkpoint（Rewind 恢复资产）分离。
type TaskBaseline struct {
	HeadSHA    string    `json:"head_sha"`              // git HEAD，只读参照；非 git 项目为空
	CapturedAt time.Time `json:"captured_at"`           // 捕获时间
	DirtyPaths []string  `json:"dirty_paths,omitempty"` // Task 起始与 HEAD 不一致的文件（含 untracked）
}

// Project 一个代码项目。每个项目对应一个 git repo，是 worktree 与 task 分组的基准。
//
// 项目不再预注册：从 task 的 project_path 派生（deriveProjectID 取目录名），
// 仅在运行期构造，供 WorktreeManager.Create 使用。
type Project struct {
	ID         string `json:"id"`
	RepoPath   string `json:"repo_path"`   // 绝对路径，project_path 取此
	BaseBranch string `json:"base_branch"` // 默认 "main"
}
