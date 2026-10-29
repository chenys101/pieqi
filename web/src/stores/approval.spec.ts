// Approval Store 单测：**决策 id 现取**与"已结算"补偿（2026-10-23）。
//
// 背景（f74bcb09 会话后期刷屏的 16 条 no pending）：卡片组件拿着渲染时刻的
// props.approval.id 回投，而期间 agent 已经发起了下一条请求（CurrentDecision 换了 id）。
// 这一层的判据很窄但很硬：
//   ① 提交时必须是 **store 里此刻**的那条 id，不是调用方手里的旧值；
//   ② 卡已经不在了（别处已批 / 任务已推进）→ 一个请求都不发；
//   ③ 后端回"已经处理过了" → 改投 approve_same，把同意兑现成同类免审；
//   ④ 其余失败必须抛出去（调用方要 notify），不能被吞掉。
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useApprovalStore, isAlreadySettled } from './approval'
import { useTaskStore } from './task'
import * as api from '@/services/api/tasks'
import type { Task } from '@/types/task'

vi.mock('@/services/api/tasks', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/services/api/tasks')>()),
  getTask: vi.fn(),
  intervene: vi.fn(),
}))

function task(over: Partial<Task> = {}): Task {
  return {
    id: 't1',
    title: '会话',
    prompt: 'p',
    project: 'pieqi',
    projectPath: 'D:/code/go/pieqi',
    status: 'waiting_input',
    agent: 'dsh',
    model: '',
    mode: '',
    modes: [],
    sessionId: 's1',
    createdAt: '2026-10-23T00:00:00Z',
    updatedAt: '2026-10-23T00:01:00Z',
    interventions: [],
    ...over,
  }
}

/** 造一张审批卡（字段与 adaptDecision 的对齐） */
function decision(id: string) {
  return {
    id,
    taskId: 't1',
    kind: 'approval' as const,
    tool: 'pwsh',
    risk: 'L2' as const,
    summary: 'pwsh：git status',
    options: ['approve', 'deny', 'approve_session'],
    createdAt: '2026-10-23T00:00:00Z',
  }
}

const getTask = vi.mocked(api.getTask)
const intervene = vi.mocked(api.intervene)

describe('ApprovalStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    getTask.mockReset()
    intervene.mockReset()
    intervene.mockResolvedValue(undefined)
  })

  it('提交的是 store 里此刻的 decision id（不是渲染时刻的旧值）', async () => {
    const s = useApprovalStore()
    const taskStore = useTaskStore()
    // 本地还是旧卡 req-old，服务端已经推进到 req-new。
    taskStore.upsertTask(task({ decision: decision('req-old') }))
    getTask.mockResolvedValue(task({ decision: decision('req-new') }))

    await s.approve('t1')

    expect(intervene).toHaveBeenCalledTimes(1)
    expect(intervene).toHaveBeenCalledWith('t1', {
      kind: 'decision',
      decisionId: 'req-new',
      choice: 'approve',
    })
  })

  it('卡已经不在了：一个请求都不发，并清掉本地残态', async () => {
    const s = useApprovalStore()
    const taskStore = useTaskStore()
    taskStore.upsertTask(task({ decision: decision('req-old') }))
    // 服务端那边早处理完了：任务已回 running、没有决策。
    getTask.mockResolvedValue(task({ status: 'running' }))

    await s.approve('t1')

    expect(intervene).not.toHaveBeenCalled()
    expect(taskStore.byId('t1')?.decision).toBeUndefined()
  })

  it('刷新失败时退回本地那条 id，仍然尝试提交（不因为一次 GET 失败就放弃）', async () => {
    const s = useApprovalStore()
    const taskStore = useTaskStore()
    taskStore.upsertTask(task({ decision: decision('req-local') }))
    getTask.mockRejectedValue(new Error('network'))

    await s.approve('t1')

    expect(intervene).toHaveBeenCalledWith('t1', {
      kind: 'decision',
      decisionId: 'req-local',
      choice: 'approve',
    })
  })

  it('后端回"已经处理过了" → 改投 approve_same（把同意兑现成同类免审）', async () => {
    const s = useApprovalStore()
    const taskStore = useTaskStore()
    taskStore.upsertTask(task({ decision: decision('req-dup') }))
    getTask.mockResolvedValue(task({ decision: decision('req-dup') }))
    intervene
      .mockRejectedValueOnce(new Error('审批 req-dup 已经处理过了（那张卡是旧的）；这条命令不需要再批一次'))
      .mockResolvedValueOnce(undefined)

    await s.approve('t1')

    expect(intervene).toHaveBeenCalledTimes(2)
    expect(intervene.mock.calls[1][1]).toMatchObject({ decisionId: 'req-dup', choice: 'approve_same' })
    // 成功收尾后本地清态（卡不该继续挂在界面上）
    expect(taskStore.byId('t1')?.decision).toBeUndefined()
  })

  it('其他失败必须抛出去（调用方要 notify，不能静默吞掉）', async () => {
    const s = useApprovalStore()
    const taskStore = useTaskStore()
    taskStore.upsertTask(task({ decision: decision('req-x') }))
    getTask.mockResolvedValue(task({ decision: decision('req-x') }))
    intervene.mockRejectedValue(new Error('boom'))

    await expect(s.approve('t1')).rejects.toThrow('boom')
    // 失败时不得乐观清态：卡还在，用户要能再试一次。
    expect(taskStore.byId('t1')?.decision?.id).toBe('req-x')
  })

  it('deny 遇到"已处理过"不补投 approve_same（拒绝不该被改写成同意）', async () => {
    const s = useApprovalStore()
    const taskStore = useTaskStore()
    taskStore.upsertTask(task({ decision: decision('req-d') }))
    getTask.mockResolvedValue(task({ decision: decision('req-d') }))
    intervene.mockRejectedValue(new Error('审批 req-d 已经处理过了（那张卡是旧的）'))

    await expect(s.deny('t1')).rejects.toThrow('已经处理过')
    expect(intervene).toHaveBeenCalledTimes(1)
  })

  it('refreshTask 并发合并：同一 id 同时刷新只发一次 GET', async () => {
    const taskStore = useTaskStore()
    let resolve!: (t: Task) => void
    getTask.mockImplementation(() => new Promise<Task>((r) => (resolve = r)))

    const a = taskStore.refreshTask('t1')
    const b = taskStore.refreshTask('t1')
    expect(getTask).toHaveBeenCalledTimes(1)
    resolve(task())
    await expect(a).resolves.toMatchObject({ id: 't1' })
    await expect(b).resolves.toMatchObject({ id: 't1' })
  })

  it('isAlreadySettled 只认"已结算"文案，不把 no pending 也算进去', () => {
    expect(isAlreadySettled(new Error('审批 x 已经处理过了（那张卡是旧的）'))).toBe(true)
    expect(isAlreadySettled(new Error('permission "x" already resolved'))).toBe(true)
    expect(isAlreadySettled(new Error('no pending permission for decision "x"'))).toBe(false)
  })
})
