import { defineStore } from 'pinia'
import { ref } from 'vue'
import * as api from '@/services/api/requirements'
import type { Requirement, RequirementOutcome } from '@/types/requirement'
import type { Task } from '@/types/task'

export const useRequirementStore = defineStore('requirement', () => {
  const items = ref<Requirement[]>([])
  const current = ref<Requirement | null>(null)
  const outcome = ref<RequirementOutcome | null>(null)
  const tasks = ref<Task[]>([])
  const loading = ref(false)
  const error = ref('')

  async function loadList() {
    loading.value = true
    error.value = ''
    try {
      items.value = await api.listRequirements()
    } catch (e) {
      error.value = e instanceof Error ? e.message : '加载失败'
    } finally {
      loading.value = false
    }
  }

  async function loadDetail(id: string) {
    loading.value = true
    error.value = ''
    try {
      const [r, o, ts] = await Promise.all([
        api.getRequirement(id),
        api.getRequirementOutcome(id),
        api.listTasksForRequirement(id),
      ])
      current.value = r
      outcome.value = o
      tasks.value = ts
    } catch (e) {
      error.value = e instanceof Error ? e.message : '加载失败'
      current.value = null
    } finally {
      loading.value = false
    }
  }

  return { items, current, outcome, tasks, loading, error, loadList, loadDetail }
})
