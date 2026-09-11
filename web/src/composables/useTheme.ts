// 主题：**偏好**（三态）与**生效值**分离。
//
// 为什么要分离：偏好可以是「跟随系统」，而 `data-theme` 必须是具体的一种
// （样式表按它取 token）。二态开关无法表达"我没选" —— 系统主题变化时无从重算。
//
// 存 localStorage 而非后端：主题是**设备级**偏好 —— 同一用户在手机和电脑上
// 可能想要不同（白天桌面浅色、手机深色），跨设备同步是反需求。
//
// 首屏不闪由 index.html 的内联脚本负责（在样式表之前同步设置 data-theme）。
// 那个脚本与本文件**共用同一个 key 与同一套解析规则** —— 改一处必须改两处。

import { ref, computed } from 'vue'

export type ThemePref = 'system' | 'light' | 'dark'
export type ResolvedTheme = 'light' | 'dark'

/** 与 index.html 内联脚本共用；改名要同步改那边 */
const STORAGE_KEY = 'pieqi.theme.pref'

/** 移动端浏览器地址栏配色，与 :root / [data-theme=dark] 的 canvas 一致 */
const THEME_COLOR: Record<ResolvedTheme, string> = {
  light: '#F6F7F9',
  dark: '#0C0D11',
}

const mql: MediaQueryList | null =
  typeof window !== 'undefined' && window.matchMedia
    ? window.matchMedia('(prefers-color-scheme: dark)')
    : null

function readStoredPref(): ThemePref {
  try {
    const v = localStorage.getItem(STORAGE_KEY)
    if (v === 'light' || v === 'dark' || v === 'system') return v
  } catch {
    // 隐私模式 / 存储被禁：当作没设过，用默认
  }
  return 'system'
}

/** 系统主题的响应式镜像。
    ⚠️ 不能直接读 `mql.matches` —— MediaQueryList 不是响应式对象，
    computed 不会因它变化而失效，于是「跟随系统」会永远停在首次解析的值上
    （真踩过：系统切到深色，界面纹丝不动，而 pref 明明还是 system）。 */
const systemDark = ref(mql ? mql.matches : false)

/** 偏好 → 生效值。system 时读系统主题镜像。 */
export function resolveTheme(pref: ThemePref): ResolvedTheme {
  if (pref === 'system') return systemDark.value ? 'dark' : 'light'
  return pref
}

/** module-level 单例：多个组件共享同一份状态，不各自持有一份 */
const pref = ref<ThemePref>(readStoredPref())
const resolved = computed<ResolvedTheme>(() => resolveTheme(pref.value))

function apply() {
  const theme = resolved.value
  const root = document.documentElement
  root.setAttribute('data-theme', theme)
  root.setAttribute('data-theme-pref', pref.value)
  // 地址栏配色跟着走，否则移动端会出现一条与页面脱节的色带
  const meta = document.querySelector('meta[name="theme-color"]')
  if (meta) meta.setAttribute('content', THEME_COLOR[theme])
}

/** 设置偏好：立即生效 + 持久化。持久化失败不影响本次生效。 */
export function setThemePref(next: ThemePref) {
  pref.value = next
  try {
    localStorage.setItem(STORAGE_KEY, next)
  } catch {
    // 存不下就只在本次会话生效
  }
  apply()
}

let listenerBound = false

/** 幂等：多处调用只装一次监听。main.ts 调用一次即可。 */
export function initTheme() {
  apply()
  if (listenerBound || !mql) return
  listenerBound = true
  const onSystemChange = () => {
    // 先更新镜像（这样 resolved 这个 computed 才会失效），再决定要不要应用
    systemDark.value = mql ? mql.matches : false
    // 只在偏好为 system 时重算 —— 否则用户显式选的浅/深色会被系统主题顶掉
    if (pref.value === 'system') apply()
  }
  if (mql.addEventListener) mql.addEventListener('change', onSystemChange)
  else if (mql.addListener) mql.addListener(onSystemChange)
}

export function useTheme() {
  return { pref, resolved, setPref: setThemePref }
}
