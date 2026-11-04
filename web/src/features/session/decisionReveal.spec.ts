import { afterEach, describe, expect, it, vi } from 'vitest'
import { anchorDecisionEntry, logDecisionRendered, paneForDecision } from './decisionReveal'

describe('paneForDecision', () => {
  it('有决策时强制回时间线（避免 Banner 被反馈整屏藏掉）', () => {
    expect(paneForDecision('feedback', true)).toBe('timeline')
    expect(paneForDecision('timeline', true)).toBe('timeline')
  })

  it('无决策时不改当前 pane', () => {
    expect(paneForDecision('feedback', false)).toBe('feedback')
    expect(paneForDecision('timeline', false)).toBe('timeline')
  })
})

describe('anchorDecisionEntry', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('对目标调用 scrollIntoView（nearest + smooth）', () => {
    const el = document.createElement('div')
    const scrollIntoView = vi.fn()
    el.scrollIntoView = scrollIntoView
    anchorDecisionEntry(el)
    expect(scrollIntoView).toHaveBeenCalledWith({ block: 'nearest', behavior: 'smooth' })
  })

  it('空节点 / 无 scrollIntoView 时静默', () => {
    expect(() => anchorDecisionEntry(null)).not.toThrow()
    expect(() => anchorDecisionEntry(undefined)).not.toThrow()
    expect(() => anchorDecisionEntry({} as Element)).not.toThrow()
  })
})

describe('logDecisionRendered', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('打出 decision_rendered 对照字段', () => {
    const info = vi.spyOn(console, 'info').mockImplementation(() => {})
    logDecisionRendered({ id: 'req-1', taskId: 't1', tool: 'Bash', kind: 'approval' })
    expect(info).toHaveBeenCalledWith('[pieqi] decision_rendered', {
      decision_id: 'req-1',
      task_id: 't1',
      tool: 'Bash',
      kind: 'approval',
    })
  })
})
