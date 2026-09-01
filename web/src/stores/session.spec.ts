// Session Store 单测（方案 §48.2）：事件流同步 / 流式增量 / 思考占位 / 去重
import { beforeEach, describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useSessionStore } from './session'
import type { TaskDto } from '@/types/api'

function dto(over: Partial<TaskDto> = {}): TaskDto {
  return {
    id: 't1',
    source: 'web',
    project_id: 'erp',
    project_path: 'G:/ws/erp',
    worktree_path: 'G:/ws/erp',
    claude_session_id: 's1',
    status: 'running',
    prompt: 'p',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:01:00Z',
    events: [
      { seq: 1, type: 'user', text: 'hi', at: '2026-01-01T00:00:00Z' },
      { seq: 2, type: 'text', text: 'Hello', at: '2026-01-01T00:00:01Z' },
    ],
    ...over,
  }
}

describe('SessionStore', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('syncFromTask：写入会话元信息 + 全量事件并登记去重', () => {
    const s = useSessionStore()
    s.syncFromTask(dto())
    expect(s.session('t1')).toMatchObject({ id: 't1', status: 'running', agent: 'claude-code' })
    expect(s.events('t1').map((e) => e.type)).toEqual(['user_message', 'text_delta'])
    // 去重集合已登记持久化事件 id
    expect(s.deduper('t1').has('t1:1')).toBe(true)
  })

  it('applyDelta：同类型末事件追加；首次正文清除思考占位', () => {
    const s = useSessionStore()
    // events 只含用户消息（无正文），首条 text 增量应清除思考占位
    s.syncFromTask(dto({ events: [{ seq: 1, type: 'user', text: 'hi', at: '2026-01-01T00:00:00Z' }] }))
    s.setThinking('t1', true)
    s.applyDelta({ taskId: 't1', text: 'Hello', isThought: false })
    expect(s.isThinking('t1')).toBe(false)

    // 已有正文后再追加：仍是一个 text 事件，文本被追加
    s.setThinking('t1', true)
    s.applyDelta({ taskId: 't1', text: ' world', isThought: false })
    const evs = s.events('t1')
    expect(evs).toHaveLength(2)
    expect(evs[1].payload.text).toBe('Hello world')
    // 已有正文 → 不是首条 → 不清除思考占位
    expect(s.isThinking('t1')).toBe(true)
  })

  it('applyDelta：thinking 增量新建事件，不清思考占位', () => {
    const s = useSessionStore()
    s.syncFromTask(dto())
    s.setThinking('t1', true)
    s.applyDelta({ taskId: 't1', text: '想想', isThought: true })
    expect(s.events('t1')).toHaveLength(3)
    expect(s.isThinking('t1')).toBe(true)
  })

  it('终态 / waiting_input 同步时清除思考占位', () => {
    const s = useSessionStore()
    s.syncFromTask(dto())
    s.setThinking('t1', true)
    s.syncFromTask(dto({ status: 'waiting_input', events: [] }))
    expect(s.isThinking('t1')).toBe(false)
  })

  it('appendLocalUserMessage：乐观插入用户气泡（提交后不等 WS）', () => {
    const s = useSessionStore()
    s.syncFromTask(dto())
    s.appendLocalUserMessage('t1', '再检查一下')
    const evs = s.events('t1')
    expect(evs.at(-1)).toMatchObject({ type: 'user_message' })
    expect(evs.at(-1)!.payload.text).toBe('再检查一下')
  })

  it('removeSession：清理全部关联状态', () => {
    const s = useSessionStore()
    s.syncFromTask(dto())
    s.removeSession('t1')
    expect(s.session('t1')).toBeUndefined()
    expect(s.events('t1')).toEqual([])
    expect(s.isThinking('t1')).toBe(false)
  })
})
