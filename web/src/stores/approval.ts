// Approval Store（方案 §10.3）：待审批集中管理（手机免进会话直接审批）

import { defineStore } from 'pinia'
import type { ApprovalChoice, ApprovalRequest } from '@/types/approval'
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
    /**
     * 送一个决策并乐观收尾（横幅立即消失，等 WS task_updated 校准）。
     *
     * 三个动作共用同一段后续处理：这里的重复不是冗余，是"批完一定同价地清态"，
     * 分开写三份才会出现某条路径忘了 patchStatus 那种 bug。
     */
    async resolve(taskId: string, choice: ApprovalChoice) {
      const taskStore = useTaskStore()
      const sessionStore = useSessionStore()
      const task = taskStore.byId(taskId)
      await tasksApi.intervene(taskId, {
        kind: 'decision',
        decisionId: task?.decision?.id,
        choice,
      })
      taskStore.clearDecision(taskId)
      taskStore.patchStatus(taskId, 'running')
      sessionStore.patchSessionStatus(taskId, 'running')
    },

    /** 批准这一次（approve） */
    async approve(taskId: string) {
      await this.resolve(taskId, 'approve')
    },

    /**
     * 批准 + 本会话内同类操作免审（approve_session）。
     *
     * 不是 ACP 的 allow_always：后端只在本任务进程存活期内按 ToolKind 放行，
     * 不落盘、不跨任务、不往 agent 配置里写权限 —— L2/L3 的硬边界仍然由 pieqi 守住。
     * 可用性由后端在 Decision.options 里声明，前端不自己判路径。
     */
    async approveSession(taskId: string) {
      await this.resolve(taskId, 'approve_session')
    },

    /** 拒绝（deny） */
    async deny(taskId: string) {
      await this.resolve(taskId, 'deny')
    },
  },
})
