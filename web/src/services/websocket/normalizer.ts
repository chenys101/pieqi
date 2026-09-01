// Event Normalizer（方案 §41/§42）：后端 wire format → 前端稳定模型。
// 这是 V2 最重要的隔离层：后端事件协议变化时只改本文件。

import type {
  WsMessageDto,
  TaskDto,
  TaskEventDto,
} from '@/types/api'
import type { AgentEvent, AgentEventType, AgentDelta } from '@/types/event'
import type { Task } from '@/types/task'
import { adaptTask } from '@/services/api/client'
import { persistentEventId } from '@/utils/event'

/** 归一化后的实时消息（dispatcher 的输入） */
export type RealtimeMessage =
  | { type: 'snapshot'; tasks: Task[]; dtos: TaskDto[] }
  | { type: 'task_upserted'; task: Task; dto: TaskDto }
  | { type: 'task_deleted'; taskId: string }
  | { type: 'delta'; delta: AgentDelta }

/** 校验 + 归一化一条 WS 消息；无法识别返回 null（静默丢弃） */
export function normalizeWsMessage(raw: unknown): RealtimeMessage | null {
  if (typeof raw !== 'object' || raw === null) return null
  const msg = raw as Partial<WsMessageDto>

  switch (msg.type) {
    case 'snapshot':
      if (!Array.isArray(msg.tasks)) return null
      return { type: 'snapshot', tasks: msg.tasks.map(adaptTask), dtos: msg.tasks }
    case 'task_created':
    case 'task_updated':
      if (!msg.task_id || !msg.task) return null
      return { type: 'task_upserted', task: adaptTask(msg.task), dto: msg.task }
    case 'task_deleted':
      if (!msg.task_id) return null
      return { type: 'task_deleted', taskId: msg.task_id }
    case 'task_delta': {
      if (!msg.task_id || !msg.delta || typeof msg.delta.text !== 'string') return null
      return {
        type: 'delta',
        delta: { taskId: msg.task_id, text: msg.delta.text, isThought: !!msg.delta.is_thought },
      }
    }
    default:
      return null
  }
}

/** 后端事件类型 → 前端稳定事件类型映射 */
const EVENT_TYPE_MAP: Record<TaskEventDto['type'], AgentEventType> = {
  user: 'user_message',
  text: 'text_delta',
  thinking: 'thinking_delta',
  tool_use: 'tool_call',
  tool_result: 'tool_result',
  status: 'status',
}

/**
 * 后端 events[] → 前端 AgentEvent[]（id = taskId:seq，用于去重）。
 * 兼容旧数据：「↻ 续问: 」前缀的 text 事件归一为 user_message。
 */
export function normalizeEvents(taskId: string, events: TaskEventDto[] | undefined): AgentEvent[] {
  if (!events?.length) return []
  return events.map((ev) => {
    let type = EVENT_TYPE_MAP[ev.type] ?? 'status'
    let text = ev.text ?? ''
    if (type === 'text_delta' && text.startsWith('↻ 续问: ')) {
      type = 'user_message'
      text = text.slice('↻ 续问: '.length)
    }
    return {
      id: persistentEventId(taskId, ev.seq),
      taskId,
      type,
      timestamp: ev.at,
      payload: {
        text,
        toolName: ev.tool_name,
        toolUseId: ev.tool_use_id,
        input: ev.input,
        result: ev.result,
        isError: ev.is_error,
      },
    }
  })
}
