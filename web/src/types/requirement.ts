/** Collaboration Plane 前端类型（ADR-0021）。 */

export type RequirementStatus = 'draft' | 'active' | 'done' | 'cancelled'

export type IssueSeverity = 'blocker' | 'major' | 'minor'
export type IssueStatus = 'open' | 'fixing' | 'resolved'
export type ACState = 'pending' | 'green' | 'red' | 'deferred'

export interface AcceptanceItem {
  id: string
  text: string
  verifiable?: boolean
  deferred?: boolean
  human_result?: string
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
}

export interface RequirementOutcome {
  requirement_id: string
  status: string
  acceptance: ACOutcome[]
  open_issues?: Issue[]
  can_mark_done: boolean
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

export interface ConfirmResult {
  requirement: Requirement
  tasks: Array<{ id: string; title?: string; status: string; prompt: string }>
  /** 覆盖确认时被废弃的旧工作任务数 */
  abandoned?: number
}
