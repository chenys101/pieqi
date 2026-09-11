// TaskTree Store：任务树的折叠状态。
//
// **一份状态，两个渲染容器**（侧栏树 / 移动端任务浏览器页）。
// 折叠态放在 store 而不是各组件自己的 ref，是因为 SPEC §6.1.1 明确要求
// 「侧栏展开的项目，切到移动端任务浏览器页仍是展开的，反之亦然」——
// 两处各存一份，用户就得在两处分别重新展开同一批项目。
//
// 后端数据源仍然是 taskStore.groupsByProject（唯一事实来源）；
// 这里只存"看不看得到"，不存"有什么"。
import { defineStore } from 'pinia'

export const useTaskTreeStore = defineStore('taskTree', {
  state: () => ({
    /**
     * 「任务列表」分组头是否展开。**初始展开** —— 用户要的是"下面直接看到项目"，
     * 而不是先点一下才知道有东西。
     */
    groupOpen: true,
    /**
     * 项目 key → 是否展开。**默认收起**：收起态树高恒为「项目数」条，
     * 不随任务数膨胀。侧栏是常驻的，高度预算固定，树的复杂度必须是
     * O(项目数) 而非 O(任务数)。
     */
    openProjects: {} as Record<string, boolean>,
  }),

  getters: {
    isProjectOpen(state) {
      return (key: string): boolean => state.openProjects[key] ?? false
    },
  },

  actions: {
    toggleGroup() {
      this.groupOpen = !this.groupOpen
    },
    toggleProject(key: string) {
      this.openProjects[key] = !this.isProjectOpen(key)
    },
    setProjectOpen(key: string, open: boolean) {
      this.openProjects[key] = open
    },
    /** 一次性展开/收起全部（移动端「展开全部」） */
    setAllProjects(keys: string[], open: boolean) {
      for (const k of keys) this.openProjects[k] = open
    },
  },
})
