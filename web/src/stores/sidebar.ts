// 桌面侧栏开合态（AppSidebar 收起/展开）。独立 store，避免和 taskTree 折叠混在一起。
import { defineStore } from 'pinia'

export const useSidebarStore = defineStore('sidebar', {
  state: () => ({
    collapsed: false,
  }),
  actions: {
    collapse() {
      this.collapsed = true
    },
    expand() {
      this.collapsed = false
    },
    toggle() {
      this.collapsed = !this.collapsed
    },
  },
})
