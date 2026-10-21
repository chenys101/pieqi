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
  createTask: vi.fn(),
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

  // 需求 2（补充会话后当前会话在最上）：有活跃任务再更新时，它要挪到列表最前。
  // 判据是「还需要人参与」—— 用户刚补了一句「继续」，就要在这个会话里等输出，
  // 不该把它留在几条已完结任务的后面。
  it('upsertTask：活跃任务（running / waiting_input / pending）提到列表最前', () => {
    const s = useTaskStore()
    s.applySnapshot([
      task({ id: 'a', status: 'completed', updatedAt: '2026-01-01T03:00:00Z' }),
      task({ id: 'b', status: 'completed', updatedAt: '2026-01-01T02:00:00Z' }),
      task({ id: 'c', status: 'completed', updatedAt: '2026-01-01T01:00:00Z' }),
    ])
    expect(s.tasks.map((t) => t.id)).toEqual(['a', 'b', 'c'])

    // 续问推回来的 task_updated（active）→ c 提到最前
    s.upsertTask(task({ id: 'c', status: 'running' }))
    expect(s.tasks.map((t) => t.id)).toEqual(['c', 'a', 'b'])
    // 已替换成新内容（不是只换位置）
    expect(s.byId('c')!.status).toBe('running')

    s.upsertTask(task({ id: 'b', status: 'waiting_input' }))
    expect(s.tasks.map((t) => t.id)).toEqual(['b', 'c', 'a'])

    s.upsertTask(task({ id: 'a', status: 'pending' }))
    expect(s.tasks.map((t) => t.id)).toEqual(['a', 'b', 'c'])
  })

  // 反例守卫：终态**不**前移。终态是"这件事结束了"，把它顶到用户眼前抢位置
  // 会让"最近完成的一条"看起来像"还要我处理的一条"。
  it('upsertTask：终态不前移（原位替换）', () => {
    const s = useTaskStore()
    s.applySnapshot([
      task({ id: 'a', status: 'running' }),
      task({ id: 'b', status: 'running' }),
      task({ id: 'c', status: 'running' }),
    ])
    s.upsertTask(task({ id: 'c', status: 'completed' }))
    expect(s.tasks.map((t) => t.id)).toEqual(['a', 'b', 'c'])
    expect(s.byId('c')!.status).toBe('completed')
  })

  // 分组页看的是"项目组"，需求 2 在那一层的表现是：活跃任务所在项目随之前移。
  it('groupsByProject：活跃任务前移后，它所属项目也排到最前', () => {
    const s = useTaskStore()
    s.applySnapshot([
      task({ id: 'x1', projectPath: 'G:/ws/alpha', status: 'completed' }),
      task({ id: 'x2', projectPath: 'G:/ws/beta', status: 'completed' }),
      task({ id: 'x3', projectPath: 'G:/ws/gamma', status: 'completed' }),
    ])
    expect(s.groupsByProject.map((g) => g.key)).toEqual(['g:/ws/alpha', 'g:/ws/beta', 'g:/ws/gamma'])

    // gamma 里补了一句 → 推到最前
    s.upsertTask(task({ id: 'x3', projectPath: 'G:/ws/gamma', status: 'running' }))
    expect(s.groupsByProject.map((g) => g.key)).toEqual(['g:/ws/gamma', 'g:/ws/alpha', 'g:/ws/beta'])
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

  // 回归：createTask 曾把 images 参数**截断**在自己的签名外（api.createTask 早就支持，
  // 但 store 这一层没往上传），表现就是"新建任务时选了图也发不出去"且不报错。
  // 这里钉住 store → api 的透传，防止签名再次被中间截断。
  it('createTask：images 必须透传到 api 层（纯 base64 + mime_type）', async () => {
    const s = useTaskStore()
    vi.mocked(api.createTask).mockResolvedValueOnce(task() as never)
    const imgs = [{ data: 'AAAA', mime_type: 'image/png' }]

    await s.createTask('G:/ws/erp', '看图', 'qoder', 'qwen', imgs)

    expect(api.createTask).toHaveBeenCalledWith('G:/ws/erp', '看图', 'qoder', 'qwen', imgs)
    // 本地乐观插入也要发生（详情页靠它立刻渲染）
    expect(s.tasks.map((t) => t.id)).toEqual(['t1'])
  })

  it('createTask：不带图时 images 为 undefined（语义与纯文本一致）', async () => {
    const s = useTaskStore()
    vi.mocked(api.createTask).mockResolvedValueOnce(task() as never)

    await s.createTask('G:/ws/erp', '纯文本', 'claude')

    // 取**最后一次**调用：mock 的 calls 在同文件内跨用例累积，
    // 写死 calls[0] 会读到前一个用例留下的那条（本文件最初就是这么错的）。
    const last = vi.mocked(api.createTask).mock.calls.at(-1)!
    expect(last[1]).toBe('纯文本')
    expect(last[4]).toBeUndefined()
  })
})
