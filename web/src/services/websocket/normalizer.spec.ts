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
      tasks: [expect.objectContaining({ id: 't1', project: 'erp', agent: 'claude-code' })],
      dtos: [dto()],
    })
  })

  it('task_updated：task_id + task 缺一不可', () => {
    const ok = normalizeWsMessage({ type: 'task_updated', task_id: 't1', task: dto() })
    expect(ok?.type).toBe('task_upserted')

    expect(normalizeWsMessage({ type: 'task_updated', task: dto() })).toBeNull()
    expect(normalizeWsMessage({ type: 'task_created', task_id: 't1' })).toBeNull()
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

  it('空 events 返回空数组', () => {
    expect(normalizeEvents('t1', undefined)).toEqual([])
    expect(normalizeEvents('t1', [])).toEqual([])
  })
})
