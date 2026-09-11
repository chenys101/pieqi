import { describe, expect, it } from 'vitest'
import { riskOf, riskWeight } from '@/types/approval'

describe('riskOf', () => {
  it('已知等级原样返回', () => {
    expect(riskOf('L0')).toBe('L0')
    expect(riskOf('L1')).toBe('L1')
    expect(riskOf('L2')).toBe('L2')
    expect(riskOf('L3')).toBe('L3')
  })

  // 这条是本文件真正要守的：后端未打标（旧持久化任务 / choice 决策）时，
  // 前端若按 L0 渲染，用户会看到一张"看起来无害"的卡。
  it('缺省 / 未知一律兜底为 L2（不往低风险倒）', () => {
    for (const v of [undefined, null, '', 'L9', 'l0']) {
      expect(riskOf(v)).toBe('L2')
    }
  })
})

describe('riskWeight', () => {
  it('权重随风险递增，L3 最重', () => {
    expect(riskWeight('L3')).toBeGreaterThan(riskWeight('L2'))
    expect(riskWeight('L2')).toBeGreaterThan(riskWeight('L1'))
    expect(riskWeight('L1')).toBeGreaterThan(riskWeight('L0'))
  })

  it('未打标按 L2 参与排序（不会被排到最前面）', () => {
    expect(riskWeight(undefined)).toBe(riskWeight('L2'))
    expect(riskWeight(undefined)).toBeGreaterThan(riskWeight('L1'))
    expect(riskWeight(undefined)).toBeLessThan(riskWeight('L3'))
  })

  it('待审批排序：风险降序，同风险按等待时间升序', () => {
    const list = [
      { id: 'a', risk: 'L1', createdAt: '2026-09-11T10:00:00Z' },
      { id: 'b', risk: 'L3', createdAt: '2026-09-11T12:00:00Z' },
      { id: 'c', risk: 'L2', createdAt: '2026-09-11T09:00:00Z' },
      { id: 'd', risk: 'L3', createdAt: '2026-09-11T08:00:00Z' },
    ]
    const sorted = [...list].sort((x, y) => {
      const byRisk = riskWeight(y.risk) - riskWeight(x.risk)
      if (byRisk !== 0) return byRisk
      return x.createdAt < y.createdAt ? -1 : x.createdAt > y.createdAt ? 1 : 0
    })
    // 两条 L3 在最前，且等得最久的（08:00）排在 12:00 之前
    expect(sorted.map((i) => i.id)).toEqual(['d', 'b', 'c', 'a'])
  })
})
