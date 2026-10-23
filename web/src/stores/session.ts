// Session Store（方案 §10.2）：整个前端最重要的 Store。
// 持有 eventsBySession（未来 Replay 的数据资产，方案 §43）、
// 实时连接状态与「思考中」占位标记。

import { defineStore } from 'pinia'
import type { AgentSession } from '@/types/session'
import type { AgentEvent, AgentDelta } from '@/types/event'
import type { TaskDto, TaskSummaryDto } from '@/types/api'
import type { TaskStatus } from '@/types/task'
import { isTerminalStatus } from '@/types/task'
import { normalizeEvents } from '@/services/websocket/normalizer'
import { DEFAULT_AGENT } from '@/services/api/client'
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
    /**
     * taskId → 事件流里是否已出现过正文（text_delta）。
     *
     * 纯性能缓存，不是业务状态：`mergeDeltaIntoEvents` 判定 `firstText` 需要扫全量事件，
     * 而该真值**单调**（一旦有正文就永远为 false）。逐字流式时每条增量都扫一遍是
     * O(N×n) —— 2950 事件的任务上这就是主线程吃紧的来源。
     * 这里记住结果，把扫描降到每任务一次。缺省 false = 未知 ⇒ 回退扫描（与改动前一致）。
     */
    hasText: {} as Record<string, boolean>,
    /**
     * taskId → 本地这份事件流**是否来自权威载荷**（带 events 数组的详情/事件推送）。
     *
     * 存在的理由：`eventsBySession[id]` 为空有两种完全不同的含义 ——
     * 「拉到过，后端说这个任务确实没有事件」与「还没拉到」。只有后者需要详情页去拉；
     * 而把前者也当成"没有"去重拉，会让一条本该显示"暂无输出"的页面反复打接口。
     * 判据因此必须是一个**独立位**，不能从数组长度反推（那正是 2026-10-23 的 bug）。
     */
    eventsLoaded: {} as Record<string, boolean>,
  }),

  getters: {
    session(state) {
      return (taskId: string): AgentSession | undefined => state.sessions[taskId]
    },
    events(state) {
      return (taskId: string): AgentEvent[] => state.eventsBySession[taskId] ?? []
    },
    /**
     * 本地事件流是否**已由权威载荷载入过**（供详情页决定"还要不要去拉"）。
     *
     * 与 `events(id).length > 0` 是两件事：后者把"拉到过且确实为空"和"还没拉到"
     * 混成同一个答案，而详情页恰恰要靠这个区别来决定是否发请求。
     */
    eventsKnown(state) {
      return (taskId: string): boolean => !!state.eventsLoaded[taskId]
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

    /** snapshot：批量同步会话（轻量视图：只有元信息，不含 events） */
    syncSessions(dtos: (TaskDto | TaskSummaryDto)[]) {
      for (const dto of dtos) this.syncFromTask(dto)
    },

    /**
     * task_updated / task_created / 单任务详情：同步会话元信息 + 全量事件。
     * events 为后端持久化真相（已含此前推送的增量），直接替换并登记去重。
     *
     * ⚠️ **载荷没带事件流时，既不能清空、也不能"假装知道它是空的"**。
     *
     * 两种"没带"的形态都要认（2026-10-23 修）：
     *   - `'events' in dto === false`：TS 层的 TaskSummaryDto（Omit 掉了 events）；
     *   - **`dto.events === null`**：wire 层的实况。后端的 TaskSummary 用非 omitempty 的
     *     `Events []TaskEvent` **遮蔽**了 Task 的同名字段，nil 切片序列化出来是
     *     `"events": null` —— 于是后端模型上明确标注的"不下发"，到了前端 DOM 里
     *     变成了"这个任务有 0 条事件"。这两个语义的差别正是详情页打不开的成因：
     *     详情页判据是「本地已有事件流就跳过拉取」（见 SessionPage 的 loadDetail），
     *     被写成"空数组"之后那条判据被满足，页面就既不渲染、也不再去要数据。
     *
     * 判据统一收敛到 `shouldReplaceEvents`：**只有数组才算"后端说了什么"**，
     * 空数组（`[]`）仍然表示"后端明确说没有事件"。
     */
    syncFromTask(dto: TaskDto | TaskSummaryDto) {
      this.sessions[dto.id] = {
        id: dto.id,
        taskId: dto.id,
        // 执行该任务的 agent（claude / qoder）；旧任务 DTO 无该字段 → 默认 agent
        agent: dto.agent || DEFAULT_AGENT,
        status: dto.status,
        startedAt: dto.started_at,
        endedAt: dto.finished_at,
      }
      // 用 'events' in dto 而非 dto.events 做类型收窄：TaskSummaryDto 上根本没有
      // 这个属性（Omit 掉了），直接访问过不了 TS 检查。
      // 而 `in` 单独**不够**：wire 上的 null 也满足 `in`（见 shouldReplaceEvents）。
      if (shouldReplaceEvents(dto)) {
        const events = normalizeEvents(dto.id, dto.events)
        this.eventsBySession[dto.id] = events
        this.deduper(dto.id).addAll(events)
        // 重算性能缓存：全量替换后，之前的判断可能不再成立。
        this.hasText[dto.id] = events.some((e) => e.type === 'text_delta')
        // ★ 只有**权威载荷**（显式数组）才算"本地已知该会话的事件流"。
        // 详情页据此决定"还要不要去拉"：没拉到过就必须拉，否则时间线永远空着。
        this.eventsLoaded[dto.id] = true
      }

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
      // 传第四个参数把「是否已有正文」这个已知真值喂给合并函数，省掉一次全量扫描
      // （见 state.hasText 与 mergeDeltaIntoEvents 的 hasText 说明）。
      const { events, firstText } = mergeDeltaIntoEvents(
        current,
        delta,
        () => `${delta.taskId}:delta-${counter}`,
        this.hasText[delta.taskId] === true,
      )
      this.eventsBySession[delta.taskId] = events
      // 缓存单调推进：只要出现过正文就永远为 true，不需要每帧重扫。
      if (!delta.isThought && firstText) this.hasText[delta.taskId] = true
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
      delete this.hasText[taskId]
      delete this.eventsLoaded[taskId]
    },
  },
})

/**
 * 这条 DTO 是否**真带了一份事件流**（可以覆盖本地）。
 *
 * 判据是「是数组」。三档语义必须分清，混掉任何两档都会出 bug：
 *   - 缺字段 / `null` → **没说**（后端模型里就是"不下发"，TaskSummary 用 nil 切片遮蔽）
 *   - `[]`            → **说了：没有事件**
 *   - `[...]`         → 说了：这些就是全部
 *
 * 放宽成 `'events' in dto` 会让 Wire 上的 `null` 走进"说了没有事件"那一档，
 * 而它真正想说的是"我没给你"。这条函数就是那个分界线，不要在每个调用点各写一遍。
 */
export function shouldReplaceEvents(dto: TaskDto | TaskSummaryDto): boolean {
  return 'events' in dto && Array.isArray((dto as TaskDto).events)
}

/** Store 实例类型（dispatcher 等消费方引用） */
export type SessionStore = ReturnType<typeof useSessionStore>
