// 本周概览的指标计算（SPEC §6.2）。
//
// **为什么这些是"趋势与质量"而不是"状态统计"**：概览回答"这周干得怎么样"，
// 不回答"现在各状态有几个" —— 后者已由侧栏任务树的项目计数与状态点回答，
// 之所以把 Spec 里那些区块从仪表盘删掉，正是因为它们在回答后者。
//
// 三条口径上的决定（都写在注释里，因为它们不是显然的）：
//  1. **窗口按自然日，不按 now-7×24h**。"过去 7 天"在 UI 上是一根 7 格的柱状图，
//     每一格得对应一个自然日；滚动窗口会让"今天这格"含义含糊。
//  2. **只有 completed 计入**。failed/cancelled 不是产出。
//  3. **缺 startedAt 的任务从平均耗时里剔除，而不是按 0 参与平均** ——
//     一个几十分钟的样本和一批缺失样本混算，平均值会被稀释到没有意义。

import { timestamp } from '@/utils/date'
import type { Task } from '@/types/task'

/** 窗口天数（含今天） */
export const INSIGHT_DAYS = 7

/**
 * R6 指标的数据起点（AC-R6-03/04）：Intervention 记录从 2.2.0 才开始落盘，
 * 之前创建的任务没有干预数据 —— 分母里混入它们会把一次通过率**虚高**成 100%。
 * ⚠️ 2.2.0 正式发布时必须把这里改成实际上线日（并在 S5 记录留痕）。
 */
export const METRICS_SINCE = '2026-09-12'

export interface InsightDay {
  /** 该自然日 00:00 的时间戳，作为 key */
  at: number
  label: string
  count: number
  isToday: boolean
}

export interface WeeklyInsight {
  /** 窗口内完成的任务数 */
  completedCount: number
  /** 较上一个 7 天窗口的差值；null = 没有可比的上周（如全新安装） */
  deltaVsPrev: number | null
  /** 7 根柱子，末位是今天 */
  days: InsightDay[]
  /** 平均耗时（分钟）；null = 无有效样本 */
  avgMinutes: number | null
  /**
   * 一次通过率（%）：无干预 completed ÷ completed（AC-R6-02）。
   * 口径三约束：只算 **METRICS_SINCE 之后创建** 的任务（此前无干预记录，混入即虚高）；
   * **分母为 0 → null**，展示 "—" 而不是 0%/100%（没有样本 ≠ 全军覆没）；
   * 分子分母**同一次循环计数**，与任务记录手工复算一致。
   */
  oneShotRate: number | null
  /** 累计代码改动 */
  diff: { files: number; additions: number; deletions: number }
  /** 是否有任何可展示的数据 —— 决定要不要走空态 */
  hasData: boolean
}

const DAY_MS = 86_400_000

/** 取某天 00:00 的时间戳（本地时区） */
function startOfDay(t: number): number {
  const d = new Date(t)
  d.setHours(0, 0, 0, 0)
  return d.getTime()
}

/**
 * 计算概览。`now` 可注入以便单测 —— 概览完全依赖"今天是什么时候"，
 * 不注入就只能测到"恰好在跑测试的这一天"的行为。
 */
export function computeWeeklyInsight(tasks: Task[], now: number = Date.now()): WeeklyInsight {
  const todayStart = startOfDay(now)
  // 窗口 = [今天往前 6 天, 现在]，含今天共 7 个自然日
  const windowStart = todayStart - (INSIGHT_DAYS - 1) * DAY_MS
  const prevWindowStart = windowStart - INSIGHT_DAYS * DAY_MS

  const days: InsightDay[] = []
  for (let i = 0; i < INSIGHT_DAYS; i++) {
    const at = windowStart + i * DAY_MS
    days.push({
      at,
      label: new Date(at).toLocaleDateString(undefined, { weekday: 'narrow' }),
      count: 0,
      isToday: at === todayStart,
    })
  }

  let completedCount = 0
  let prevCompletedCount = 0
  let durationsSum = 0
  let durationsN = 0
  let oneShotN = 0
  let oneShotTotal = 0
  const diff = { files: 0, additions: 0, deletions: 0 }
  const since = new Date(`${METRICS_SINCE}T00:00:00`).getTime()

  for (const t of tasks) {
    if (t.status !== 'completed' || !t.finishedAt) continue
    const fin = timestamp(t.finishedAt)
    if (fin < prevWindowStart) continue // 更早的不参与任何统计
    const isThisWeek = fin >= windowStart

    if (isThisWeek) {
      completedCount++
      // 落到哪一格
      const idx = Math.floor((startOfDay(fin) - windowStart) / DAY_MS)
      if (idx >= 0 && idx < days.length) days[idx].count++

      const started = t.startedAt ? timestamp(t.startedAt) : 0
      if (started > 0 && fin > started) {
        durationsSum += fin - started
        durationsN++
      }
      if (t.diffStat) {
        diff.files += t.diffStat.files
        diff.additions += t.diffStat.additions
        diff.deletions += t.diffStat.deletions
      }
      // 一次通过率（AC-R6-02/03/05）：分母含**被干预过**的 completed（否则"拒绝
      // 一切"能刷到 100%）；起点前创建的样本整体剔除（分子分母一起剔，不留偏样本）
      if (timestamp(t.createdAt) >= since) {
        oneShotTotal++
        if ((t.interventions?.length ?? 0) === 0) oneShotN++
      }
    } else {
      // 落在上一个窗口才是"上周"
      if (fin >= prevWindowStart) prevCompletedCount++
    }
  }

  return {
    completedCount,
    deltaVsPrev: prevCompletedCount > 0 ? completedCount - prevCompletedCount : null,
    days,
    avgMinutes: durationsN > 0 ? Math.round(durationsSum / durationsN / 60_000) : null,
    oneShotRate: oneShotTotal > 0 ? Math.round((oneShotN / oneShotTotal) * 100) : null,
    diff,
    hasData: completedCount > 0 || diff.files > 0,
  }
}

/**
 * 柱状图每根的高度百分比。
 *
 * 最大值必须至少为 1：全是 0 时若按 max=0 计算会得 0/0 → NaN 高度，
 * 表现为柱子渲染成奇怪的满高或消失。这里让 0 柱统一显示为极矮的一格（3%），
 * 用户看到的是"这周没完成"，不是"图坏了"。
 */
export function sparkHeight(count: number, max: number): number {
  const base = Math.max(1, max)
  if (count <= 0) return 3
  return Math.max(12, Math.round((count / base) * 100))
}
