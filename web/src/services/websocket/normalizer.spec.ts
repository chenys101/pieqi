// Normalizer 单测（方案 §42）：wire format → 前端模型的隔离层行为
import { describe, expect, it } from 'vitest'
import { normalizeWsMessage, normalizeEvents } from './normalizer'
import type { TaskDto } from '@/types/api'

/** 最小合法 TaskDto fixture */
function dto(over: Partial<TaskDto> = {}): TaskDto {
  return {
    id: 't1',
    source: 'web',
    project_id: 'erp',
    project_path: 'G:/ws/erp',
    worktree_path: 'G:/ws/erp',
    claude_session_id: 's1',
    status: 'running',
    prompt: '修复订单创建的 bug',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:01:00Z',
    ...over,
  }
}

describe('normalizeWsMessage', () => {
  it('snapshot：tasks 全量适配为前端模型', () => {
    const msg = normalizeWsMessage({ type: 'snapshot', tasks: [dto()] })
    expect(msg).toEqual({
      type: 'snapshot',
      tasks: [expect.objectContaining({ id: 't1', project: 'erp', agent: 'claude' })],
      dtos: [dto()],
    })
  })

  it('task_updated：task_id + task 缺一不可', () => {
    const ok = normalizeWsMessage({ type: 'task_updated', task_id: 't1', task: dto() })
    expect(ok?.type).toBe('task_upserted')

    expect(normalizeWsMessage({ type: 'task_updated', task: dto() })).toBeNull()
    expect(normalizeWsMessage({ type: 'task_created', task_id: 't1' })).toBeNull()
  })

  it('task_completed：与 task_updated 同样适配（后端 completed 终态走此类型）', () => {
    const completed = dto({ status: 'completed' })
    const msg = normalizeWsMessage({ type: 'task_completed', task_id: 't1', task: completed })
    expect(msg?.type).toBe('task_upserted')
    expect(msg).toEqual({ type: 'task_upserted', task: expect.objectContaining({ status: 'completed' }), dto: completed })
  })

  it('task_deleted：取 task_id', () => {
    expect(normalizeWsMessage({ type: 'task_deleted', task_id: 't1' })).toEqual({
      type: 'task_deleted',
      taskId: 't1',
    })
  })

  it('task_delta：is_thought 归一为 isThought', () => {
    const msg = normalizeWsMessage({
      type: 'task_delta',
      task_id: 't1',
      delta: { text: 'hello', is_thought: true },
    })
    expect(msg).toEqual({ type: 'delta', delta: { taskId: 't1', text: 'hello', isThought: true } })
  })

  it('无法识别的消息返回 null（静默丢弃）', () => {
    expect(normalizeWsMessage(null)).toBeNull()
    expect(normalizeWsMessage('str')).toBeNull()
    expect(normalizeWsMessage({ type: 'unknown' })).toBeNull()
    expect(normalizeWsMessage({ type: 'task_delta', task_id: 't1' })).toBeNull()
  })

  // --- task_usage：上下文用量（ACP usage_update 的透传） ---

  it('task_usage：归一为 usage 消息并保留 used/size', () => {
    const msg = normalizeWsMessage({
      type: 'task_usage',
      task_id: 't1',
      usage: { used: 12345, size: 200000 },
    })
    expect(msg).toEqual({
      type: 'usage',
      taskId: 't1',
      usage: { used: 12345, size: 200000, costUsd: undefined, hasCost: false },
    })
  })

  it('task_usage：has_cost 缺省即 false（"没报成本"不得被读成免费）', () => {
    const omitted = normalizeWsMessage({ type: 'task_usage', task_id: 't1', usage: { used: 1, size: 100 } })
    expect(omitted).toMatchObject({ usage: { hasCost: false, costUsd: undefined } })

    // 明确报 0 与没报是两件事：前者 hasCost=true，UI 才允许显示 "$0.00"
    const explicitZero = normalizeWsMessage({
      type: 'task_usage',
      task_id: 't1',
      usage: { used: 1, size: 100, cost_usd: 0, has_cost: true },
    })
    expect(explicitZero).toMatchObject({ usage: { hasCost: true, costUsd: 0 } })
  })

  // size<=0 的快照没有信息量（"占用了 x/0"）。后端已拦，这里再拦一次是因为
  // 本层的职责就是"进来的一定是合法模型"，消费方（进度条）不该各写一遍守卫。
  it('task_usage：size<=0 或缺字段一律丢弃', () => {
    expect(normalizeWsMessage({ type: 'task_usage', task_id: 't1', usage: { used: 9, size: 0 } })).toBeNull()
    expect(normalizeWsMessage({ type: 'task_usage', task_id: 't1', usage: { used: 9, size: -1 } })).toBeNull()
    expect(normalizeWsMessage({ type: 'task_usage', task_id: 't1' })).toBeNull()
    expect(normalizeWsMessage({ type: 'task_usage', task_id: 't1', usage: { used: 'x', size: 100 } })).toBeNull()
    expect(normalizeWsMessage({ type: 'task_usage', usage: { used: 1, size: 100 } })).toBeNull()
  })
})

