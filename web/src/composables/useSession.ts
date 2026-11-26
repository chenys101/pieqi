// useSession：Session 页核心组合 —— 事件流 / 干预 / 决策 / 滚动跟随

import { computed, ref, watch, type Ref } from 'vue'
import { useSessionStore } from '@/stores/session'
import { useTaskStore } from '@/stores/task'
import { useApprovalStore } from '@/stores/approval'
import { useNotificationStore } from '@/stores/notification'
import { isTerminalStatus } from '@/types/task'
import * as tasksApi from '@/services/api/tasks'
import type { PendingImage } from '@/types/image'

export function useSession(taskId: Ref<string>) {
  const sessionStore = useSessionStore()
  const taskStore = useTaskStore()
  const approvalStore = useApprovalStore()
  const notify = useNotificationStore()

  const task = computed(() => taskStore.byId(taskId.value))
  const session = computed(() => sessionStore.session(taskId.value))
  const events = computed(() => sessionStore.events(taskId.value))

  /**
   * 可发补充：终态续问，或 running 排队下一轮（ResumeRich，可带图）。
   * pending / waiting_input(approval) 不在此列 —— 前者尚无会话，后者先答审批卡。
   */
  const canSendPrompt = computed(() => {
    if (!task.value) return false
    const s = task.value.status
    return isTerminalStatus(s) || s === 'running'
  })
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
   * 追加 prompt：终态 / running 都走 ResumeRich（running = 排队到本轮结束后）。
   * 成功后乐观插入用户气泡 + 思考占位（方案 §36）。
   *
   * model（可选）是**本轮**要用的模型（不透明选择值，见 GET /api/agents/{agent}/models）。
   * 空 = 沿用会话当前路由。只对 Resume 起新一轮有意义。
   *
   * images（可选）随这条消息发出。**纯 base64**（见 imageAttach.ts 的说明）。
   * running 也可带图（排队下一轮）；waiting_input(approval) 仍不收图。
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

  /**
   * 三个决策动作的失败**一律要说出来**（notify.error）。
   *
   * 2026-10-23 f74bcb09 会话里，"点了没反应"之所以完全查不出来：卡片 loading 被 finally
   * 释放了，界面回到原样，而失败只体现在服务端日志的一行 no pending 里。
   * 现在后端会把"已经处理过"与"id 不存在"分开回报，两种都必须浮到用户面前。
   */
  async function approve() {
    try {
      await approvalStore.approve(taskId.value)
    } catch (err) {
      notify.error(err instanceof Error ? err.message : '审批失败')
    }
  }

  /** 批准 + 本 turn 同类免审（只有 ACP 路径的决策带这个动作） */
  async function approveTurn() {
    try {
      await approvalStore.approveTurn(taskId.value)
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
    approveTurn,
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
