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
  | 'rewind' // 用户回退代码（Feedback P0）
  | 'model_switch' // 会话内切换模型（留痕，后端 model_switch.go）
  | 'mode_switch' // 会话内切换 ACP 模式（留痕）
  | 'plan' // ACP Agent Plan 清单
  | 'completed' // 会话完成
  | 'error' // 错误

/** rewind 事件的结构化载荷（后端 input 字段归一化产物） */
export interface RewindPayload {
  toTurn: number
  restored: string[]
  previewStopped: boolean
}

/**
 * model_switch 事件的结构化载荷（后端 input 字段归一化产物）。
 *
 * from/to 都是 agent 下发的**不透明选择值**，前端只用于比对清单换人读名，
 * **不得解析或拼接**（后端 model_catalog.go 有同样的纪律与原因）。
 */
export interface ModelSwitchPayload {
  /** 此前用的模型；空串 = 此前用 agent 默认路由 */
  from: string
  /** 切换到的模型；空串 = 交还 agent 默认路由 */
  to: string
}

/** mode_switch 事件载荷（后端 ModeSwitchPayload） */
export interface ModeSwitchPayload {
  from: string
  to: string
}

/** plan 事件条目（后端 PlanEntry） */
export interface PlanEntryPayload {
  content: string
  priority: string
  status: string
}

export interface PlanPayload {
  entries: PlanEntryPayload[]
}

/**
 * 可直接渲染的图片引用（后端 model.TaskImage 的 `stored=true` 子集）。
 *
 * 刻意**不带** base64 或 URL：URL 由组件按 taskId + hash 拼（见 services/api/attachments），
 * 这样同一张图在两个事件里引用时天然是同一个地址，浏览器缓存能命中。
 */
export interface AgentEventImage {
  mimeType: string
  bytes: number
  /** sha256 hex：拼取图 URL 用 */
  hash: string
  /** 文件名（工具结果里的图才有），用作标题与无障碍文案 */
  name?: string
}

export interface AgentEventPayload {
  text?: string
  toolName?: string
  toolUseId?: string
  input?: unknown
  result?: string
  isError?: boolean
  /**
   * 随事件附带的图片（已落盘、可渲染的那些）。
   *
   * 只有 `stored=true` 的条目会进来 —— "有元数据但没存字节"的旧图在这里被
   * 在归一化阶段就滤掉，避免组件里再判一次（两处判据必然漂移）。
   */
  images?: AgentEventImage[]
  /** 仅 rewind 事件填充 */
  rewind?: RewindPayload
  /** 仅 model_switch 事件填充 */
  modelSwitch?: ModelSwitchPayload
  /** 仅 mode_switch 事件填充 */
  modeSwitch?: ModeSwitchPayload
  /** 仅 plan 事件填充 */
  plan?: PlanPayload
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
