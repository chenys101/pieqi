// 回归：**WS 快照把列表 DTO 也喂给了会话事件流**（2026-10-23 用户反馈「无法查看任务详情」）。
//
// 真实现场（已在隔离实例 + 真实浏览器复现）：详情页打开着 → 服务端重启 → WS 重连 →
// 首帧 snapshot 到达。此时：
//   ① taskStore.applySnapshot 把「轻量视图」当权威数据**整表替换**；
//   ② sessionStore 若也收到这份 DTO，它的 events 由于来自 HTTP JSON，
//      在 JS 里是**显式 null**（不是 undefined）；
//   ③ SessionPage 的判据是「本地已有该会话的事件流就跳过拉详情」，
//      于是详情页既不渲染时间线、也不再向服务端要数据 ——
//      表现就是「时间线容器在、内容空、页面写着任务不存在或已删除」。
//
// 这是一个**跨两处的耦合**：任何一处单独修都能让症状消失，但只有两处一起修才
// 保证不会再从另一条路径回来（详情页仍会拿空事件去渲染）。
// 所以两段断言分开放：Store 级钉住"快照不得提供事件流"，Page 级钉住"没有事件必须去拉"。
import { beforeEach, describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useSessionStore } from './session'
import { useTaskStore } from './task'
import { dispatch } from '@/services/websocket/dispatcher'
import { normalizeWsMessage } from '@/services/websocket/normalizer'
import { adaptTask } from '@/services/api/client'
import type { TaskDto, TaskSummaryDto } from '@/types/api'
import type { Task } from '@/types/task'

/** 后端的列表/快照载荷（model.NewTaskSummary）：**events 被置 nil**，序列化后是 `"events": null`。 */
function snapshotTask(id: string, status: Task['status'] = 'failed'): TaskSummaryDto {
  return {
    id,
    source: 'http',
    project_id: 'pieqi',
    project_path: 'D:/code/go/pieqi',
    worktree_path: 'D:/code/go/pieqi',
    claude_session_id: 's1',
    status,
    prompt: 'p',
    // ★ 关键：Go 的 `Events []TaskEvent json:"events,omitempty"` 对 nil 切片不发字段，
    // 但 TaskSummary 把 Events 遮蔽成非 omitempty 的 `[]TaskEvent`，nil 会原样发出 null。
    // 无论线上是 null 还是缺字段，前端都**不能**把它当成"我说了什么"。
    events: null as unknown as TaskDto['events'],
    event_count: 322,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:01:00Z',
  } as unknown as TaskSummaryDto
}

describe('WS 快照不得污染会话事件流', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('dispatch(snapshot)：列表任务进了 taskStore，但事件流仍是空的', () => {
    const taskStore = useTaskStore()
    const sessionStore = useSessionStore()
    const msg = normalizeWsMessage({ type: 'snapshot', seq: 10, tasks: [snapshotTask('t1')] })
    expect(msg).not.toBeNull()

    dispatch(msg!, { taskStore, sessionStore })

    // 元数据到位（列表照常能显示）
    expect(taskStore.byId('t1')?.status).toBe('failed')
    // ★ 事件流不得被"这个任务有 0 条事件"伪装：它只是没给内容
    expect(sessionStore.events('t1')).toEqual([])
  })

  it('重连场景：本地已有事件流时，快照不得把它清空（null events 不是"没有事件"）', () => {
    const taskStore = useTaskStore()
    const sessionStore = useSessionStore()
    // 详情页已经拉过一次完整详情
    sessionStore.syncFromTask({
      ...snapshotTask('t1', 'running'),
      events: [
        { seq: 1, type: 'user', text: 'hi', at: '2026-01-01T00:00:00Z' },
        { seq: 2, type: 'text', text: 'Hello', at: '2026-01-01T00:00:01Z' },
      ],
    } as unknown as TaskSummaryDto)
    expect(sessionStore.events('t1')).toHaveLength(2)

    const msg = normalizeWsMessage({ type: 'snapshot', seq: 11, tasks: [snapshotTask('t1')] })
    dispatch(msg!, { taskStore, sessionStore })

    expect(sessionStore.events('t1')).toHaveLength(2)
  })

  it('syncFromTask(null events)：等同于"没带 events"，保留本地事件流', () => {
    const s = useSessionStore()
    s.syncFromTask({
      ...snapshotTask('t1', 'running'),
      events: [{ seq: 1, type: 'user', text: 'hi', at: '2026-01-01T00:00:00Z' }],
    } as unknown as TaskSummaryDto)
    s.syncFromTask(snapshotTask('t1', 'completed'))
    expect(s.events('t1')).toHaveLength(1)
    expect(s.session('t1')?.status).toBe('completed')
  })

  it('adaptTask：轻量视图不提供 output（后端已裁掉），也不猜一个空串回来', () => {
    // 详情页的「旧任务兜底文本」靠 output；列表里它是 undefined —— 两者不能混淆
    const t = adaptTask(snapshotTask('t1'))
    expect(t.output).toBeUndefined()
  })

  // ★ 变异验证用：把 applySnapshot 换成"合并"（见 task.ts 的说明）之后，
  //   前端模型里**已经有 output**，而新到达的轻量视图把它裁掉了。
  //   合并时若"用 undefined 覆盖 undefined"或"保留旧值"，前者无害、后者会让详情页
  //   拿一行过期文本当权威 —— 这里钉住的是：**服务端说没有就是没有**。
  it('applySnapshot：轻量视图不带 output 时，本地残留的 output 必须被清掉', () => {
    const taskStore = useTaskStore()
    taskStore.applySnapshot([{ ...adaptTask(snapshotTask('t1')), output: '上一轮的服务端累积文本' } as Task])
    expect(taskStore.byId('t1')?.output).toBe('上一轮的服务端累积文本')

    // 服务端此刻的权威副本里没有 output（任务已被续问、output 被清空）
    taskStore.applySnapshot([adaptTask(snapshotTask('t1'))])
    expect(taskStore.byId('t1')?.output).toBeUndefined()
  })
})
