// usePwa：安装提示 / standalone 检测（方案 §38）

import { ref } from 'vue'

interface BeforeInstallPromptEvent extends Event {
  prompt(): Promise<void>
  userChoice: Promise<{ outcome: 'accepted' | 'dismissed' }>
}

export function usePwa() {
  const canInstall = ref(false)
  const isStandalone = ref(false)
  let deferredPrompt: BeforeInstallPromptEvent | null = null

  if (typeof window !== 'undefined') {
    isStandalone.value =
      window.matchMedia('(display-mode: standalone)').matches ||
      // iOS Safari
      (navigator as unknown as { standalone?: boolean }).standalone === true

    window.addEventListener('beforeinstallprompt', (e) => {
      e.preventDefault()
      deferredPrompt = e as BeforeInstallPromptEvent
      canInstall.value = true
    })
  }

  /** 触发安装弹窗（仅 canInstall 时有效） */
  async function promptInstall(): Promise<'accepted' | 'dismissed' | 'unavailable'> {
    if (!deferredPrompt) return 'unavailable'
    await deferredPrompt.prompt()
    const { outcome } = await deferredPrompt.userChoice
    deferredPrompt = null
    canInstall.value = false
    return outcome
  }

  return { canInstall, isStandalone, promptInstall }
}

/** 注册 Service Worker（main.ts 调用一次） */
export function registerServiceWorker(): void {
  if ('serviceWorker' in navigator) {
    navigator.serviceWorker.register('/sw.js').catch(() => {
      // 注册失败：离线兜底不可用，不影响在线功能
    })
  }
}
