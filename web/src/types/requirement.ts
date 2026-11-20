/** Collaboration Plane 前端类型（ADR-0021）。 */

export type RequirementStatus = 'draft' | 'active' | 'done' | 'cancelled'

export type IssueSeverity = 'blocker' | 'major' | 'minor'
export type IssueStatus = 'open' | 'fixing' | 'resolved'
export type ACState = 'pending' | 'green' | 'red' | 'deferred'

export type ExecutionPath = 's0' | 's1' | 's2'

export interface AcceptanceItem {
  id: string
  text: string
  verifiable?: boolean
  deferred?: boolean
  human_result?: string
  evidence_ref?: string
}

export interface Issue {
  id: string
  title: string
  detail?: string
  severity: IssueSeverity
  covers_ac?: string[]
  source_task_id?: string
  status: IssueStatus
  fix_task_id?: string
  reverify_task_id?: string
  created_at: string
  updated_at: string
}

export interface Requirement {
  id: string
  title: string
  body?: string
  project_id?: string
  project_path: string
  /** confirm 后创建的集成分支，如 req/<id8> */
  integration_branch?: string
  /** s0 | s1 | s2 */
  path?: ExecutionPath
  workflow_id?: string
  workflow_version?: string
  status: RequirementStatus
  acceptance?: AcceptanceItem[]
  issues?: Issue[]
  graph_version: number
  created_at: string
  updated_at: string
}

export interface ACOutcome {
  id: string
  text: string
  state: ACState
  open_issue_ids?: string[]
  covering_tasks?: string[]
  has_evidence?: boolean
}

export interface RequirementOutcome {
  requirement_id: string
  status: string
  acceptance: ACOutcome[]
  open_issues?: Issue[]
  can_mark_done: boolean
  missing_evidence?: string[]
}

export interface ProposedTaskNode {
  temp_id: string
  title?: string
  prompt: string
  /** design | frontend | backend | test | review | fix | other */
  role?: string
  skills?: string[]
  /** planned 可空；启动前必填 */
  agent?: string
  /** 空 = agent 默认模型 */
  model?: string
  covers_ac?: string[]
  depends_on?: string[]
}

export type SuggestOutcomeKind = 'none' | 'proposal' | 'blocked'

export interface SplitBlock {
  id: string
  requirement_id: string
  summary: string
  reasons?: string[]
  questions?: string[]
  missing?: string[]
  created_at: string
}

export interface SplitProposal {
  id: string
  requirement_id: string
  nodes: ProposedTaskNode[]
  created_at: string
}

export interface SuggestOutcome {
  kind: SuggestOutcomeKind
  proposal?: SplitProposal
  block?: SplitBlock
}

export interface ConfirmResult {
  requirement: Requirement
  tasks: Array<{ id: string; title?: string; status: string; prompt: string }>
  /** 覆盖确认时被废弃的旧工作任务数 */
  abandoned?: number
}

/** Plan Engineering（ADR-0023） */
export type PlanAssertionKind = 'decision' | 'system_fact' | 'assumption' | 'open_question'

export interface PlanAssertion {
  id: string
  kind: PlanAssertionKind | string
  text: string
  evidence_ref?: string
  accepted_risk?: boolean
  accepted_by?: string
  blocks_publish: boolean
}

export interface PlanNodeRevision {
  node_id: string
  title: string
  role?: string
  prompt?: string
  covers_ac?: string[]
  depends_on_nodes?: string[]
  agent?: string
  model?: string
}

export interface PlanDraft {
  requirement_id?: string
  stage: string
  assertions?: PlanAssertion[]
  nodes?: PlanNodeRevision[]
  updated_at?: string
}

export interface PlanRevision {
  id: string
  requirement_id: string
  revision: number
  nodes: PlanNodeRevision[]
  assertions?: PlanAssertion[]
  published_at: string
  published_by?: string
  immutable: boolean
}

export interface ExecutionPin {
  requirement_id: string
  plan_revision_id: string
  pinned_at: string
}

export interface PlanView {
  draft: PlanDraft
  execution_pin?: ExecutionPin | null
  revisions?: PlanRevision[]
  publish_blocked?: string[]
}

/** Plan Engineering（ADR-0023） */
export type PlanAssertionKind = 'decision' | 'system_fact' | 'assumption' | 'open_question'

export interface PlanAssertion {
  id: string
  kind: PlanAssertionKind | string
  text: string
  evidence_ref?: string
  accepted_risk?: boolean
  accepted_by?: string
  blocks_publish: boolean
}

export interface PlanNodeRevision {
  node_id: string
  title: string
  role?: string
  prompt?: string
  covers_ac?: string[]
  depends_on_nodes?: string[]
  agent?: string
  model?: string
}

export interface PlanDraft {
  requirement_id?: string
  stage: string
  assertions?: PlanAssertion[]
  nodes?: PlanNodeRevision[]
  updated_at?: string
}

export interface PlanRevision {
  id: string
  requirement_id: string
  revision: number
  nodes: PlanNodeRevision[]
  assertions?: PlanAssertion[]
  published_at: string
  published_by?: string
  immutable: boolean
}

export interface ExecutionPin {
  requirement_id: string
  plan_revision_id: string
  pinned_at: string
}

export interface PlanView {
  draft: PlanDraft
  execution_pin?: ExecutionPin | null
  revisions?: PlanRevision[]
  publish_blocked?: string[]
}
