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
      // 快照是**轻量视图**（无 events）：只同步会话元信息，
      // 时间线由详情页按需拉取（syncFromTask 遇 undefined events 会保留本地事件流）。
      // 该消息同时承担"丢弃事件后的重同步"，与首帧同路径。
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
    case 'usage':
      // 用量是 task 的**可变快照**，落回 taskStore（详情页/列表都从这里读），
      // 不进 sessionStore 的事件流 —— 它不是"发生过的一件事"，而是"此刻的状态"。
      // 未知任务同样丢弃（快照未到/已删除）。
      if (!t.taskStore.byId(msg.taskId)) return
      t.taskStore.applyUsage(msg.taskId, msg.usage)
      return
  }
}
