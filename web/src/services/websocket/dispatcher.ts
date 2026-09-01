// Event Dispatcher（方案 §13）：WS → normalize → 分发到 Pinia Store。
// 组件禁止自己解释 WebSocket 消息。

import type { RealtimeMessage } from './normalizer'
import type { TaskStore } from '@/stores/task'
import type { SessionStore } from '@/stores/session'

export interface DispatchTargets {
  taskStore: TaskStore
  sessionStore: SessionStore
}

/** 把一条归一化消息应用到 Store（顺序：Task 元数据 → Session 事件流） */
export function dispatch(msg: RealtimeMessage, t: DispatchTargets): void {
  switch (msg.type) {
    case 'snapshot':
      t.taskStore.applySnapshot(msg.tasks)
      // 快照含全量 events：逐任务同步会话事件流（带去重，重连重复推送安全）
      t.sessionStore.syncSessions(msg.dtos)
      return
    case 'task_upserted':
      t.taskStore.upsertTask(msg.task)
      t.sessionStore.syncFromTask(msg.dto)
      return
    case 'task_deleted':
      t.taskStore.removeTask(msg.taskId)
      t.sessionStore.removeSession(msg.taskId)
      return
    case 'delta':
      // 未知任务（快照未到/已删除）：丢弃，等 task_updated 兜底（与 V1 行为一致）
      if (!t.taskStore.byId(msg.delta.taskId)) return
      t.sessionStore.applyDelta(msg.delta)
      return
  }
}
