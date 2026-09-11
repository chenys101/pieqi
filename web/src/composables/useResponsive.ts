// useResponsive：响应式断点（SPEC §6.4）。**三档，不是一个开关。**
//
//   < 768px    移动    单列；变更反馈整屏，靠顶部分段切换
//   768–1279   平板    变更反馈走 Drawer（420px 覆盖式）
//   ≥ 1280px   宽屏    变更反馈为常驻右栏（420px ⇄ 46px 贴边条）
//
// 为什么 1280 要单独一档：常驻右栏要同时容下「240px 侧栏 + 时间线 + 420px 反馈」，
// 1280 以下给不出这三样 —— 时间线会被压到每行都断句，比收起还难读。
// 所以「反馈怎么摆」在 1280 处拐弯，而不是在 768。
//
// 注意 isMobile 与 isWide **可能同时为 false**（平板档），不要写成二选一。

import { ref, onMounted, onUnmounted } from 'vue'
import type { Ref } from 'vue'

const MOBILE_QUERY = '(max-width: 768px)'
const WIDE_QUERY = '(min-width: 1280px)'

export function useResponsive() {
  const isMobile = ref(false)
  const isWide = ref(false)

  /** 绑定一条媒体查询，返回解绑函数 */
  function bind(query: string, target: Ref<boolean>): () => void {
    const mq = window.matchMedia(query)
    const apply = (e: MediaQueryList | MediaQueryListEvent) => {
      target.value = e.matches
    }
    // 立即取一次初值：不能只等 change —— 组件挂载时查询可能**已经**是匹配的，
    // 那种情况下浏览器不会再派发一次 change，于是永远停在 false。
    apply(mq)
    if (typeof mq.addEventListener === 'function') mq.addEventListener('change', apply)
    // 老 iOS Safari (≤12) 只有 addListener
    else if (typeof mq.addListener === 'function') mq.addListener(apply)
    return () => {
      if (typeof mq.removeEventListener === 'function') mq.removeEventListener('change', apply)
      else if (typeof mq.removeListener === 'function') mq.removeListener(apply)
    }
  }

  let unbind: Array<() => void> = []

  onMounted(() => {
    unbind = [bind(MOBILE_QUERY, isMobile), bind(WIDE_QUERY, isWide)]
  })
  onUnmounted(() => {
    unbind.forEach((f) => f())
    unbind = []
  })

  return { isMobile, isWide }
}
