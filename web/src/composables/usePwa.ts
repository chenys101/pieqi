// usePwa：安装提示 / standalone 检测（方案 §38，D5 改为情境化）
//
// ⚠️ **必须是模块级单例**，`beforeinstallprompt` 是**一次性**事件，且可能早于
// Vue 挂载就派发（浏览器判定"可安装"后立即发）。若在组件里 `usePwa()` 时才注册监听，
// 组件挂载晚于事件 → 永远收不到，提示永远不出现 —— 这正是 D5 记录的
// "那个按钮时而出现时而消失"的一部分成因：每次调用都新建一份 ref 和监听器，
// 谁先挂载谁才有机会截住 deferredPrompt。
//
// 因此这里把 ref 与监听器提到模块作用域：整个应用只注册一次，任何组件读到的
// 都是同一份状态（与 useTheme 的"偏好/生效值分离"同理：状态不该属于某个组件实例）。

import { computed, ref } from 'vue'

interface BeforeInstallPromptEvent extends Event {
  prompt(): Promise<void>
  userChoice: Promise<{ outcome: 'accepted' | 'dismissed' }>
}

const canInstall = ref(false)
const isStandalone = ref(false)
/** 「暂不」：本次会话内不再提示。用 sessionStorage 而非 localStorage ——
 *  安装提示是一次性动作的**邀约**，不是偏好；拒绝一次不该等于永久放弃。 */
const dismissed = ref(false)
const DISMISS_KEY = 'pieqi.pwa.install-dismissed'

let deferredPrompt: BeforeInstallPromptEvent | null = null

if (typeof window !== 'undefined') {
  isStandalone.value =
    window.matchMedia('(display-mode: standalone)').matches ||
    // iOS Safari
    (navigator as unknown as { standalone?: boolean }).standalone === true

  try {
    dismissed.value = sessionStorage.getItem(DISMISS_KEY) === 'true'
  } catch {
    // 存储被禁：仅在本次内存里生效
  }

  window.addEventListener('beforeinstallprompt', (e) => {
    e.preventDefault()
    deferredPrompt = e as BeforeInstallPromptEvent
    canInstall.value = true
  })

  // 已经装过了（无论是从本次提示装的，还是用户自己在浏览器里装的）
  window.addEventListener('appinstalled', () => {
    deferredPrompt = null
    canInstall.value = false
  })
}

export function usePwa() {
  /** 触发安装弹窗（仅 canInstall 时有效） */
  async function promptInstall(): Promise<'accepted' | 'dismissed' | 'unavailable'> {
    if (!deferredPrompt) return 'unavailable'
    await deferredPrompt.prompt()
    const { outcome } = await deferredPrompt.userChoice
    // 浏览器规定该事件只能用一次；用完必须丢弃，否则重复调用会抛
    deferredPrompt = null
    canInstall.value = false
    return outcome
  }

  /** 「暂不」：本次会话不再出现 */
  function dismiss() {
    dismissed.value = true
    try {
      sessionStorage.setItem(DISMISS_KEY, 'true')
    } catch {
      // 存储被禁：内存里记住即可
    }
  }

  /**
   * 是否应当展示提示条。
   *
   * 三个条件缺一不可：
   *  1. `canInstall` —— 浏览器真的给了安装能力。**这就是"不出现"的默认态**：
   *     iOS Safari 从不派发 `beforeinstallprompt`，所以 iOS 上这永远不显示
   *     （改为设置页那一行静态说明，见 D5）。
   *  2. `!isStandalone` —— 已经装过的不再劝装。`canInstall` 通常已隐含这一点，
   *     但显式判断一次，避免"从主屏图标启动 + 浏览器又给了事件"的罕见组合。
   *  3. `!dismissed` —— 用户说过"暂不"。
   */
  const shouldPrompt = computed(() => canInstall.value && !isStandalone.value && !dismissed.value)

  return { canInstall, isStandalone, dismissed, shouldPrompt, promptInstall, dismiss }
}

/** 注册 Service Worker（main.ts 调用一次） */
export function registerServiceWorker(): void {
  // dev 不注册（R4 dev 守卫）：dev server 上 sw.js 是未注入版本号的占位产物，
  // 注册它 = 开发期被旧缓存坑 + 版本语义全错。SW 只属于生产构建。
  if (import.meta.env.DEV) return
  if ('serviceWorker' in navigator) {
    navigator.serviceWorker.register('/sw.js').catch(() => {
      // 注册失败：离线兜底不可用，不影响在线功能
    })
  }
}
