// 事件工具：id 生成 / 去重

import type { AgentEvent } from '@/types/event'

/** 持久化事件的稳定唯一键：`taskId:seq` */
export function persistentEventId(taskId: string, seq: number): string {
  return `${taskId}:${seq}`
}

/**
 * 事件去重集合：按 event.id 判断是否已存在。
 * 覆盖网络重连 / 服务端重复推送 / 页面重进场景（方案 §14）。
 */
export class EventDeduper {
  private seen = new Set<string>()

  /** 已存在返回 true（调用方应跳过），不存在则登记并返回 false */
  has(id: string): boolean {
    return this.seen.has(id)
  }

  add(id: string): void {
    this.seen.add(id)
  }

  /** 批量登记（同步替换场景） */
  addAll(events: AgentEvent[]): void {
    for (const e of events) this.seen.add(e.id)
  }

  reset(): void {
    this.seen.clear()
  }
}

/**
 * 流式增量合并：目标类型与末事件一致则追加文本，否则新建事件。
 * 与后端 appendTextDelta 逻辑互为镜像（方案 §39）。
 */
export function mergeDeltaIntoEvents(
  events: AgentEvent[],
  delta: { taskId: string; text: string; isThought: boolean },
  nextId: () => string,
): { events: AgentEvent[]; appended: boolean; firstText: boolean } {
  const targetType = delta.isThought ? 'thinking_delta' : 'text_delta'
  const last = events[events.length - 1]
  // 首次正文文本输出（结束「思考中」占位的时机）
  const firstText = !delta.isThought && !events.some((e) => e.type === 'text_delta')

  if (last && last.type === targetType && last.payload.text !== undefined) {
    const merged: AgentEvent = {
      ...last,
      payload: { ...last.payload, text: last.payload.text + delta.text },
    }
    return { events: [...events.slice(0, -1), merged], appended: true, firstText }
  }
  const ev: AgentEvent = {
    id: nextId(),
    taskId: delta.taskId,
    type: targetType,
    timestamp: new Date().toISOString(),
    payload: { text: delta.text },
  }
  return { events: [...events, ev], appended: true, firstText }
}
