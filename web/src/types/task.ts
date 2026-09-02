// 前端领域模型（方案 §8/§9）：与后端 wire format 解耦的唯一稳定模型。

/** 任务状态（与后端枚举值一致，但归属前端命名空间） */
export type TaskStatus =
  | 'pending'
  | 'running'
  | 'waiting_input'
  | 'completed'
  | 'failed'
  | 'cancelled'

/** 是否终态 */
export function isTerminalStatus(s: TaskStatus): boolean {
  return s === 'completed' || s === 'failed' || s === 'cancelled'
}

/** 是否可干预（running 可追加 prompt / waiting_input 可决策） */
export function isInterventionable(s: TaskStatus): boolean {
  return s === 'running' || s === 'waiting_input' || s === 'pending' || isTerminalStatus(s)
}

/**
 * 前端 Task 模型（camelCase）。
 * 与 TaskDto 的差异由 services/api 适配器抹平（方案 §55）。
 */
export interface Task {
  id: string
  /** 展示标题：LLM 生成的一句话标题，未生成时由 prompt 智能截断兜底 */
  title: string
  /** 原始 prompt 全文 */
  prompt: string
  /** 项目名（project_id，取路径最后一段） */
  project: string
  /** 项目绝对路径 */
  projectPath: string
  status: TaskStatus
  /** 执行 Agent（当前后端只有 Claude Code） */
  agent: string
  /** 会话 id（claude_session_id） */
  sessionId: string
  /** 当前待决策（waiting_input 时存在） */
  decision?: import('./approval').ApprovalRequest
  /** 流式累积的最新文本（旧任务兜底展示） */
  output?: string
  error?: string
  createdAt: string
  updatedAt: string
  startedAt?: string
  finishedAt?: string
}

/** 项目分组（侧栏 / Projects 页） */
export interface TaskGroup {
  key: string
  projectId: string
  projectPath: string
  tasks: Task[]
  counts: Record<TaskStatus, number>
}
