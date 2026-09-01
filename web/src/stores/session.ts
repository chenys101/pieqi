// Session Store（方案 §10.2）：整个前端最重要的 Store。
// 持有 eventsBySession（未来 Replay 的数据资产，方案 §43）、
// 实时连接状态与「思考中」占位标记。

import { defineStore } from 'pinia'
import type { AgentSession } from '@/types/session'
import type { AgentEvent, AgentDelta } from '@/types/event'
import type { TaskDto } from '@/types/api'
import type { TaskStatus } from '@/types/task'
import { isTerminalStatus } from '@/types/task'
import { normalizeEvents } from '@/services/websocket/normalizer'
import { EventDeduper, mergeDeltaIntoEvents } from '@/utils/event'
import type { ConnectionState } from '@/services/websocket/client'

export const useSessionStore = defineStore('session', {
  state: () => ({
    /** 会话元信息（从 Task 派生，taskId 为键） */
    sessions: {} as Record<string, AgentSession>,
    /** 会话事件流：Replay / Timeline 的核心数据（方案 §39/§43） */
    eventsBySession: {} as Record<string, AgentEvent[]>,
    /** WS 连接状态（全局 UI 指示） */
    connection: 'initial' as ConnectionState,
    /** taskId → 思考中标记（提交后等待首次流式输出的过渡态） */
    thinking: {} as Record<string, boolean>,
    /** 流式增量合成 id 计数器 */
    deltaCounters: {} as Record<string, number>,
    /** 去重集合（方案 §14）：网络重连 / 重复推送 / 页面重进 */
    dedupers: {} as Record<string, EventDeduper>,
  }),

  getters: {
    session(state) {
      return (taskId: string): AgentSession | undefined => state.sessions[taskId]
    },
    events(state) {
      return (taskId: string): AgentEvent[] => state.eventsBySession[taskId] ?? []
    },
    isThinking(state) {
      return (taskId: string): boolean => !!state.thinking[taskId]
    },
    connectionLabel(): string {
      switch (this.connection) {
        case 'connected':
          return '已连接'
        case 'connecting':
          return '连接中'
        case 'reconnecting':
          return '重连中'
        case 'disconnected':
          return '已断开'
        default:
          return '未连接'
      }
    },
  },

  actions: {
    setConnection(state: ConnectionState) {
      this.connection = state
    },

    deduper(taskId: string): EventDeduper {
      if (!this.dedupers[taskId]) this.dedupers[taskId] = new EventDeduper()
      return this.dedupers[taskId]
    },

    /** snapshot：批量同步会话 */
    syncSessions(dtos: TaskDto[]) {
      for (const dto of dtos) this.syncFromTask(dto)
    },

    /**
     * task_updated / task_created / 单任务详情：同步会话元信息 + 全量事件。
     * events 为后端持久化真相（已含此前推送的增量），直接替换并登记去重。
     */
    syncFromTask(dto: TaskDto) {
      this.sessions[dto.id] = {
        id: dto.id,
        taskId: dto.id,
        agent: 'claude-code',
        status: dto.status,
        startedAt: dto.started_at,
        endedAt: dto.finished_at,
      }
      const events = normalizeEvents(dto.id, dto.events)
      this.eventsBySession[dto.id] = events
      this.deduper(dto.id).addAll(events)

      // 终态 / 需决策：清除思考占位（此时是请求决策而非思考）
      if (isTerminalStatus(dto.status) || dto.status === 'waiting_input') {
        this.thinking[dto.id] = false
      }
    },

    /**
     * task_delta 真流式（方案 §39）：同类型末事件追加，否则新建；
     * 首次正文文本清除「思考中」占位。
     */
    applyDelta(delta: AgentDelta) {
      if (!delta.text) return
      const current = this.eventsBySession[delta.taskId] ?? []
      const counter = (this.deltaCounters[delta.taskId] ?? 0) + 1
      this.deltaCounters[delta.taskId] = counter
      const { events, firstText } = mergeDeltaIntoEvents(current, delta, () => `${delta.taskId}:delta-${counter}`)
      this.eventsBySession[delta.taskId] = events
      if (firstText) this.thinking[delta.taskId] = false
    },

    /** 乐观插入用户消息（提交续问后不等 WS，方案 §36 Optimistic UI） */
    appendLocalUserMessage(taskId: string, text: string) {
      const counter = (this.deltaCounters[taskId] ?? 0) + 1
      this.deltaCounters[taskId] = counter
      const ev: AgentEvent = {
        id: `${taskId}:local-${counter}`,
        taskId,
        type: 'user_message',
        timestamp: new Date().toISOString(),
        payload: { text },
      }
      this.eventsBySession[taskId] = [...(this.eventsBySession[taskId] ?? []), ev]
    },

    setThinking(taskId: string, on: boolean) {
      this.thinking[taskId] = on
    },

    /** 会话状态本地微调（审批后乐观更新） */
    patchSessionStatus(taskId: string, status: TaskStatus) {
      const s = this.sessions[taskId]
      if (s) s.status = status
      if (isTerminalStatus(status) || status === 'waiting_input') this.thinking[taskId] = false
    },

    removeSession(taskId: string) {
      delete this.sessions[taskId]
      delete this.eventsBySession[taskId]
      delete this.thinking[taskId]
      delete this.deltaCounters[taskId]
      delete this.dedupers[taskId]
    },
  },
})

/** Store 实例类型（dispatcher 等消费方引用） */
export type SessionStore = ReturnType<typeof useSessionStore>
