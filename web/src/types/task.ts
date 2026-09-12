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
  /**
   * 用户干预记录（R6）。**适配层已归一为数组**（永远不是 undefined/null）——
   * "一次通过"的判定就是 `interventions.length === 0`，不允许各消费方自行判空。
   */
  interventions: TaskIntervention[]
  /**
   * 终态时的累计代码改动快照。
   *
   * **只有 completed 任务才有**（后端刻意不给 failed/cancelled 做快照 ——
   * 它们的改动不是有效产出，计入会把概览变成一根被失败任务撑起来的柱子）。
   * 老任务没有这个字段（在本 feature 上线前就已完成），渲染时要能容忍缺失。
   */
  diffStat?: DiffStat
}

/** 终态时的累计代码改动 */
export interface DiffStat {
  files: number
  additions: number
  deletions: number
  capturedAt: string
}

/** 用户对任务的一次干预（R6，来自后端 model.Intervention） */
export interface TaskIntervention {
  id: string
  taskId: string
  /** "decision"（审批） | "append_prompt"（续问） */
  kind: string
  /** "approve" | "deny"（仅 decision） */
  choice?: string
  text?: string
  /** 来源渠道（im/http/cli） */
  source: string
  createdAt: string
}

/** 项目分组（侧栏 / Projects 页） */
export interface TaskGroup {
  key: string
  projectId: string
  projectPath: string
  tasks: Task[]
  counts: Record<TaskStatus, number>
}
