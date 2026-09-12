// FeedbackPanel Store：变更反馈右栏的收起态 + Turn 选中（SPEC §5.2.1 / R3）。
//
// **为什么放在 store 而不是 SessionPage 的本地 ref**：SPEC 明确要求
// 「收起态不丢状态 —— 切页、切视口都保持」。本地 ref 会在路由切走再回来时
// 重置成默认值，用户每次进详情都被"重置成展开"，等于没有默认收起。
//
// `activeTurn`（AC-R3-06）：Timeline 分组「查看本轮变更」→ 面板滚动定位到
// 对应 TurnCard。同样放 store —— 切页/切视口回来后选中不丢，与 collapsed 同责。
//
// 注意这里只存「看不看得到 / 选中谁」，不存「有什么」——
// 反馈数据由 feedbackBundle store 现场从 /feedback 派生（ADR-0001：后端不存第二份聚合）。
import { defineStore } from 'pinia'

export const useFeedbackPanelStore = defineStore('feedbackPanel', {
  state: () => ({
    /** 宽屏右栏是否收起为 46px 贴边条 */
    collapsed: true,
    /** Timeline 选中的 Turn（null = 无选中）。切页/切视口保持 */
    activeTurn: null as number | null,
  }),

  actions: {
    expand() {
      this.collapsed = false
    },
    collapse() {
      this.collapsed = true
    },
    /** Timeline「查看本轮变更」入口：展开面板并锁定 Turn */
    focusTurn(turn: number) {
      this.activeTurn = turn
      this.collapsed = false
    },
    clearActiveTurn() {
      this.activeTurn = null
    },
  },
})
