// Approval Store（方案 §10.3）：待审批集中管理（手机免进会话直接审批）

import { defineStore } from 'pinia'
import type { ApprovalRequest } from '@/types/approval'
import { useTaskStore } from './task'
import * as tasksApi from '@/services/api/tasks'
import { useSessionStore } from './session'

export const useApprovalStore = defineStore('approval', {
  getters: {
    /** 全部待决策：从 Task Store 实时派生 */
    pending(): ApprovalRequest[] {
      const taskStore = useTaskStore()
      return taskStore.tasks
        .filter((t) => t.status === 'waiting_input' && t.decision)
        .map((t) => t.decision!)
        .sort((a, b) => new Date(a.createdAt).getTime() - new Date(b.createdAt).getTime())
    },
  },

  actions: {
    /** 批准（approve）。后端无 allow_always，方案 §3.1 不扩协议 */
    async approve(taskId: string) {
      const taskStore = useTaskStore()
      const sessionStore = useSessionStore()
      const task = taskStore.byId(taskId)
      await tasksApi.intervene(taskId, {
        kind: 'decision',
        decisionId: task?.decision?.id,
        choice: 'approve',
      })
      // 乐观更新：横幅立即消失，等 WS task_updated 校准
      taskStore.clearDecision(taskId)
      taskStore.patchStatus(taskId, 'running')
      sessionStore.patchSessionStatus(taskId, 'running')
    },

    /** 拒绝（deny） */
    async deny(taskId: string) {
      const taskStore = useTaskStore()
      const sessionStore = useSessionStore()
      const task = taskStore.byId(taskId)
      await tasksApi.intervene(taskId, {
        kind: 'decision',
        decisionId: task?.decision?.id,
        choice: 'deny',
      })
      taskStore.clearDecision(taskId)
      taskStore.patchStatus(taskId, 'running')
      sessionStore.patchSessionStatus(taskId, 'running')
    },
  },
})
