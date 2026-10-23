// Event Normalizer（方案 §41/§42）：后端 wire format → 前端稳定模型。
// 这是 V2 最重要的隔离层：后端事件协议变化时只改本文件。

import type {
  WsMessageDto,
  TaskDto,
  TaskSummaryDto,
  TaskEventDto,
  TaskImageDto,
  UsagePayloadDto,
} from '@/types/api'
import type {
  AgentEvent,
  AgentEventType,
  AgentDelta,
  AgentEventImage,
  RewindPayload,
  ModelSwitchPayload,
} from '@/types/event'
import type { Task, TaskUsage } from '@/types/task'
import { adaptTask } from '@/services/api/client'
import { persistentEventId } from '@/utils/event'

/**
 * 归一化后的实时消息（dispatcher 的输入）。
 *
 * seq 是后端分配的全局事件序号，**每种消息都有**（缺省 0 = 未知）。
 * 它不参与业务分发，只供 SeqTracker 判断连续性 —— 放在联合类型上而不是
 * 各分支里，是为了让"每条消息都带 seq"成为类型上的事实，新增消息类型时
 * 不会漏掉（漏掉就是一个静默的 gap 检测盲区）。
 */
export type RealtimeMessage = { seq: number } & (
  | { type: 'snapshot'; tasks: Task[]; dtos: TaskSummaryDto[] }
  | { type: 'task_upserted'; task: Task; dto: TaskDto }
  | { type: 'task_deleted'; taskId: string }
  | { type: 'delta'; delta: AgentDelta }
  | { type: 'usage'; taskId: string; usage: TaskUsage }
)

/** 校验 + 归一化一条 WS 消息；无法识别返回 null（静默丢弃） */
export function normalizeWsMessage(raw: unknown): RealtimeMessage | null {
  if (typeof raw !== 'object' || raw === null) return null
  const msg = raw as Partial<WsMessageDto>

  // seq 缺失（旧服务端）按 0 处理：0 = "服务端没告诉我"，消费方据此跳过 gap 判断
  // —— 宁可不判定，也不能凭一个缺失的字段去误报"丢事件"。
  const seq = typeof msg.seq === 'number' && msg.seq > 0 ? msg.seq : 0

  switch (msg.type) {
    case 'snapshot':
      if (!Array.isArray(msg.tasks)) return null
      return { type: 'snapshot', tasks: msg.tasks.map(adaptTask), dtos: msg.tasks, seq }
    case 'task_created':
    case 'task_updated':
    // 后端 completed 终态走专用类型（task_runner.go transition），载荷与 task_updated 相同；
    // 不接住则命中 default 被静默丢弃，详情页状态要刷新页面才更新
    case 'task_completed':
      if (!msg.task_id || !msg.task) return null
      return { type: 'task_upserted', task: adaptTask(msg.task), dto: msg.task, seq }
    case 'task_deleted':
      if (!msg.task_id) return null
      return { type: 'task_deleted', taskId: msg.task_id, seq }
    case 'task_delta': {
      if (!msg.task_id || !msg.delta || typeof msg.delta.text !== 'string') return null
      return {
        type: 'delta',
        delta: { taskId: msg.task_id, text: msg.delta.text, isThought: !!msg.delta.is_thought },
        seq,
      }
    }
    case 'task_usage': {
      const u = adaptUsagePayload(msg.task_id, msg.usage)
      if (!u) return null
      return { type: 'usage', taskId: msg.task_id!, usage: u, seq }
    }
    default:
      return null
  }
}

/**
 * 校验并归一化 usage 载荷；不合法返回 undefined。
 *
 * 判据：`size > 0` 是这份快照有意义的**必要条件**（"占用了 x/0" 不是信息）。
 * 后端在 ACPAgent 与 core wire 两处都拦过 size<=0，这里再拦一次不是不信任对端，
 * 而是这一层的职责就是"进来的一定是合法模型"—— 消费方（进度条）不该再各写一遍守卫。
 */
export function adaptUsagePayload(
  taskId: unknown,
  raw: UsagePayloadDto | undefined,
): TaskUsage | undefined {
  if (typeof taskId !== 'string' || !taskId) return undefined
  if (!raw || typeof raw.used !== 'number' || typeof raw.size !== 'number') return undefined
  if (raw.size <= 0) return undefined
  return {
    used: raw.used,
    size: raw.size,
    costUsd: raw.cost_usd,
    // has_cost 缺省即 false：只有 agent 明确报了成本才显示金额。
    hasCost: raw.has_cost === true,
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
 * 图片元数据 → 可渲染的引用；只收**已落盘**的（stored=true 且有 hash）。
 *
 * 为什么在这里滤而不是在组件里：这是"这张图能不能画"的唯一判据。放在归一化层之后，
 * 组件拿到 images 就直接画，不会再出现"组件判一次、别处再判一次"的漂移；
 * 而历史数据里 stored 缺省（旧版只记了元数据没存字节），在这里被统一挡掉 ——
 * 表现是"这条事件没有图"，而不是一排永远加载不出来的破图。
 *
 * 为什么不让后端直接不返回那些条目：事件是事实源，元数据本身是真实发生过的
 *（用户确实发过这张图，只是字节没留住）。过滤属于**展示适配**，归前端。
 */
function adaptEventImages(list: TaskImageDto[] | undefined): AgentEventImage[] | undefined {
  if (!Array.isArray(list) || list.length === 0) return undefined
  const out: AgentEventImage[] = []
  for (const img of list) {
    if (!img || img.stored !== true) continue
    if (typeof img.hash !== 'string' || !/^[0-9a-fA-F]{64}$/.test(img.hash)) continue
    out.push({
      mimeType: typeof img.mime_type === 'string' ? img.mime_type : 'image/png',
      bytes: typeof img.bytes === 'number' ? img.bytes : 0,
      hash: img.hash.toLowerCase(),
      name: typeof img.name === 'string' && img.name ? img.name : undefined,
    })
  }
  return out.length ? out : undefined
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
        images: adaptEventImages(ev.images),
        // rewind 事件：结构化载荷（to_turn / restored / preview_stopped）
        rewind: ev.type === 'rewind' ? adaptRewindInput(ev.input) : undefined,
        // model_switch 事件：结构化载荷（from / to 均为不透明选择值）
        modelSwitch: ev.type === 'model_switch' ? adaptModelSwitchInput(ev.input) : undefined,
      },
    }
  })
}
