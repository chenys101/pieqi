// useTask：单个任务的响应式视图 + 操作（页面级组合）

import { computed, type Ref } from 'vue'
import { useTaskStore } from '@/stores/task'
import { useNotificationStore } from '@/stores/notification'

export function useTask(taskId: Ref<string>) {
  const store = useTaskStore()
  const notify = useNotificationStore()

  const task = computed(() => store.byId(taskId.value))
  const canCancel = computed(
    () => !!task.value && ['pending', 'running', 'waiting_input'].includes(task.value.status),
  )

  async function cancel() {
    if (!task.value) return
    try {
      await store.cancelTask(task.value.id)
    } catch (err) {
      notify.error(err instanceof Error ? err.message : '取消失败')
    }
  }

  async function remove() {
    if (!task.value) return
    try {
      await store.deleteTask(task.value.id)
    } catch (err) {
      notify.error(err instanceof Error ? err.message : '删除失败')
    }
  }

  return { task, canCancel, cancel, remove }
}
