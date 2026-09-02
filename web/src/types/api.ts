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
export type TaskEventTypeDto = 'text' | 'user' | 'thinking' | 'tool_use' | 'tool_result' | 'status'

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
