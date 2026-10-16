// useResponsive：响应式断点（SPEC §6.4）。**三档，不是一个开关。**
//
//   < 768px    移动    单列；头部只留一行，变更反馈整屏、靠那一行最右的图标切换
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
    // ⚠️ `matchMedia` 可能不存在（jsdom 默认没有；老浏览器也可能缺）。
    // 早期这里直接调用，于是**任何**引入 useResponsive 的组件在 jsdom 下
    // 都会在 mounted 钩子里抛 "window.matchMedia is not a function"，
    // 而这个抛错会连带把父组件（如 SessionTimeline）的整棵子树渲染搞崩 ——
    // 表现为一堆看似无关的用例全红（2026-10-16 实测：57 条）。
    // 这里退化为"保持默认值"而不是抛错：窄屏/宽屏判定失败时，
    // 组件仍应正常渲染（默认值 = 都不匹配 = 走移动档，与本文件顶部
    // "isMobile 与 isWide 可能同时为 false" 的约定一致）。
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return () => {}

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
