// useSession：Session 页核心组合 —— 事件流 / 干预 / 决策 / 滚动跟随

import { computed, ref, watch, type Ref } from 'vue'
import { useSessionStore } from '@/stores/session'
import { useTaskStore } from '@/stores/task'
import { useApprovalStore } from '@/stores/approval'
import { useNotificationStore } from '@/stores/notification'
import { isTerminalStatus } from '@/types/task'
import * as tasksApi from '@/services/api/tasks'

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
   */
  async function submitPrompt(text: string) {
    const id = taskId.value
    if (!text.trim()) return
    try {
      await tasksApi.intervene(id, { kind: 'append_prompt', text: text.trim() })
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
    deny,
    consumeForceScroll,
  }
}

/** 滚动跟随：在底部时跟随新事件；翻历史不被打断（方案 §36/§39） */
export function useTimelineScroll(eventsRef: Ref<unknown[]>, consumeForceScroll: () => boolean) {
  const el = ref<HTMLElement | null>(null)
  let nearBottom = true

  function onScroll() {
    if (!el.value) return
    nearBottom = el.value.scrollHeight - el.value.scrollTop - el.value.clientHeight < 120
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
