// FeedbackBundle 共享 store：Timeline 分组头与 FeedbackPanel 的**同一份**数据。
//
// 为什么 store 化而不是两处各拉：AC-R3-02 要求「分组头的文件数 == 点开 diff 的
// 文件数」。同一个 DTO 当然同源，但两处各自 fetch 就有两个刷新节奏 —— 面板重拉
// （回退后）而 Timeline 不动时，同一屏出现两个版本的 turns。
// 数据没有第二个来源，就不该有第二个持有者。
//
// 拉取时机由消费方自决：Timeline 常驻（挂载即拉 + 新 user_message 增量重拉）；
// FeedbackPanel 沿用原 shouldLoad 闸门（dock 收起态也要拉 —— 贴边条计数）。
import { defineStore } from 'pinia'
import { getFeedback } from '@/services/api/feedback'
import type { FeedbackBundleDto } from '@/types/api'

export const useFeedbackBundleStore = defineStore('feedbackBundle', {
  state: () => ({
    bundles: {} as Record<string, FeedbackBundleDto>,
    /** 进行中的请求（去重：Timeline 增量重拉与面板 refresh 同时发生只发一次） */
    inflight: {} as Record<string, Promise<void>>,
  }),

  getters: {
    bundle: (state) => (taskId: string): FeedbackBundleDto | null => state.bundles[taskId] ?? null,
  },

  actions: {
    /** force=false 时已有数据则跳过（初载）；force=true 无条件重拉（回退后/增量） */
    async load(taskId: string, force = false): Promise<void> {
      if (!taskId) return
      if (!force && this.bundles[taskId]) return
      const running = this.inflight[taskId]
      if (running) return running
      const p = getFeedback(taskId)
        .then((bundle) => {
          this.bundles[taskId] = bundle
        })
        .catch(() => {
          // 404 / 网络失败都会走到这里。**刻意吞掉**：
          //   · 加载失败不是"本任务没有反馈"的合法结论，所以**不写空 bundle**
          //     （否则面板会显示"没有变更"，把一次故障说成"改动了 0 个文件"）；
          //   · 但也不上抛 —— 这个 store 的 load 是 fire-and-forget（Timeline 挂载即调），
          //     上抛会变成未处理的 Promise rejection，把控制台的错误断言整体染红，
          //     掩盖真正的问题。
          // 调用方要感知失败，就 await 它并自行 catch（Timeline 的增量重拉已经这么做）。
        })
        .finally(() => {
          delete this.inflight[taskId]
        })
      this.inflight[taskId] = p
      return p
    },
  },
})
