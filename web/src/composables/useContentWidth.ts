// 正文列宽度：**设备级偏好**，存 localStorage。
//
// 为什么要有这个：宽屏（≥1280px）下主区正文曾经写死 `max-w-3xl`（768px）居中。
// 两个问题都不在"数字选得对不对"，而在**它是常量**：
//   1. 768px 对 27" 显示器太窄（两边各空掉一大片），对 13" 笔记本又刚好 ——
//      同一个数字不可能同时合适，这本来就该是用户的选择；
//   2. 内容是**居中**的，而侧栏（240px）在左、反馈栏（420px ⇄ 46px）在右，
//      于是正文左边界会随反馈栏收展**跳 187px**。对齐问题由 ContentColumn
//      左对齐解决，本文件只管"能拉多宽"。
//
// 存 localStorage 而非后端：与 useTheme / useReduceMotion 同判据 ——
// 屏幕宽度是**设备属性**，手机和台式机本来就该不同，跨设备同步是反需求。
//
// ⚠️ 只在宽屏档生效。平板（768–1279）居中 `max-w-3xl` 是对的（屏幕就那么大，
// 左对齐反而更差），移动端是单列满宽 —— 两者都不读这个值。

import { computed, ref } from 'vue'

/** 与 useTheme 同前缀；改名要连带清理用户的旧值 */
const STORAGE_KEY = 'pieqi.content-width'

/**
 * 正文列宽度下限（px）。
 *
 * 判据是**可读性而不是手感**：正文栏低于约 640px 时，时间线里的工具卡片
 * （`font-mono` 的命令行、diff 行）会开始频繁折行，而它们恰恰是这里最需要
 * 横向空间的内容。
 */
export const MIN_CONTENT_WIDTH = 640

/**
 * 可**手动拖到**的上限（px）。
 *
 * 2026-10-16 修正：默认值已改为"铺满可用宽度"（见 contentWidth 的语义），
 * 所以这里的上限只是**拖拽能到的边界**，不再等于默认宽度。
 * 留一个上限而不是无限：拖出可视区之外的宽度没有意义，反而让手柄跑到屏幕外。
 */
export const MAX_CONTENT_WIDTH = 2040

/** 键盘 ←/→ 的步长（px）；Shift 时 ×4，见 ContentColumn.onKeydown */
export const CONTENT_WIDTH_STEP = 16

/**
 * 用户选择的宽度。
 *
 * ★ **`null` = 还没选过 ⇒ 铺满可用宽度**（不是某个定值）。
 *
 * 这条是 2026-10-16 真机验收后改的，前一个版本的默认值是 768（= 原来的
 * `max-w-3xl`），理由是"老用户升级无跳变"。**那个理由是错的**：
 * 用户报的原始症状是"正文太窄、右边空一大片"，而 1888px 视口下
 * 时间线可用宽度 1546px、内容只占 768px ⇒ **右侧空 778px（50%）**，
 * 默认态与他投诉的那张图几乎一致 —— 等于要求用户"先自己拖一下"才算修好。
 *
 * 所以默认必须是"填满"，可读性上限交给用户按需自己收窄（手柄/键盘都支持）。
 * 不做"超宽屏自动限宽"：时间线里多是代码与工具卡片，本就不怕宽；
 * 真嫌宽的人会自己拖，而替他做这个决定反而重现了原来的毛病。
 *
 * ⚠️ 用 `null` 而不是哨兵数字（如 0 或 -1）：哨兵会混进 clamp 的取值区间，
 * 日后必然有人把它当"一个合法的窄宽度"处理。
 */
const chosen = ref<number | null>(readStored())

function readStored(): number | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (raw === null) return null // 没设过 ⇒ 铺满
    if (raw.trim() === '') return null // 脏数据当作没设过
    const n = Number(raw)
    return Number.isFinite(n) ? clampContentWidth(n) : null
  } catch {
    // 隐私模式 / 存储被禁：当作没设过
    return null
  }
}

/** 夹取到允许区间；非有限数一律退回下限（调用方负责先判 null 语义） */
export function clampContentWidth(px: number): number {
  if (!Number.isFinite(px)) return MIN_CONTENT_WIDTH
  return Math.min(MAX_CONTENT_WIDTH, Math.max(MIN_CONTENT_WIDTH, Math.round(px)))
}

/**
 * 当前可用宽度（px）。null = 未测量。
 * 由 ContentColumn 用 ResizeObserver 汇报 —— 反馈栏收展**不改变窗口宽度**，
 * 却实实在在改变了这一列的可用宽度，所以必须观察容器本身而不是 window。
 */
const available = ref<number | null>(null)

/**
 * 实际生效宽度。
 *
 * - 用户没选过（null）⇒ 铺满可用宽度（未测量时退化为 null = 不设上限，由 CSS 兜住）
 * - 用户选过 ⇒ 他的选择与可用宽度取小（永不溢出）
 *
 * ⚠️ 夹取只发生在这里，**绝不写回 `chosen`**：否则窗口缩小一次，
 * 用户的 1200 就被永久改成 800，再放大也回不去 —— 而他从没要求过 800。
 */
const effective = computed<number | null>(() => {
  const avail = available.value
  if (chosen.value === null) return avail // 铺满：等于可用宽度（null ⇒ 不限，CSS 自然铺满）
  return avail === null ? chosen.value : Math.min(chosen.value, avail)
})

/** 「已经调过一次宽度」的标记键。用它决定还要不要显示"可拖拽"提示 */
const TOUCHED_KEY = 'pieqi.content-width.touched'

function readTouched(): boolean {
  try {
    return localStorage.getItem(TOUCHED_KEY) === 'true'
  } catch {
    return false
  }
}

/** 用户是否显式调过宽度（决定要不要显示"可拖拽"提示） */
const touched = ref(readTouched())

function markTouched() {
  if (touched.value) return
  touched.value = true
  try {
    localStorage.setItem(TOUCHED_KEY, 'true')
  } catch {
    // 存不下就只在本次会话生效
  }
}

function persist() {
  try {
    if (chosen.value === null) localStorage.removeItem(STORAGE_KEY)
    else localStorage.setItem(STORAGE_KEY, String(chosen.value))
  } catch {
    // 存不下就只在本次会话生效
  }
}

/** 设置宽度（夹取 + 持久化 + 标记已调过）。拖动中每帧都会调，故不做防抖 */
export function setContentWidth(next: number) {
  const clamped = clampContentWidth(next)
  if (clamped === chosen.value) return
  chosen.value = clamped
  markTouched()
  persist()
}

/** 复位到"铺满可用宽度"（双击手柄） */
export function resetContentWidth() {
  chosen.value = null
  markTouched()
  persist()
}

/**
 * 由 ContentColumn 汇报"当前容器还给得起多少像素"。
 *
 * ⚠️ 只**记录**，不拿去改 `chosen` —— 理由见 effective 的注释。
 */
export function setContentWidthBudget(px: number | null) {
  available.value = px
}

export function useContentWidth() {
  return {
    /** 用户的显式选择（null = 没选过，即铺满） */
    width: computed(() => chosen.value),
    /** 实际生效宽度（px）；null = 不设上限，交给 CSS 铺满 */
    effectiveWidth: effective,
    /** 是否已经调过宽度（false 时才需要向用户提示"可拖拽"） */
    touched: computed(() => touched.value),
    set: setContentWidth,
    reset: resetContentWidth,
  }
}

/** 仅供测试重置模块级状态 */
export function __resetContentWidthForTest() {
  chosen.value = null
  available.value = null
  touched.value = false
}
