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
 *
 * `hasText` 是**可选**的性能提示（2026-10-22）：
 * 原先 `firstText` 每次都用 `events.some(e => e.type === 'text_delta')` 求，
 * 而这是**每条增量**都跑一次的全量扫描 —— 长会话里一轮回答 N 条增量 × n 个事件
 * 就是 O(N×n)，实测 2950 事件的任务在逐字流式时主线程明显吃力。
 *
 * 判据：`firstText` 的真值只会**单调地**从 true 变 false（一旦出现过 text_delta
 * 就永远是 false），所以它可以由调用方记住一次、之后 O(1) 读。
 * 传 `true` = 调用方保证「此前一定已有 text_delta」⇒ 跳过扫描。
 * 不传 / 传 false = 行为与改动前完全一致（保守回退到扫描），
 * 这样所有既有调用方与测试都不需要改语义。
 */
export function mergeDeltaIntoEvents(
  events: AgentEvent[],
  delta: { taskId: string; text: string; isThought: boolean },
  nextId: () => string,
  hasText = false,
): { events: AgentEvent[]; appended: boolean; firstText: boolean } {
  const targetType = delta.isThought ? 'thinking_delta' : 'text_delta'
  const last = events[events.length - 1]
  // 首次正文文本输出（结束「思考中」占位的时机）。
  // 已知此前有过正文时直接为 false，省掉一次全量扫描（见上方 hasText 的说明）。
  const firstText = !delta.isThought && !hasText && !events.some((e) => e.type === 'text_delta')

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

/** 事件流里是否已经有正文（text_delta）。供调用方在增量路径上记住结果，避免重复扫描。 */
export function hasTextDelta(events: AgentEvent[]): boolean {
  return events.some((e) => e.type === 'text_delta')
}
