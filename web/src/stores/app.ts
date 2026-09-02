// App Store：全局 UI 状态 / 鉴权状态 / 补全数据源（boot 一次性拉取）

import { defineStore } from 'pinia'
import type { AuthStatus } from '@/services/api/auth'
import { getAuthStatus } from '@/services/api/auth'
import type { CompletionSources } from '@/services/api/skills'
import { loadCompletionSources } from '@/services/api/skills'

export interface AppBanner {
  message: string
  tone: 'warning' | 'error' | 'info'
}

export const useAppStore = defineStore('app', {
  state: () => ({
    booted: false,
    /** 鉴权状态（boot 轮询，方案 §35） */
    auth: null as AuthStatus | null,
    /** 全局横幅：debug 模式 / 未绑定 / 401 */
    banner: null as AppBanner | null,
    /** 斜杠补全数据源（commands + skills） */
    completions: { commands: [], skills: [] } as CompletionSources,
    /** 移动端侧栏抽屉 */
    mobileNavOpen: false,
    /** 全局新建任务弹窗 */
    newTaskOpen: false,
  }),

  actions: {
    setBanner(message: string, tone: AppBanner['tone']) {
      this.banner = { message, tone }
    },
    clearBanner() {
      this.banner = null
    },

    /** boot：鉴权状态 + 补全源（失败静默，不阻塞主流程，与 V1 一致） */
    async loadBootContext() {
      try {
        const st = await getAuthStatus()
        this.auth = st
        if (st.debug) {
          this.setBanner('免鉴权调试模式已开启 — 所有访问放行，仅限本地开发', 'info')
        } else if (!st.bound) {
          this.setBanner('系统尚未绑定飞书管理员账号 — 请在内网访问 /api/auth/bind 完成', 'warning')
        }
      } catch {
        // 网络失败：不设横幅，由连接状态指示器兜底
      }
      try {
        this.completions = await loadCompletionSources()
      } catch {
        // 补全源失败：输入框仍可用，仅无 / 提示
      }
    },
  },
})
