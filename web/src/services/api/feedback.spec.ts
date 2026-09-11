// 回归测试：后端 Go 的 **nil 切片会序列化成 `null`**，而 DTO 声明的是数组。
//
// 这条守的是一个真出过的崩溃：`/outcome` 与 `/evidence` 在没有 checks / issues /
// rewinds 时返回 `null`，于是 `outcome.checks.length` 这种在 TS 里完全合法的写法
// 在运行期抛 `Cannot read properties of null` —— **反馈面板一打开就白屏**，
// 而且只在"这份 outcome 恰好没有检查项"的任务上复现，看起来像随机故障。
//
// 归一放在适配层（而不是让每个消费方各防一次）：
// 类型说了是数组，适配层就有责任让它真的是数组。
import { describe, expect, it, vi } from 'vitest'

const { request } = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('@/services/api/client', () => ({ request, requestText: vi.fn() }))

import { getEvidence, getOutcome } from './feedback'

describe('getOutcome：null 数组合一为 []', () => {
  it('后端返回 null 时给出空数组', async () => {
    request.mockResolvedValueOnce({
      task_id: 't1',
      status: 'completed',
      changes: { files: 0, additions: 0, deletions: 0 },
      checks: null,
      issues: null,
      rewinds: null,
      generated_at: '2026-09-11T00:00:00+08:00',
    })

    const o = await getOutcome('t1')
    expect(o.checks).toEqual([])
    expect(o.issues).toEqual([])
    expect(o.rewinds).toEqual([])
    // 真正要守的是这句：模板里的 `.length` 不能再炸
    expect(o.checks.length).toBe(0)
  })

  it('真实数组原样保留（归一不误伤有数据的任务）', async () => {
    request.mockResolvedValueOnce({
      task_id: 't2',
      status: 'partial',
      changes: { files: 2, additions: 10, deletions: 3 },
      checks: [{ name: 'unit', status: 'failed' }],
      issues: ['unit 失败'],
      rewinds: [{ to_turn: 1, restored: ['a.ts'], at: '2026-09-11T00:00:00+08:00' }],
      generated_at: '2026-09-11T00:00:00+08:00',
    })

    const o = await getOutcome('t2')
    expect(o.checks).toHaveLength(1)
    expect(o.issues).toEqual(['unit 失败'])
    expect(o.rewinds).toHaveLength(1)
    expect(o.status).toBe('partial')
  })
})

describe('getEvidence：null 数组合一为 []', () => {
  it('checks / screenshots / diff_brief 为 null 时给出空数组', async () => {
    request.mockResolvedValueOnce({
      task_id: 't1',
      scope: 'task',
      checks: null,
      screenshots: null,
      diff_brief: null,
      errors: 0,
      changes: { files: 0, additions: 0, deletions: 0 },
      created_at: '2026-09-11T00:00:00+08:00',
    })

    const e = await getEvidence('t1')
    expect(e.checks).toEqual([])
    expect(e.screenshots).toEqual([])
    expect(e.diff_brief).toEqual([])
    expect(e.diff_brief.length).toBe(0)
  })
})