describe('normalizeEvents', () => {
  it('后端事件类型映射为前端稳定类型，id 为 taskId:seq', () => {
    const evs = normalizeEvents('t1', [
      { seq: 1, type: 'user', text: 'hi', at: '2026-01-01T00:00:00Z' },
      { seq: 2, type: 'thinking', text: '想想', at: '2026-01-01T00:00:01Z' },
      { seq: 3, type: 'tool_use', tool_name: 'Bash', at: '2026-01-01T00:00:02Z' },
      { seq: 4, type: 'tool_result', tool_use_id: 'x', result: 'ok', at: '2026-01-01T00:00:03Z' },
      { seq: 5, type: 'text', text: 'done', at: '2026-01-01T00:00:04Z' },
    ])
    expect(evs.map((e) => e.type)).toEqual([
      'user_message',
      'thinking_delta',
      'tool_call',
      'tool_result',
      'text_delta',
    ])
    expect(evs.map((e) => e.id)).toEqual(['t1:1', 't1:2', 't1:3', 't1:4', 't1:5'])
  })

  it('旧数据「↻ 续问: 」前缀的 text 事件归一为 user_message 并去前缀', () => {
    const [ev] = normalizeEvents('t1', [
      { seq: 1, type: 'text', text: '↻ 续问: 再跑一次', at: '2026-01-01T00:00:00Z' },
    ])
    expect(ev.type).toBe('user_message')
    expect(ev.payload.text).toBe('再跑一次')
  })

  it('model_switch：归一为独立类型并取出 from/to（不透明串原样透传）', () => {
    const [ev] = normalizeEvents('t1', [
      {
        seq: 1,
        type: 'model_switch',
        text: '模型切换：X → Y',
        input: { from: '["magpie","a"]', to: '["magpie","b"]' },
        at: '2026-01-01T00:00:00Z',
      },
    ])
    expect(ev.type).toBe('model_switch')
    // 前端只拿它去比对清单换人读名，绝不解析/重组这个串
    expect(ev.payload.modelSwitch).toEqual({ from: '["magpie","a"]', to: '["magpie","b"]' })
    // 人读摘要是后端写好的，载荷缺失时兜底显示
    expect(ev.payload.text).toBe('模型切换：X → Y')
  })

  it('model_switch：from 缺失（此前用 agent 默认）补空串，不当成非法载荷', () => {
    const [ev] = normalizeEvents('t1', [
      { seq: 1, type: 'model_switch', input: { to: '["magpie","b"]' }, at: '2026-01-01T00:00:00Z' },
    ])
    expect(ev.payload.modelSwitch).toEqual({ from: '', to: '["magpie","b"]' })
  })

  it('model_switch：to 缺失即载荷非法 → 降级为纯文本（不编造"换到了什么"）', () => {
    const [ev] = normalizeEvents('t1', [
      { seq: 1, type: 'model_switch', text: '模型切换：X → Y', input: { from: 'a' }, at: '2026-01-01T00:00:00Z' },
    ])
    expect(ev.type).toBe('model_switch')
    expect(ev.payload.modelSwitch).toBeUndefined()
    expect(ev.payload.text).toBe('模型切换：X → Y')
  })

  it('空 events 返回空数组', () => {
    expect(normalizeEvents('t1', undefined)).toEqual([])
    expect(normalizeEvents('t1', [])).toEqual([])
  })
})
