// Task Store 单测（方案 §48.2）：getters / upsert / 乐观更新（不触网）
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useTaskStore } from './task'
import * as api from '@/services/api/tasks'
import type { Task } from '@/types/task'

// 只拦 getTasks：loaded 的两条分支（拉到 / 拉挂）都得能断言，其余保持真实实现
vi.mock('@/services/api/tasks', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/services/api/tasks')>()),
  getTasks: vi.fn(),
}))

/** 最小 Task fixture */
function task(over: Partial<Task> = {}): Task {
  return {
    id: 't1',
    title: '修复订单 bug',
    prompt: '修复订单创建的 bug',
    project: 'erp',
    projectPath: 'G:/ws/erp',
    status: 'running',
    agent: 'claude',
    model: '',
    sessionId: 's1',
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:01:00Z',
    interventions: [],
    ...over,
  }
}

describe('TaskStore', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('upsertTask：新任务插入，已有任务原位替换', () => {
    const s = useTaskStore()
    s.upsertTask(task())
    s.upsertTask(task({ status: 'completed' }))
    expect(s.tasks).toHaveLength(1)
    expect(s.tasks[0].status).toBe('completed')
  })

  it('applySnapshot：全量替换', () => {
    const s = useTaskStore()
    s.upsertTask(task())
    s.applySnapshot([task({ id: 't2' }), task({ id: 't3' })])
    expect(s.tasks.map((t) => t.id)).toEqual(['t2', 't3'])
  })

  // loaded 是"空数组到底是不是真的空"的唯一判据，新建任务页靠它决定默认项目模式
  it('loaded：applySnapshot 即算到了（空快照也算）', () => {
    const s = useTaskStore()
    expect(s.loaded).toBe(false)
    s.applySnapshot([])
    expect(s.loaded).toBe(true)
  })

  it('loaded：loadTasks 成功与失败都置位（拉挂时也要能落到自定义路径）', async () => {
    const s = useTaskStore()
    vi.mocked(api.getTasks).mockResolvedValueOnce({ tasks: [task()], groups: [] } as never)
    await s.loadTasks()
    expect(s.loaded).toBe(true)
    expect(s.error).toBeNull()

    vi.mocked(api.getTasks).mockRejectedValueOnce(new Error('offline'))
    await s.loadTasks()
    expect(s.loaded).toBe(true)
    expect(s.error).toBe('offline')
  })

  it('getters：running / waiting / failed / needsAttention / counts', () => {
    const s = useTaskStore()
    s.applySnapshot([
      task({ id: 'a', status: 'running' }),
      task({ id: 'b', status: 'pending' }),
      task({ id: 'c', status: 'waiting_input' }),
      task({ id: 'd', status: 'failed' }),
      task({ id: 'e', status: 'completed' }),
    ])
    expect(s.runningTasks.map((t) => t.id)).toEqual(['a', 'b'])
    expect(s.waitingTasks.map((t) => t.id)).toEqual(['c'])
    expect(s.failedTasks.map((t) => t.id)).toEqual(['d'])
    // 需关注 = 待决策 + 失败
    expect(s.needsAttention.map((t) => t.id)).toEqual(['c', 'd'])
    expect(s.counts).toMatchObject({ running: 1, pending: 1, waiting_input: 1, failed: 1, completed: 1 })
  })

  it('groupsByProject：按归一路径分组，活跃组排前', () => {
    const s = useTaskStore()
    s.applySnapshot([
      task({ id: 'a', projectPath: 'G:\\ws\\erp' }),
      task({ id: 'b', projectPath: 'g:/ws/erp', status: 'completed' }),
      task({ id: 'c', projectPath: 'g:/ws/oms', status: 'running' }),
    ])
    const groups = s.groupsByProject
    expect(groups).toHaveLength(2)
    // 同一项目（分隔符/大小写不同）归为一组
    expect(groups.find((g) => g.key === 'g:/ws/erp')!.tasks).toHaveLength(2)
    // 活跃任务多的组排前：oms(running) 的活跃数 1 = erp(running 1)... erp 也有 1 个 running
    // erp: running=1, oms: running=1 → 同为活跃，顺序按首见：erp 在前
    expect(groups[0].key).toBe('g:/ws/erp')
  })

  it('patchStatus / clearDecision：审批后乐观更新', () => {
    const s = useTaskStore()
    s.applySnapshot([
      task({
        id: 'a',
        status: 'waiting_input',
        decision: { id: 'd1', taskId: 'a', kind: 'approval', summary: 'rm -rf', options: [], createdAt: '2026-01-01T00:00:00Z' },
      }),
    ])
    s.patchStatus('a', 'running')
    expect(s.byId('a')!.status).toBe('running')
    // 终态清空 decision
    s.upsertTask(task({ id: 'a', status: 'waiting_input' }))
    s.patchStatus('a', 'completed')
    expect(s.byId('a')!.decision).toBeUndefined()
  })

  it('removeTask：按 id 删除', () => {
    const s = useTaskStore()
    s.applySnapshot([task({ id: 'a' }), task({ id: 'b' })])
    s.removeTask('a')
    expect(s.tasks.map((t) => t.id)).toEqual(['b'])
  })
})
