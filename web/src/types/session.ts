// Agent 会话模型（方案 §9.2）。
// 当前后端 Task 与 Session 是 1:1（一个任务一个 agent 会话），
// 前端以 taskId 作为会话容器键，为未来 Task→多 Session 演进预留。

import type { TaskStatus } from './task'

export interface AgentSession {
  /** 会话 id（当前为 taskId；未来可为真实 session id） */
  id: string
  taskId: string
  agent: string
  status: TaskStatus
  startedAt?: string
  endedAt?: string
  /** Session Fork 预留（方案 §44） */
  parentSessionId?: string
  forkPointEventId?: string
}
