// Event Normalizer（方案 §41/§42）：后端 wire format → 前端稳定模型。
// 这是 V2 最重要的隔离层：后端事件协议变化时只改本文件。

import type {
  WsMessageDto,
  TaskDto,
  TaskSummaryDto,
  TaskEventDto,
} from '@/types/api'
import type { AgentEvent, AgentEventType, AgentDelta, RewindPayload, ModelSwitchPayload } from '@/types/event'
import type { Task } from '@/types/task'
import { adaptTask } from '@/services/api/client'
import { persistentEventId } from '@/utils/event'

/** 归一化后的实时消息（dispatcher 的输入） */
export type RealtimeMessage =
  | { type: 'snapshot'; tasks: Task[]; dtos: TaskSummaryDto[] }
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
    // 后端 completed 终态走专用类型（task_runner.go transition），载荷与 task_updated 相同；
    // 不接住则命中 default 被静默丢弃，详情页状态要刷新页面才更新
    case 'task_completed':
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
  rewind: 'rewind',
  model_switch: 'model_switch',
}

/** rewind 事件的 input 载荷（后端 rewindEventPayload） */
interface RewindInput {
  to_turn?: number
  restored?: string[]
  preview_stopped?: boolean
}

/** 校验并提取 rewind input 载荷；不合法返回 undefined（降级为纯文本展示） */
function adaptRewindInput(raw: unknown): RewindPayload | undefined {
  if (typeof raw !== 'object' || raw === null) return undefined
  const input = raw as RewindInput
  if (typeof input.to_turn !== 'number') return undefined
  return {
    toTurn: input.to_turn,
    restored: Array.isArray(input.restored) ? input.restored : [],
    previewStopped: !!input.preview_stopped,
  }
}

/** model_switch 事件的 input 载荷（后端 model.ModelSwitchPayload） */
interface ModelSwitchInput {
  from?: string
  to?: string
}

/**
 * 校验并提取 model_switch input 载荷；不合法返回 undefined（降级为纯文本展示）。
 *
 * `to` 缺失即视为不合法：切换事件没有目标模型就说明不了"换到了什么"，
 * 与其渲染一条含糊的记录，不如退回后端写好的 Text。
 */
function adaptModelSwitchInput(raw: unknown): ModelSwitchPayload | undefined {
  if (typeof raw !== 'object' || raw === null) return undefined
  const input = raw as ModelSwitchInput
  if (typeof input.to !== 'string') return undefined
  return { from: typeof input.from === 'string' ? input.from : '', to: input.to }
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
        // rewind 事件：结构化载荷（to_turn / restored / preview_stopped）
        rewind: ev.type === 'rewind' ? adaptRewindInput(ev.input) : undefined,
        // model_switch 事件：结构化载荷（from / to 均为不透明选择值）
        modelSwitch: ev.type === 'model_switch' ? adaptModelSwitchInput(ev.input) : undefined,
      },
    }
  })
}
