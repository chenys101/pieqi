// 本周概览的计算单测。
//
// 这里的风险不在算术，在**窗口边界**：差一天就是完全不同的两根柱子，
// 而边界错的时候 UI 上表现为"今天完成的任务没算进去"或"上周的混进来了"，
// 都不能靠肉眼对着界面发现。所以每个用例都锚定一个固定的 now。

import { describe, expect, it } from 'vitest'
import { computeWeeklyInsight, sparkHeight, INSIGHT_DAYS, METRICS_SINCE } from './insight'
import type { Task } from '@/types/task'

// 固定基准：2026-09-11 12:00（周四），本地时区
const BASE = new Date(2026, 8, 11, 12, 0, 0).getTime()
const DAY = 86_400_000

function t(over: Partial<Task> = {}): Task {
  return {
    id: 't1',
    title: 'x',
    prompt: 'x',
    project: 'demo',
    projectPath: 'G:/ws/demo',
    status: 'completed',
    agent: 'claude-code',
    sessionId: 's',
    createdAt: new Date(BASE).toISOString(),
    updatedAt: new Date(BASE).toISOString(),
    finishedAt: new Date(BASE).toISOString(),
    interventions: [],
    ...over,
  }
}

describe('computeWeeklyInsight', () => {
  it('窗口是 7 个自然日且末位是今天', () => {
    const r = computeWeeklyInsight([], BASE)
    expect(r.days).toHaveLength(INSIGHT_DAYS)
    expect(r.days[r.days.length - 1].isToday).toBe(true)
    expect(r.days.filter((d) => d.isToday)).toHaveLength(1)
  })

  it('今天完成的任务计入今天那一格', () => {
    const r = computeWeeklyInsight([t()], BASE)
    expect(r.completedCount).toBe(1)
    expect(r.days[INSIGHT_DAYS - 1].count).toBe(1)
  })

  it('6 天前（窗口最左边界当天）仍在窗口内', () => {
    // 边界上的关键点：取 6 天前的中午，startOfDay 后正好等于 windowStart
    const fin = new Date(BASE - 6 * DAY).toISOString()
    const r = computeWeeklyInsight([t({ finishedAt: fin })], BASE)
    expect(r.completedCount).toBe(1)
    expect(r.days[0].count).toBe(1)
    expect(r.days[0].isToday).toBe(false)
  })

  it('7 天前已完成的不计入本周', () => {
    const fin = new Date(BASE - 7 * DAY).toISOString()
    const r = computeWeeklyInsight([t({ finishedAt: fin })], BASE)
    expect(r.completedCount).toBe(0)
    expect(r.hasData).toBe(false)
  })

  it('failed / cancelled 不计入产出', () => {
    const r = computeWeeklyInsight([t({ status: 'failed' }), t({ id: 't2', status: 'cancelled' })], BASE)
    expect(r.completedCount).toBe(0)
    expect(r.hasData).toBe(false)
  })

  it('较上周差值：上周没有完成则不显示 delta（null，而不是 -N）', () => {
    // 全新安装 / 第一周：声称"较上周 -3"是把缺失写成退步
    const r = computeWeeklyInsight([t()], BASE)
    expect(r.deltaVsPrev).toBeNull()
  })

  it('较上周差值：有上周数据时给出差值', () => {
    const lastWeek = new Date(BASE - 9 * DAY).toISOString() // 落在上一个 7 天窗口
    const r = computeWeeklyInsight([t(), t({ id: 'a' }), t({ id: 'b' }), t({ id: 'prev', finishedAt: lastWeek })], BASE)
    expect(r.completedCount).toBe(3)
    expect(r.deltaVsPrev).toBe(2) // 3 本周 - 1 上周
  })

  it('平均耗时按分钟取整，且缺 startedAt 的样本被剔除而不是按 0 平均', () => {
    const withDur = t({ id: 'a', startedAt: new Date(BASE - 20 * 60_000).toISOString() })
    // 若把下面这条按 0 参与平均，(20+0)/2 = 10 分钟；正确应是 20
    const noDur = t({ id: 'b', startedAt: undefined })
    const r = computeWeeklyInsight([withDur, noDur], BASE)
    expect(r.avgMinutes).toBe(20)
  })

  it('没有任何有效样本时平均耗时为 null，而不是 0', () => {
    const r = computeWeeklyInsight([t({ startedAt: undefined })], BASE)
    expect(r.avgMinutes).toBeNull()
  })

  it('代码改动是累加的，且容忍老任务没有快照', () => {
    const r = computeWeeklyInsight(
      [
        t({ id: 'a', diffStat: { files: 3, additions: 40, deletions: 5, capturedAt: '' } }),
        t({ id: 'b', diffStat: { files: 2, additions: 10, deletions: 1, capturedAt: '' } }),
        t({ id: 'legacy' }), // 本 feature 上线前完成的任务：无 diffStat
      ],
      BASE,
    )
    expect(r.diff).toEqual({ files: 5, additions: 50, deletions: 6 })
  })

  it('hasData：只有改动但没有完成数时也算有数据', () => {
    const r = computeWeeklyInsight([t({ diffStat: { files: 1, additions: 2, deletions: 0, capturedAt: '' } })], BASE)
    expect(r.hasData).toBe(true)
  })
})

