// useSession：Session 页核心组合 —— 事件流 / 干预 / 决策 / 滚动跟随

import { computed, ref, watch, type Ref } from 'vue'
import { useSessionStore } from '@/stores/session'
import { useTaskStore } from '@/stores/task'
import { useApprovalStore } from '@/stores/approval'
import { useNotificationStore } from '@/stores/notification'
import { isTerminalStatus } from '@/types/task'
import * as tasksApi from '@/services/api/tasks'
import type { PendingImage } from '@/features/session/imageAttach'

export function useSession(taskId: Ref<string>) {
  const sessionStore = useSessionStore()
  const taskStore = useTaskStore()
  const approvalStore = useApprovalStore()
  const notify = useNotificationStore()

  const task = computed(() => taskStore.byId(taskId.value))
  const session = computed(() => sessionStore.session(taskId.value))
  const events = computed(() => sessionStore.events(taskId.value))

  /** 终态可续问（走 Resume）；运行态按钮为中止 */
  const canSendPrompt = computed(() => (task.value ? isTerminalStatus(task.value.status) : false))
  const canCancel = computed(
    () => !!task.value && ['pending', 'running', 'waiting_input'].includes(task.value.status),
  )

  /** 「思考中」占位：提交后标记，或冷启动（运行中且无任何输出）兜底 */
  const showThinking = computed(() => {
    if (!task.value) return false
    const active = task.value.status === 'running' || task.value.status === 'pending'
    if (!active) return false
    if (sessionStore.isThinking(task.value.id)) return true
    return events.value.length === 0 && !task.value.output
  })

  /** 提交后强制滑到底（一次性标记，事件更新消费） */
  const forceScroll = ref<string | null>(null)

  /**
   * 追加 prompt：运行中 → stdin 注入；终态 → Resume 续问。
   * 成功后乐观插入用户气泡 + 思考占位（方案 §36）。
   *
   * model（可选）是**本轮**要用的模型（不透明选择值，见 GET /api/agents/{agent}/models）。
   * 空 = 沿用会话当前路由。只对续问（Resume 起新一轮）有意义 —— 后端在纯 stdin 注入的
   * 路径上不看它，故 UI 也只在可续问时给出这个选择器（见 InterveneInput）。
   *
   * images（可选）随这条消息发出。**纯 base64**（见 imageAttach.ts 的说明）。
   * 同样只对续问有意义：运行中的 stdin 注入是纯文本通道。
   */
  async function submitPrompt(text: string, model?: string, images?: PendingImage[]) {
    const id = taskId.value
    // 有图就算有内容：只发一张图、不写字是正当用法。
    if (!text.trim() && !images?.length) return
    try {
      await tasksApi.intervene(id, {
        kind: 'append_prompt',
        text: text.trim(),
        model: model || undefined,
        images: images?.length
          ? images.map((i) => ({ data: i.data, mime_type: i.mimeType }))
          : undefined,
      })
      sessionStore.appendLocalUserMessage(id, text.trim())
      sessionStore.setThinking(id, true)
      forceScroll.value = id
    } catch (err) {
      notify.error(err instanceof Error ? err.message : '发送失败')
      throw err
    }
  }

  async function cancel() {
    try {
      await taskStore.cancelTask(taskId.value)
    } catch (err) {
      notify.error(err instanceof Error ? err.message : '取消失败')
    }
  }

  async function approve() {
    try {
      await approvalStore.approve(taskId.value)
    } catch (err) {
      notify.error(err instanceof Error ? err.message : '审批失败')
    }
  }

  /** 批准 + 本会话同类免审（只有 ACP 路径的决策带这个动作） */
  async function approveSession() {
    try {
      await approvalStore.approveSession(taskId.value)
    } catch (err) {
      notify.error(err instanceof Error ? err.message : '审批失败')
    }
  }

  async function deny() {
    try {
      await approvalStore.deny(taskId.value)
    } catch (err) {
      notify.error(err instanceof Error ? err.message : '拒绝失败')
    }
  }

  /** 事件变化时消费强制滚动标记 */
  function consumeForceScroll(): boolean {
    if (forceScroll.value === taskId.value) {
      forceScroll.value = null
      return true
    }
    return false
  }

  return {
    task,
    session,
    events,
    showThinking,
    canSendPrompt,
    canCancel,
    submitPrompt,
    cancel,
    approve,
    approveSession,
    deny,
    consumeForceScroll,
  }
}

/** 滚动跟随：在底部时跟随新事件；翻历史不被打断（方案 §36/§39） */

/**
 * 「贴底」判定余量（px）。
 *
 * 导出而不是在组件里另写一个：**"在不在底部"只能有一处真相** ——
 * 底部跟随（这里）与「直达最新输出」按钮的显隐（SessionTimeline）若各写一个阈值，
 * 就会出现「按钮说你在底部、而新输出没跟随」这种自相矛盾的界面。
 */
export const BOTTOM_SLACK = 120

export function useTimelineScroll(eventsRef: Ref<unknown[]>, consumeForceScroll: () => boolean) {
  const el = ref<HTMLElement | null>(null)
  let nearBottom = true

  function onScroll() {
    if (!el.value) return
    nearBottom = el.value.scrollHeight - el.value.scrollTop - el.value.clientHeight < BOTTOM_SLACK
  }

  watch(
    () => eventsRef.value.length,
    () => {
      const force = consumeForceScroll()
      if (!force && !nearBottom) return
      requestAnimationFrame(() => {
        if (el.value) el.value.scrollTop = el.value.scrollHeight
        nearBottom = true
      })
    },
  )

  /** 切换会话：强制到底 */
  function scrollToEnd() {
    requestAnimationFrame(() => {
      if (el.value) el.value.scrollTop = el.value.scrollHeight
      nearBottom = true
    })
  }

  return { el, onScroll, scrollToEnd }
}
