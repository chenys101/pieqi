// Task Store（方案 §10.1）：任务元数据的唯一可信源。
// 只管 State / Derived / Actions；API 一律走 services 层。

import { defineStore } from 'pinia'
import type { Task, TaskGroup, TaskStatus } from '@/types/task'
import { isTerminalStatus } from '@/types/task'
import * as api from '@/services/api/tasks'
import { adaptTask } from '@/services/api/client'
import { groupKey } from '@/utils/format'
import { timestamp } from '@/utils/date'

export const useTaskStore = defineStore('task', {
  state: () => ({
    tasks: [] as Task[],
    loading: false,
    error: null as string | null,
  }),

  getters: {
    byId(state) {
      return (id: string): Task | undefined => state.tasks.find((t) => t.id === id)
    },
    runningTasks: (s) => s.tasks.filter((t) => t.status === 'running' || t.status === 'pending'),
    waitingTasks: (s) => s.tasks.filter((t) => t.status === 'waiting_input'),
    failedTasks: (s) => s.tasks.filter((t) => t.status === 'failed'),
    /** 需要关注：需决策 + 失败 */
    needsAttention(): Task[] {
      return [...this.waitingTasks, ...this.failedTasks]
    },
    counts(): Record<TaskStatus, number> {
      const c = { pending: 0, running: 0, waiting_input: 0, completed: 0, failed: 0, cancelled: 0 } as Record<TaskStatus, number>
      for (const t of this.tasks) c[t.status]++
      return c
    },
    /** 项目分组（路径归一；组内按活跃时间倒序；含活跃任务的组排前） */
    groupsByProject(state): TaskGroup[] {
      const m = new Map<string, TaskGroup>()
      for (const t of state.tasks) {
        const key = groupKey(t.projectPath)
        let g = m.get(key)
        if (!g) {
          g = {
            key,
            projectId: t.project,
            projectPath: t.projectPath,
            tasks: [],
            counts: { pending: 0, running: 0, waiting_input: 0, completed: 0, failed: 0, cancelled: 0 } as Record<TaskStatus, number>,
          }
          m.set(key, g)
        }
        g.tasks.push(t)
        g.counts[t.status]++
      }
      const groups = [...m.values()]
      for (const g of groups) g.tasks.sort((a, b) => timestamp(b.updatedAt) - timestamp(a.updatedAt))
      // 活跃组（running/waiting_input）排前，其余保持首见顺序
      return groups.sort((a, b) => {
        const aActive = a.counts.running + a.counts.waiting_input + a.counts.pending
        const bActive = b.counts.running + b.counts.waiting_input + b.counts.pending
        return bActive - aActive
      })
    },
    /** 新建任务的项目候选：最近使用倒序 */
    recentProjects(state): { projectId: string; projectPath: string }[] {
      const m = new Map<string, { projectId: string; projectPath: string; lastUsed: number }>()
      for (const t of state.tasks) {
        if (!t.projectPath) continue
        const key = groupKey(t.projectPath)
        const ts = timestamp(t.updatedAt) || timestamp(t.createdAt)
        const e = m.get(key)
        if (!e) m.set(key, { projectId: t.project, projectPath: t.projectPath, lastUsed: ts })
        else if (ts > e.lastUsed) e.lastUsed = ts
      }
      return [...m.values()].sort((a, b) => b.lastUsed - a.lastUsed)
    },
  },

  actions: {
    /** 首屏 / 手动刷新：HTTP 拉全量 */
    async loadTasks() {
      this.loading = true
      this.error = null
      try {
        const { tasks } = await api.getTasks()
        this.tasks = tasks
      } catch (err) {
        this.error = err instanceof Error ? err.message : String(err)
      } finally {
        this.loading = false
      }
    },

    /** WS snapshot 全量替换 */
    applySnapshot(tasks: Task[]) {
      this.tasks = tasks
    },

    /** WS task_created / task_updated：不存在则插入，存在则替换 */
    upsertTask(task: Task) {
      const i = this.tasks.findIndex((t) => t.id === task.id)
      if (i >= 0) this.tasks[i] = task
      else this.tasks.push(task)
    },

    removeTask(id: string) {
      this.tasks = this.tasks.filter((t) => t.id !== id)
    },

    /** 创建任务：本地立即 upsert + 返回完整 DTO（含预置 user 事件） */
    async createTask(projectPath: string, prompt: string): Promise<Task> {
      const dto = await api.createTask(projectPath, prompt)
      const task = adaptTask(dto)
      this.upsertTask(task)
      return task
    },

    async cancelTask(id: string) {
      await api.cancelTask(id)
    },

    async deleteTask(id: string) {
      await api.deleteTask(id)
      this.removeTask(id)
    },

    async refreshTask(id: string) {
      const task = await api.getTask(id)
      this.upsertTask(task)
      return task
    },

    /** 本地状态微调（审批提交后的乐观更新） */
    patchStatus(id: string, status: TaskStatus) {
      const t = this.byId(id)
      if (t) {
        t.status = status
        if (isTerminalStatus(status)) t.decision = undefined
      }
    },
    clearDecision(id: string) {
      const t = this.byId(id)
      if (t) t.decision = undefined
    },
  },
})

/** Store 实例类型（dispatcher 等消费方引用） */
export type TaskStore = ReturnType<typeof useTaskStore>
