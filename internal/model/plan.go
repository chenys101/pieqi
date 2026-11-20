package model

import "time"

// Plan Engineering（ADR-0023）。V1 挂 Requirement payload。

const (
	AssertDecision     = "decision"
	AssertSystemFact   = "system_fact"
	AssertAssumption   = "assumption"
	AssertOpenQuestion = "open_question"
)

const (
	PlanStageIntent      = "intent"
	PlanStageBusiness    = "business"
	PlanStageSystem      = "system"
	PlanStageSolution    = "solution"
	PlanStageEngineering = "engineering"
	PlanStageReview      = "review"
)

type PlanAssertion struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Text          string `json:"text"`
	EvidenceRef   string `json:"evidence_ref,omitempty"`
	AcceptedRisk  bool   `json:"accepted_risk,omitempty"`
	AcceptedBy    string `json:"accepted_by,omitempty"`
	BlocksPublish bool   `json:"blocks_publish"`
}

type NodeRevision struct {
	NodeID         string     `json:"node_id"`
	Title          string     `json:"title"`
	Role           TaskRole   `json:"role,omitempty"`
	Prompt         string     `json:"prompt,omitempty"`
	CoversAC       []string   `json:"covers_ac,omitempty"`
	DependsOnNodes []string   `json:"depends_on_nodes,omitempty"`
	Agent          string     `json:"agent,omitempty"`
	Model          string     `json:"model,omitempty"`
	Verify         VerifyMode `json:"verify,omitempty"`
}

type PlanDraft struct {
	RequirementID string          `json:"requirement_id"`
	Stage         string          `json:"stage"`
	Assertions    []PlanAssertion `json:"assertions,omitempty"`
	Nodes         []NodeRevision  `json:"nodes,omitempty"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type PlanRevision struct {
	ID            string          `json:"id"`
	RequirementID string          `json:"requirement_id"`
	Revision      int             `json:"revision"`
	Nodes         []NodeRevision  `json:"nodes"`
	Assertions    []PlanAssertion `json:"assertions,omitempty"`
	PublishedAt   time.Time       `json:"published_at"`
	PublishedBy   string          `json:"published_by,omitempty"`
	Immutable     bool            `json:"immutable"`
}

type ExecutionPin struct {
	RequirementID  string    `json:"requirement_id"`
	PlanRevisionID string    `json:"plan_revision_id"`
	PinnedAt       time.Time `json:"pinned_at"`
}

type PlanChange struct {
	ID            string    `json:"id"`
	RequirementID string    `json:"requirement_id"`
	Reason        string    `json:"reason"`
	CreatedAt     time.Time `json:"created_at"`
	CreatedBy     string    `json:"created_by,omitempty"`
}
