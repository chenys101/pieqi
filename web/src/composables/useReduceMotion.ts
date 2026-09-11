// 减弱动效：设置 → 外观与偏好里的开关。
//
// 与主题同构（偏好存 localStorage、写 <html> 属性、首屏由内联脚本兜底），
// 但**不合并进 useTheme** —— 主题决定配色，动效决定时序，两者独立变化
// （用户可能想要深色 + 正常动效）。
//
// 实际降速规则在 styles/index.css：本文件只负责把偏好落到
// `html[data-reduce-motion="true"]`。系统层面的 prefers-reduced-motion
// 由 CSS 媒体查询独立处理，即使用户没开这个开关也生效。

import { ref } from 'vue'

/** 与 index.html 内联脚本共用；改名要同步改那边 */
const STORAGE_KEY = 'pieqi.reduce-motion'

function readStored(): boolean {
  try {
    return localStorage.getItem(STORAGE_KEY) === 'true'
  } catch {
    // 存储被禁：按未开启处理
    return false
  }
}

/** module-level 单例：多处调用共享同一份状态 */
const reduce = ref(readStored())

function apply() {
  const root = document.documentElement
  // 关掉时移除属性而不是设成 "false"：让 CSS 只有一条命中路径，
  // 也保证系统媒体查询那条不被一个"显式 false"意外覆盖
  if (reduce.value) root.setAttribute('data-reduce-motion', 'true')
  else root.removeAttribute('data-reduce-motion')
}

export function setReduceMotion(next: boolean) {
  reduce.value = next
  try {
    localStorage.setItem(STORAGE_KEY, String(next))
  } catch {
    // 存不下就只在本次会话生效
  }
  apply()
}

/** 幂等。main.ts 调用一次。 */
export function initReduceMotion() {
  apply()
}

export function useReduceMotion() {
  return { reduce, set: setReduceMotion }
}
