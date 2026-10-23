// Approval Store（方案 §10.3）：待审批集中管理（手机免进会话直接审批）

import { defineStore } from 'pinia'
import type { ApprovalChoice, ApprovalRequest } from '@/types/approval'
import { useTaskStore } from './task'
import * as tasksApi from '@/services/api/tasks'
import { useSessionStore } from './session'

/**
 * 判定一次审批失败是否属于"这张卡已经处理过了"。
 *
 * 为什么前端要认这个：后端在 2026-10-23 之后把"已批过/已拒绝/已超时"与"id 根本不存在"
 * 分开回报（前者 409 + 「已经处理过了」，后者 409 + no pending permission）。
 * 这里只做**展示层**的区分，不做补偿决策 —— 补偿动作（approve_same）由点击意图决定。
 */
export function isAlreadySettled(err: unknown): boolean {
  const msg = err instanceof Error ? err.message : String(err ?? '')
  return msg.includes('已经处理过') || msg.includes('already resolved')
}

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
     *
     * ★ 决策 id **必须在提交前从 store 现取**，不能用调用方传进来的那份快照。
     * 2026-10-23 的 no pending 刷屏就是这里：卡片组件拿着渲染时刻的 props.approval.id，
     * 而期间 agent 已经发起了下一条请求（CurrentDecision 换了 id）。提交旧 id 的后果是
     * 后端 pending 表里查无此物 —— 用户看到的却是"我明明点了允许"。
     * 现取仍为空（卡其实已经消失）时直接返回，什么都不发。
     *
     * 失败时**先释放卡片的 loading**（这里的 finally 已经在做），并把错误原文抛给调用方：
     * 之前刷新任务失败会被静默吞掉，用户面对一张没反应的卡无从判断发生了什么。
     */
    async resolve(taskId: string, choice: ApprovalChoice) {
      const taskStore = useTaskStore()
      const sessionStore = useSessionStore()
      // 提交前重新读一次服务端：本页 / 本机其它端刚才可能已经处理过这张卡，
      // 甚至 agent 已经推进到下一条请求。拿最新的一条比拿渲染时刻的那条准。
      let fresh: { id: string } | undefined
      try {
        const task = await taskStore.refreshTask(taskId)
        fresh = task.decision
      } catch {
        // 刷新失败不阻断：退回到本地已有的那条（可能是旧的，但至少能试一次）。
        fresh = taskStore.byId(taskId)?.decision
      }
      const decisionId = fresh?.id
      if (!decisionId) {
        // 卡已经不在了（别处已批/任务已推进）：不发请求，也不报错 —— 清掉本地残态即可。
        taskStore.clearDecision(taskId)
        return
      }
      try {
        await tasksApi.intervene(taskId, { kind: 'decision', decisionId, choice })
      } catch (err) {
        // 已结算的卡片：用户的意图仍然是"允许这条"，只是它的审批请求已经没有载体。
        // 改投 approve_same：后端据此把这个工具类别记成本会话免审，下一个同类请求
        // 直接通过（否则那条命令永远没机会跑，而用户以为已经批准了）。
        if (isAlreadySettled(err) && (choice === 'approve' || choice === 'approve_session')) {
          await tasksApi.intervene(taskId, { kind: 'decision', decisionId, choice: 'approve_same' })
        } else {
          throw err
        }
      }
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
