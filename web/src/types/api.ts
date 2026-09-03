// 后端 DTO（wire format，snake_case 与 Go JSON tag 一一对应）。
// 仅 services 层允许接触这些类型，经 Adapter 转成前端领域模型（方案 §55）。

/** 任务状态机（后端 model.TaskStatus） */
export type TaskStatusDto =
  | 'pending'
  | 'running'
  | 'waiting_input'
  | 'completed'
  | 'failed'
  | 'cancelled'

/** 执行事件类型（后端 model.TaskEventType） */
export type TaskEventTypeDto =
  | 'text'
  | 'user'
  | 'thinking'
  | 'tool_use'
  | 'tool_result'
  | 'status'
  | 'rewind'

/** 决策类型：approval=权限审批（进程存活）；choice=多选（已废弃，兜底保留） */
export type DecisionKindDto = 'approval' | 'choice'

export interface TaskEventDto {
  seq: number
  type: TaskEventTypeDto
  text?: string
  tool_name?: string
  tool_use_id?: string
  input?: unknown
  result?: string
  is_error?: boolean
  at: string
}

export interface DecisionDto {
  id: string
  kind?: DecisionKindDto
  tool_name?: string
  summary: string
  options: string[]
  created_at: string
}

/** 后端 Task（wire format） */
export interface TaskDto {
  id: string
  source: string
  project_id: string
  project_path: string
  worktree_path: string
  claude_session_id: string
  acp_session_id?: string
  status: TaskStatusDto
  prompt: string
  title?: string
  output?: string
  events?: TaskEventDto[]
  current_decision?: DecisionDto
  error?: string
  origin_channel?: string
  origin_chat_id?: string
  origin_identity?: string
  created_at: string
  updated_at: string
  started_at?: string
  finished_at?: string
}

/** GET /api/tasks 分组响应 */
export interface TaskGroupDto {
  project_id: string
  project_path: string
  counts: Record<string, number>
  tasks: TaskDto[]
}

/** POST /api/tasks/:id/intervene 请求体 */
export interface InterveneRequestDto {
  kind: 'decision' | 'append_prompt'
  decision_id?: string
  choice?: 'approve' | 'deny'
  text?: string
}

/** WebSocket 消息（EventBus 转发 + snapshot） */
export interface WsSnapshotDto {
  type: 'snapshot'
  tasks: TaskDto[]
}

export interface WsTaskEventDto {
  type: 'task_created' | 'task_updated'
  task_id: string
  task: TaskDto
}

export interface WsTaskDeletedDto {
  type: 'task_deleted'
  task_id: string
}

export interface WsTaskDeltaDto {
  type: 'task_delta'
  task_id: string
  delta: {
    text: string
    is_thought?: boolean
  }
}

export type WsMessageDto = WsSnapshotDto | WsTaskEventDto | WsTaskDeletedDto | WsTaskDeltaDto

/** GET /api/auth/status 响应 */
export interface AuthStatusDto {
  bound: boolean
  debug: boolean
  openid?: string
  nickname?: string
  bound_at?: string
}

/** GET /api/tunnel/status 响应（外网脱敏） */
export interface TunnelStatusDto {
  active: boolean
  tunnel_url?: string
  expires_at?: string
}

/** POST /api/tunnel/start|renew 响应 */
export interface TunnelOpResultDto {
  tunnel_url: string
  lark_deep_link: string
  token: string
  expires_at: string
}

/** GET /api/larkreg/status 响应 */
export interface LarkRegStatusDto {
  registered: boolean
  app_id?: string
}

/** GET /api/larkreg/poll：202 等待中 / 200 完成 */
export interface LarkRegPollDto {
  app_id?: string
  hint?: string
  error?: string
}

/** GET/POST /api/larkreg/config */
export interface LarkRegConfigDto {
  app_id?: string
  app_secret?: string
  verify_token?: string
  encrypt_key?: string
  event_mode?: 'longconn' | 'webhook'
  secret_set?: boolean
}

/** Skill / Command 补全源 */
export interface CompletionItemDto {
  name: string
  description: string
  dir: string
}

// ---------- Feedback P0（p0-design.md §5，wire 与 Go JSON tag 一致） ----------

/** FileChange 操作类型（后端 core.FileChange.Operation） */
export type FileOperationDto = 'create' | 'modify' | 'delete' | 'rename'

/** 派生的单文件变更（Agent 声明改了什么） */
export interface FileChangeDto {
  path: string
  operation: FileOperationDto
  turn: number
  tool_use_ids?: string[]
  status: 'pending' | 'success' | 'failed'
  additions?: number
  deletions?: number
}

/** 一个 Turn 的变更统计（规则生成） */
export interface ChangeSummaryDto {
  files: number
  additions: number
  deletions: number
  creates?: number
  deletes?: number
  modifies?: number
}

/** Feedback 总览里的一个 Turn */
export interface TurnInfoDto {
  turn: number
  start_event_seq: number
  user_prompt?: string
  summary: ChangeSummaryDto
  changes?: FileChangeDto[]
}

/** Task 起始 baseline（git HEAD + dirty 快照记录） */
export interface TaskBaselineDto {
  head_sha?: string
  captured_at?: string
  dirty_paths?: string[]
}

/** Preview 生命周期状态 */
export type PreviewStateDto =
  | 'unavailable'
  | 'available'
  | 'starting'
  | 'running'
  | 'stopped'
  | 'error'

export interface FeedbackPreviewDto {
  state: PreviewStateDto
  framework?: string
  port?: number
  url?: string
}

/** GET /api/tasks/:id/feedback 响应 */
export interface FeedbackBundleDto {
  task_id: string
  baseline?: TaskBaselineDto
  turns: TurnInfoDto[]
  cumulative: ChangeSummaryDto
  checkpoints: number[]
  preview?: FeedbackPreviewDto
}

/** GET /api/tasks/:id/feedback/diff 响应 */
export interface FeedbackDiffDto {
  path: string
  turn?: number
  operation: FileOperationDto
  diff: string
  additions: number
  deletions: number
  truncated: boolean
  binary: boolean
}

/** POST /api/tasks/:id/rewind 请求 */
export interface RewindRequestDto {
  to_turn: number
  scope?: 'code'
}

/** POST /api/tasks/:id/rewind 响应 */
export interface RewindResponseDto {
  ok: boolean
  rewind_event_seq: number
  to_turn: number
  restored: string[]
  preview_stopped: boolean
}

/** GET /api/tasks/:id/preview/status 响应 */
export interface PreviewStatusDto {
  state: PreviewStateDto
  framework?: string
  port?: number
  error?: string
}
