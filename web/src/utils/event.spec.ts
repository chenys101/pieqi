// 事件工具单测（方案 §14/§39）：去重 / 流式增量合并
import { describe, expect, it } from 'vitest'
import { EventDeduper, hasTextDelta, mergeDeltaIntoEvents, persistentEventId } from './event'
import type { AgentEvent } from '@/types/event'

function ev(id: string, type: AgentEvent['type'], text?: string): AgentEvent {
  return { id, taskId: 't1', type, timestamp: '2026-01-01T00:00:00Z', payload: { text } }
}

describe('EventDeduper', () => {
  it('重复 id 判定 has=true，新 id 登记后 has=true', () => {
    const d = new EventDeduper()
    expect(d.has('t1:1')).toBe(false)
    d.add('t1:1')
    expect(d.has('t1:1')).toBe(true)
    expect(d.has('t1:2')).toBe(false)
  })

  it('addAll 批量登记（snapshot 替换场景）', () => {
    const d = new EventDeduper()
    d.addAll([ev('t1:1', 'text_delta'), ev('t1:2', 'tool_call')])
    expect(d.has('t1:1')).toBe(true)
    expect(d.has('t1:2')).toBe(true)
  })

  it('reset 清空（会话删除场景）', () => {
    const d = new EventDeduper()
    d.add('t1:1')
    d.reset()
    expect(d.has('t1:1')).toBe(false)
  })
})

describe('mergeDeltaIntoEvents', () => {
  it('末事件同类型：文本追加不新建', () => {
    const out = mergeDeltaIntoEvents([ev('t1:1', 'text_delta', 'Hello')], {
      taskId: 't1',
      text: ' world',
      isThought: false,
    }, () => 'new')
    expect(out.events).toHaveLength(1)
    expect(out.events[0].payload.text).toBe('Hello world')
    expect(out.events[0].id).toBe('t1:1') // id 不变（仍是同一段输出）
    expect(out.firstText).toBe(false)
  })

  it('类型切换（text→thinking）：新建事件', () => {
    const out = mergeDeltaIntoEvents([ev('t1:1', 'text_delta', 'Hi')], {
      taskId: 't1',
      text: '思考中',
      isThought: true,
    }, () => 't1:delta-1')
    expect(out.events).toHaveLength(2)
    expect(out.events[1].type).toBe('thinking_delta')
    expect(out.events[1].id).toBe('t1:delta-1')
  })

  it('空流首次正文文本：firstText=true（结束思考占位的时机）', () => {
    const out = mergeDeltaIntoEvents([], { taskId: 't1', text: 'Hi', isThought: false }, () => 'x')
    expect(out.firstText).toBe(true)
  })

  it('已有正文后再追加：firstText=false；thinking 不算正文', () => {
    const a = mergeDeltaIntoEvents([ev('t1:1', 'text_delta', 'A')], { taskId: 't1', text: 'B', isThought: false }, () => 'x')
    expect(a.firstText).toBe(false)
    const b = mergeDeltaIntoEvents([ev('t1:1', 'thinking_delta', '想')], { taskId: 't1', text: 'C', isThought: false }, () => 'x')
    expect(b.firstText).toBe(true)
  })

  // 2026-10-22 性能修复：hasText 是**可选**的快速路径，结果必须与扫描完全一致。
  describe('hasText 快速路径（性能）', () => {
    it('hasText=true 且此前确有正文：跳过扫描，firstText=false', () => {
      const stream = [ev('t1:1', 'text_delta', 'A'), ev('t1:2', 'tool_call')]
      const out = mergeDeltaIntoEvents(stream, { taskId: 't1', text: 'B', isThought: false }, () => 'x', true)
      expect(out.firstText).toBe(false)
    })

    it('hasText=true 时**不再读 events**：用一个会抛错的代理证明真的跳过了扫描', () => {
      // 把数组换成"一访问 some 就炸"的对象，能跑通即证明没有走 .some()。
      const trap = new Proxy([ev('t1:1', 'tool_call')] as AgentEvent[], {
        get(target, prop, recv) {
          if (prop === 'some') throw new Error('不应扫描：hasText=true 时必须跳过 .some()')
          return Reflect.get(target, prop, recv)
        },
      })
      const out = mergeDeltaIntoEvents(trap, { taskId: 't1', text: 'B', isThought: false }, () => 'x', true)
      expect(out.firstText).toBe(false)
      expect(out.events).toHaveLength(2) // 类型不同 → 新建
    })

    it('不传 hasText：行为与改动前完全一致（回退扫描）', () => {
      const noText = mergeDeltaIntoEvents([ev('t1:1', 'tool_call')], { taskId: 't1', text: 'A', isThought: false }, () => 'x')
      expect(noText.firstText).toBe(true)
      const withText = mergeDeltaIntoEvents([ev('t1:1', 'text_delta', 'A')], { taskId: 't1', text: 'B', isThought: false }, () => 'x')
      expect(withText.firstText).toBe(false)
    })

    it('thinking 增量即使 hasText=true 也不算正文', () => {
      const out = mergeDeltaIntoEvents([], { taskId: 't1', text: '想', isThought: true }, () => 'x', true)
      expect(out.firstText).toBe(false)
    })
  })
  it('hasTextDelta 判定与 mergeDeltaIntoEvents 的回退路径一致', () => {
    expect(hasTextDelta([ev('a', 'tool_call')])).toBe(false)
    expect(hasTextDelta([ev('a', 'thinking_delta'), ev('b', 'text_delta')])).toBe(true)
  })
})

describe('persistentEventId', () => {
  it('taskId:seq 格式', () => {
    expect(persistentEventId('abc', 3)).toBe('abc:3')
  })
})