describe('sparkHeight', () => {
  it('全 0 时返回固定的极矮高度，不产生 NaN', () => {
    expect(sparkHeight(0, 0)).toBe(3)
    expect(Number.isNaN(sparkHeight(0, 0))).toBe(false)
  })

  it('有数据时按比例，且单根柱子也有可见高度', () => {
    expect(sparkHeight(4, 4)).toBe(100)
    expect(sparkHeight(2, 4)).toBe(50)
    // count 很小但非零时不能塌缩成 0 高度（看不见等于没有）
    expect(sparkHeight(1, 100)).toBe(12)
  })
})

describe('一次通过率（R6）', () => {
  // 用例日期全部从 METRICS_SINCE 派生 —— 常量随发布日更新时用例不脆
  const SINCE_T = new Date(`${METRICS_SINCE}T12:00:00`).getTime()
  const FIN = new Date(SINCE_T + 2 * DAY).toISOString() // 起点后第 2 天完成（落在窗口内的前提由 now 保证）

  function intervention(over: Record<string, unknown> = {}) {
    return { id: 'i1', taskId: 't1', kind: 'decision', choice: 'approve', source: 'http', createdAt: FIN, ...over }
  }

  it('与手工复算一致：3 个 completed、1 个被干预 → 67%（AC-R6-02）', () => {
    const tasks = [
      t({ id: 'a', createdAt: new Date(SINCE_T).toISOString(), finishedAt: FIN, interventions: [] }),
      t({ id: 'b', createdAt: new Date(SINCE_T).toISOString(), finishedAt: FIN, interventions: [intervention()] }),
      t({ id: 'c', createdAt: new Date(SINCE_T).toISOString(), finishedAt: FIN, interventions: [] }),
    ]
    // now = 完成日之后 → 三者都在 7 天窗口内
    const r = computeWeeklyInsight(tasks, new Date(SINCE_T + 3 * DAY).getTime())
    expect(r.completedCount).toBe(3)
    expect(r.oneShotRate).toBe(67)
  })

  it('起点前创建的 completed 不进分子也不进分母（AC-R6-03）', () => {
    const tasks = [
      // 起点前创建、被干预 1 次 —— 若混入分母，4 取 2 = 50%；正确答案 2 取 2 = 100%
      t({ id: 'old', createdAt: new Date(SINCE_T - DAY).toISOString(), finishedAt: FIN, interventions: [intervention()] }),
      t({ id: 'a', createdAt: new Date(SINCE_T).toISOString(), finishedAt: FIN, interventions: [] }),
      t({ id: 'b', createdAt: new Date(SINCE_T).toISOString(), finishedAt: FIN, interventions: [] }),
    ]
    const r = computeWeeklyInsight(tasks, new Date(SINCE_T + 3 * DAY).getTime())
    expect(r.oneShotRate).toBe(100)
  })

  it('起点后没有 completed 样本 → null（展示 "—"，不编 0% 或 100%）', () => {
    // 只有起点前的任务（本轮其余用例的默认情形）
    const r = computeWeeklyInsight([t({})], BASE)
    expect(r.oneShotRate).toBeNull()
    // 分母为 0 的另一种形态：起点后只有 failed
    const r2 = computeWeeklyInsight(
      [t({ id: 'f', status: 'failed', createdAt: new Date(SINCE_T).toISOString() })],
      new Date(SINCE_T + 3 * DAY).getTime(),
    )
    expect(r2.oneShotRate).toBeNull()
  })
})
