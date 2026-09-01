// useResponsive：桌面 / 移动断点（Tailwind md = 768px）

import { ref, onMounted, onUnmounted } from 'vue'

const QUERY = '(max-width: 768px)'

export function useResponsive() {
  const isMobile = ref(false)
  let mq: MediaQueryList | null = null

  const update = (e: MediaQueryList | MediaQueryListEvent) => {
    isMobile.value = e.matches
  }

  onMounted(() => {
    mq = window.matchMedia(QUERY)
    isMobile.value = mq.matches
    if (typeof mq.addEventListener === 'function') mq.addEventListener('change', update)
    // 老 iOS Safari (≤12) 只有 addListener
    else if (typeof mq.addListener === 'function') mq.addListener(update)
  })
  onUnmounted(() => {
    if (!mq) return
    if (typeof mq.removeEventListener === 'function') mq.removeEventListener('change', update)
    else if (typeof mq.removeListener === 'function') mq.removeListener(update)
  })

  return { isMobile }
}
