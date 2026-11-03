package model

import "time"

// RequirementStatus 需求生命周期（ADR-0021 长路径）。
type RequirementStatus string

const (
	RequirementDraft    RequirementStatus = "draft"
	RequirementActive   RequirementStatus = "active" // 已有确认过的图，执行中
	RequirementDone     RequirementStatus = "done"
	RequirementCancelled RequirementStatus = "cancelled"
)

// TaskRole 需求图上的任务角色提示（选 agent / skills；非独立聚合）。
type TaskRole string

const (
	RoleDesign   TaskRole = "design"
	RoleFrontend TaskRole = "frontend"
	RoleBackend  TaskRole = "backend"
	RoleTest     TaskRole = "test"
	RoleReview   TaskRole = "review"
	RoleFix      TaskRole = "fix"
	RoleOther    TaskRole = "other"
)

// VerifyMode 该 Task 对应 AC 如何验收。
type VerifyMode string

const (
	VerifyAuto   VerifyMode = "auto"
	VerifyHuman  VerifyMode = "human"
	VerifyEither VerifyMode = "either"
)

// Requirement 协作平面的需求容器（文件夹 + 验收总账）。ADR-0021。
type Requirement struct {
	ID           string            `json:"id"`
	Title        string            `json:"title"`
	Body         string            `json:"body,omitempty"`
	ProjectID    string            `json:"project_id,omitempty"`
	ProjectPath  string            `json:"project_path"` // 显式 cwd；禁止猜仓库
	Status       RequirementStatus `json:"status"`
	Acceptance   []AcceptanceItem  `json:"acceptance,omitempty"`
	Issues       []Issue           `json:"issues,omitempty"`
	Events       []RequirementEvent `json:"events,omitempty"`
	GraphVersion int               `json:"graph_version"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

// AcceptanceItem 需求上的一条验收标准。
type AcceptanceItem struct {
	ID         string `json:"id"`
	Text       string `json:"text"`
	Verifiable bool   `json:"verifiable"`
	Deferred   bool   `json:"deferred,omitempty"`
}

// IssueSeverity 缺陷严重度；回流分流依据（ADR-0021）。
type IssueSeverity string

const (
	SeverityBlocker IssueSeverity = "blocker"
	SeverityMajor   IssueSeverity = "major"
	SeverityMinor   IssueSeverity = "minor"
)

// IssueStatus 问题单状态。
type IssueStatus string

const (
	IssueOpen     IssueStatus = "open"
	IssueFixing   IssueStatus = "fixing"
	IssueResolved IssueStatus = "resolved"
)

// Issue 结构化缺陷；验证失败 → 再开发只经 Issue。
type Issue struct {
	ID           string        `json:"id"`
	Title        string        `json:"title"`
	Detail       string        `json:"detail,omitempty"`
	Severity     IssueSeverity `json:"severity"`
	CoversAC     []string      `json:"covers_ac,omitempty"`
	SourceTaskID string        `json:"source_task_id,omitempty"`
	SourceTurn   int           `json:"source_turn,omitempty"`
	CheckID      string        `json:"check_id,omitempty"`
	Status       IssueStatus   `json:"status"`
	FixTaskID    string        `json:"fix_task_id,omitempty"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
}

// RequirementEventType 需求级审计事件。
type RequirementEventType string

const (
	ReqEventCreated  RequirementEventType = "created"
	ReqEventUpdated  RequirementEventType = "updated"
	ReqEventConfirm  RequirementEventType = "confirm"
	ReqEventExpand   RequirementEventType = "expand"
	ReqEventPromote  RequirementEventType = "promote"
	ReqEventIssue    RequirementEventType = "issue"
	ReqEventStatus   RequirementEventType = "status"
)

// RequirementEvent append-only 需求事件（审计 / graph 快照）。
type RequirementEvent struct {
	Seq       int                  `json:"seq"`
	Type      RequirementEventType `json:"type"`
	Text      string               `json:"text,omitempty"`
	Payload   string               `json:"payload,omitempty"` // 结构化快照 JSON
	At        time.Time            `json:"at"`
}

// SplitProposal 拆分/展开的短暂结构化草案（ADR-0021）。
// confirm 前不进 Task 表；拒则丢弃；通过则创建 planned Task + 依赖边。
type SplitProposal struct {
	ID            string              `json:"id"`
	RequirementID string              `json:"requirement_id"`
	Nodes         []ProposedTaskNode  `json:"nodes"`
	CreatedAt     time.Time           `json:"created_at"`
}

// ProposedTaskNode 提案里的一格；DependsOn 引用同提案内其它节点的 TempID。
type ProposedTaskNode struct {
	TempID    string     `json:"temp_id"` // 提案内稳定 id，confirm 时映射成真实 Task.ID
	Title     string     `json:"title,omitempty"`
	Prompt    string     `json:"prompt"`
	Role      TaskRole   `json:"role,omitempty"`
	Skills    []string   `json:"skills,omitempty"`
	Agent     string     `json:"agent,omitempty"`
	CoversAC  []string   `json:"covers_ac,omitempty"`
	Verify    VerifyMode `json:"verify,omitempty"`
	DependsOn []string   `json:"depends_on,omitempty"` // 同提案 TempID
}
