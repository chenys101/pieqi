// Agent 事件模型（方案 §9.3 / §41 / §42）。
// 后端事件（user/text/thinking/tool_use/tool_result/status）经 Normalizer
// 归一为前端稳定模型；未来后端协议变化只改 normalizer，不改 UI。

import type { TaskStatus } from './task'

/** 前端稳定事件类型（UI 渲染的唯一依据） */
export type AgentEventType =
  | 'user_message' // 用户提交的 prompt / 续问（右对齐气泡）
  | 'text_delta' // Agent 文本输出（流式累积块）
  | 'thinking_delta' // Agent 思考块（默认折叠）
  | 'tool_call' // 工具调用卡片
  | 'tool_result' // 工具结果
  | 'status' // 状态变更
  | 'completed' // 会话完成
  | 'error' // 错误

export interface AgentEventPayload {
  text?: string
  toolName?: string
  toolUseId?: string
  input?: unknown
  result?: string
  isError?: boolean
}

export interface AgentEvent {
  /** 唯一键：`${taskId}:${seq}`（持久化事件）或 `${taskId}:delta-${n}`（流式增量） */
  id: string
  taskId: string
  sessionId?: string
  type: AgentEventType
  timestamp: string
  payload: AgentEventPayload
  /** Agent Graph 预留（方案 §47）：父事件关系不丢弃 */
  parentEventId?: string
}

/** 真流式增量（WS task_delta 归一化产物） */
export interface AgentDelta {
  taskId: string
  text: string
  isThought: boolean
}

/** 快照 / task_updated 的会话状态映射 */
export interface SessionStatusChange {
  taskId: string
  status: TaskStatus
}

/** Cost / Token 预留（方案 §46） */
export interface Usage {
  inputTokens?: number
  outputTokens?: number
  totalTokens?: number
  estimatedCost?: number
}
