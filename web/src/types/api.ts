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

/** 审批风险分级（SPEC §5.4 组① / §6.2）。与后端 `model.RiskLevel` 同名同义。 */
export type RiskLevelDto = 'L0' | 'L1' | 'L2' | 'L3'

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
  /** 风险分级 L0–L3；**缺省 = 未知**（旧任务 / choice 类决策），前端按 L2 兜底 */
  risk?: RiskLevelDto
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
  /** 终态时的累计代码改动快照（见 core.SnapshotDiffStat：这是随时间丢失的数据，必须当场固存） */
  diff_stat?: DiffStatDto
}

export interface DiffStatDto {
  files: number
  additions: number
  deletions: number
  captured_at: string
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

/** 机器人角色：admin 受理管理员特权命令（隧道 / API），member 一律回「无权操作」 */
export type BotRoleDto = 'admin' | 'member'

/** 机器人绑定记录（model.Bot）。**不含 app_secret** —— 它在服务端的 per-bot 凭据文件里。 */
export interface BotDto {
  id: string
  channel: 'lark' | 'wecom' | 'wechat'
  name: string
  role: BotRoleDto
  sys_prompt?: string
  app_id?: string
  created_at: string
}

/** GET /api/bots 响应 */
export interface BotsResponseDto {
  bots: BotDto[]
  admin_bot_id?: string
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

/** 单文件的累计增删（口径：baseline → 当前） */
export interface FileStatDto {
  path: string
  additions: number
  deletions: number
}

/**
 * 累计统计：合计 + 每路径明细。
 *
 * 明细与合计**出自同一次计算**，两者必须能对上 —— 「累计变化」列表里的行数
 * 如果另取一份（比如某单个 Turn 的回填值），就会出现「列表说 +1 -4、
 * 点开后那份 diff 是 +1 -3」这种同一面板给出两个结论的情况。
 */
export interface CumulativeSummaryDto extends ChangeSummaryDto {
  entries?: FileStatDto[]
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
  cumulative: CumulativeSummaryDto
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
  /** P1：回退后自动重跑 checks + 重启 preview（Rewind → Verify） */
  verify?: boolean
}

/** POST /api/tasks/:id/rewind 响应 */
export interface RewindResponseDto {
  ok: boolean
  rewind_event_seq: number
  to_turn: number
  restored: string[]
  preview_stopped: boolean
  /** P1：verify=true 时的验证摘要 */
  verification?: RewindVerificationDto
}

/** GET /api/tasks/:id/preview/status 响应 */
export interface PreviewStatusDto {
  state: PreviewStateDto
  framework?: string
  port?: number
  error?: string
}

/** GET /api/tasks/:id/preview/attach 响应（P1：外链 + 二维码） */
export interface PreviewAttachDto {
  /** 隧道可达的外部预览 URL（含 token，勿外传） */
  url: string
  /** 二维码 PNG 端点（公开只读，可直接作 <img src>） */
  qr: string
}

// ---------- Feedback P1（p1-design.md §11，wire 与 Go JSON tag 一致） ----------

/** Check 状态机（后端 core.Check.Status） */
export type CheckStatusDto = 'pending' | 'running' | 'success' | 'failed' | 'skipped'

/** 一次可验证性检查（test/lint/build；agent 事件流复用或用户重跑） */
export interface CheckDto {
  id: string
  task_id: string
  /** Agent 自跑时归属的 Turn；重跑记录为 0 */
  turn?: number
  /** 人读命令名（如 "npm test"） */
  name: string
  /** 完整 shell 命令（sh -c 执行） */
  command: string
  /** agent = 事件流复用；rerun = 用户重跑 */
  origin: 'agent' | 'rerun'
  status: CheckStatusDto
  duration_ms?: number
  exit_code?: number
  /** 截断输出（保留尾部错误段） */
  output?: string
  started_at: string
  finished_at?: string
}

/** GET /api/tasks/:id/checks 响应 */
export interface ChecksResponseDto {
  checks: CheckDto[]
}

/** Outcome / Evidence 内嵌的 check 摘要 */
export interface CheckSummaryDto {
  id: string
  name: string
  status: CheckStatusDto
  exit_code?: number
}

/** Task 完成度（规则派生：completed | partial | failed） */
export type OutcomeStatusDto = 'completed' | 'partial' | 'failed'

/** 本 Task 发生过的回退（审计） */
export interface RewindInfoDto {
  to_turn: number
  restored: string[]
  at: string
}

/** GET /api/tasks/:id/outcome 响应（手机端主验收面） */
export interface TaskOutcomeDto {
  task_id: string
  status: OutcomeStatusDto
  changes: ChangeSummaryDto
  preview?: FeedbackPreviewDto
  checks: CheckSummaryDto[]
  /** failed checks + task.error + 末轮 is_error */
  issues: string[]
  rewinds: RewindInfoDto[]
  generated_at: string
}

/** Evidence 挂载层级 */
export type EvidenceScopeDto = 'task' | 'turn' | 'outcome'

/** GET /api/tasks/:id/evidence 响应（验证证据快照，随取随派生） */
export interface EvidenceDto {
  task_id: string
  scope: EvidenceScopeDto
  turn?: number
  preview?: FeedbackPreviewDto
  checks: CheckSummaryDto[]
  /** 末轮 is_error tool_result 数 */
  errors: number
  changes: ChangeSummaryDto
  /** 每文件一行摘要（如 "modify src/a.vue (+10 -2)"） */
  diff_brief: string[]
  /** P2：视觉证据（截图 URL，最新 N 张） */
  screenshots?: string[]
  /** P2：页面 console 摘要 */
  console?: ConsoleSummaryDto
  /** P2：页面网络失败摘要 */
  network?: NetworkSummaryDto
  created_at: string
}

// ---------- Feedback P2（p2-design.md §9，wire 与 Go JSON tag 一致） ----------

/** 一次截图记录（POST /preview/screenshots 响应 / 列表项） */
export interface ScreenshotDto {
  id: string
  task_id: string
  /** preview 实例标识（taskID:port） */
  preview_id: string
  /** PNG 端点（/api/tasks/:id/preview/screenshots/<id>.png） */
  url: string
  created_at: string
}

/** GET /api/tasks/:id/preview/screenshots 响应 */
export interface ScreenshotsResponseDto {
  screenshots: ScreenshotDto[]
}

/** preview 页面 console 事件（只采 error/warn） */
export interface ConsoleEntryDto {
  level: 'error' | 'warn'
  text: string
  at: string
}

/** GET /api/tasks/:id/preview/console 响应 */
export interface ConsoleSummaryDto {
  errors: number
  warnings: number
  entries?: ConsoleEntryDto[]
}

/** preview 页面失败的网络请求（只采 4xx/5xx/failed；status=0 = failed） */
export interface NetworkEntryDto {
  url: string
  method: string
  status: number
  at: string
}

/** GET /api/tasks/:id/preview/network 响应 */
export interface NetworkSummaryDto {
  failures: number
  entries?: NetworkEntryDto[]
}

/** POST /api/tasks/:id/push 响应（Evidence Push） */
export interface PushResponseDto {
  ok: boolean
  kind: 'outcome' | 'evidence' | 'error'
  channel: string
}

/** GET /api/tasks/:id/approvals/:decisionId/diff 响应（前瞻性 Diff） */
export interface ApprovalDiffDto {
  path: string
  operation: FileOperationDto
  diff: string
  additions: number
  deletions: number
  truncated: boolean
  binary: boolean
  prospective: true
}

/** POST /api/tasks/:id/continue 响应（Evidence → Continue） */
export interface ContinueResponseDto {
  ok: boolean
  /** 后端组装出的续问 prompt（审计/回显） */
  appended_prompt: string
  event_seq: number
}

/** Rewind → Verify 验证摘要 */
export interface RewindVerificationDto {
  restored_files: number
  checks: CheckDto[]
  preview: { state: PreviewStateDto; url?: string }
}
