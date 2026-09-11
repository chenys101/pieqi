// FeedbackPanel Store：变更反馈右栏的收起态（SPEC §5.2.1）。
//
// **为什么放在 store 而不是 SessionPage 的本地 ref**：SPEC 明确要求
// 「收起态不丢状态 —— 切页、切视口都保持」。本地 ref 会在路由切走再回来时
// 重置成默认值，用户每次进详情都被"重置成展开"，等于没有默认收起。
//
// 默认 `true`（收起）。理由见 SPEC §5.2.1：420px 是**永久开销**，
// 而任务详情的主线是读时间线（我说的 → Agent 做的），
// 反馈是按需查看的辅助面 —— 默认展开等于让辅助面先扣掉时间线 30% 的宽度。
//
// 注意这里只存「看不看得到」，不存「有什么」——
// 反馈数据仍然由 FeedbackPanel 现场从 /feedback 派生（ADR-0001：后端不存第二份聚合）。
import { defineStore } from 'pinia'

export const useFeedbackPanelStore = defineStore('feedbackPanel', {
  state: () => ({
    /** 宽屏右栏是否收起为 46px 贴边条 */
    collapsed: true,
  }),

  actions: {
    expand() {
      this.collapsed = false
    },
    collapse() {
      this.collapsed = true
    },
  },
})
