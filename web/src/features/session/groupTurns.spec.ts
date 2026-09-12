import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import { groupEventsByTurn, countUserMessages } from './groupTurns'
import type { AgentEvent } from '@/types/event'
import type { TurnInfoDto } from '@/types/api'
import SessionTimeline from './components/SessionTimeline.vue'
import { useSessionStore } from '@/stores/session'
import { useTaskStore } from '@/stores/task'
import { useFeedbackBundleStore } from '@/stores/feedbackBundle'
import { useFeedbackPanelStore } from '@/stores/feedbackPanel'
import type { FeedbackBundleDto } from '@/types/api'

// T5/T6 出口：分组边界与 StartEventSeq 同源（AC-R3-01）、覆盖率 100%（03）、
// 旧任务平铺不报错（04）、新分组增量出现且已有折叠态不丢（05）、
// 「查看本轮变更」→ 面板选中同 Turn（06）、文件数与点开 diff 同源（02）。

const TID = 't1'

/** AgentEvent 无 seq 字段，id 是 `${taskId}:${seq}` —— 用它编码 seq（真实约定） */
function ev(seq: number, type: AgentEvent['type'], text?: string): AgentEvent {
  return {
    id: `${TID}:${seq}`,
    taskId: TID,
    type,
    timestamp: new Date(0).toISOString(),
    payload: text !== undefined ? { text } : {},
  }
}

function info(turn: number, seq: number, files = 2, additions = 10, deletions = 3): TurnInfoDto {
  return { turn, start_event_seq: seq, summary: { files, additions, deletions } }
}

describe('groupEventsByTurn（纯函数）', () => {
  it('user_message 切组：组边界与 StartEventSeq 一致（AC-R3-01）', () => {
    // 事件流：status(1) user(2) tool(3) user(4) text(5)
    const events = [
      ev(1, 'status'),
      ev(2, 'user_message', '做A'),
      ev(3, 'tool_call'),
      ev(4, 'user_message', '做B'),
      ev(5, 'text_delta'),
    ]
    const turns = [info(1, 2), info(2, 4)]
    const groups = groupEventsByTurn(events, turns)
    expect(groups).toHaveLength(3) // 前导 + 2 个 Turn
    // AC-R3-01 的可验证形式：第 N 个 Turn 组的首事件 id 携带的 seq == turns[N-1].start_event_seq
    expect(groups[1].turn).toBe(1)
    expect(seqOf(groups[1].events[0])).toBe(turns[0].start_event_seq)
    expect(seqOf(groups[2].events[0])).toBe(turns[1].start_event_seq)
    expect(groups[1].events.map((e) => e.id)).toEqual([`${TID}:2`, `${TID}:3`])
  })

  it('覆盖率 100%：每个事件恰好落入一个组（AC-R3-03）', () => {
    const events = [
      ev(1, 'status'),
      ev(2, 'user_message'),
      ev(3, 'tool_call'),
      ev(4, 'tool_result'),
      ev(5, 'user_message'),
      ev(6, 'text_delta'),
      ev(7, 'completed'),
    ]
    const groups = groupEventsByTurn(events, [info(1, 2), info(2, 5)])
    const total = groups.reduce((n, g) => n + g.events.length, 0)
    expect(total).toBe(events.length)
    // 且无重叠：所有 id 唯一
    const ids = groups.flatMap((g) => g.events.map((e) => e.id))
    expect(new Set(ids).size).toBe(ids.length)
  })

  it('旧任务：无 user_message → 空分组，走平铺兜底（AC-R3-04）', () => {
    const events = [ev(1, 'status'), ev(2, 'text_delta')]
    expect(groupEventsByTurn(events, [])).toEqual([])
    expect(groupEventsByTurn(events, undefined)).toEqual([])
  })

  it('旧任务续问 shim：turns=[] 但流里有 user_message → 仍平铺（不造假分组）', () => {
    // normalizer 把旧任务的「↻ 续问: 」text 合成为 user_message；后端不认它为 Turn
    const events = [ev(1, 'text_delta'), ev(2, 'user_message'), ev(3, 'text_delta')]
    expect(groupEventsByTurn(events, [])).toEqual([])
  })

  it('临时增量组：已知边界之后的 user_message 开新组，且吞掉后续事件（AC-R3-05）', () => {
    // turns 只知道 Turn1(seq=1)；运行中 user@10 到达，随后 tool@11 应落在 Turn2 而不是 Turn1
    const events = [
      ev(1, 'user_message', '做A'),
      ev(2, 'tool_call'),
      ev(10, 'user_message', '做B'),
      ev(11, 'tool_call'),
      ev(12, 'text_delta'),
    ]
    const groups = groupEventsByTurn(events, [info(1, 1)])
    expect(groups).toHaveLength(2)
    expect(groups[1].turn).toBe(2)
    expect(groups[1].info).toBeNull()
    expect(groups[1].events.map((e) => e.id)).toEqual([`${TID}:10`, `${TID}:11`, `${TID}:12`])
    expect(groups[0].events.map((e) => e.id)).toEqual([`${TID}:1`, `${TID}:2`])
  })

  it('乐观插入（无 seq）的本地消息也开临时组，后续事件归入', () => {
    const events = [
      ev(1, 'user_message', '做A'),
      ev(2, 'text_delta'),
      { ...ev(0, 'user_message', '做B'), id: `${TID}:local-1` },
    ]
    const groups = groupEventsByTurn(events, [info(1, 1)])
    expect(groups).toHaveLength(2)
    expect(groups[1].turn).toBe(2)
    expect(groups[1].events.map((e) => e.id)).toEqual([`${TID}:local-1`])
  })

  it('bundle 落后于事件流：尾组 info=null（不编造第二个来源的数字）', () => {
    const events = [
      ev(1, 'user_message', '做A'),
      ev(2, 'tool_call'),
      ev(3, 'user_message', '做B'),
      ev(4, 'text_delta'),
    ]
    const groups = groupEventsByTurn(events, [info(1, 1)]) // 只有 Turn 1
    expect(groups).toHaveLength(2)
    expect(groups[0].info?.turn).toBe(1)
    expect(groups[1].info).toBeNull()
  })

  it('前导区折叠进 turn 0，且 user_message 计数不受前导影响', () => {
    const events = [ev(1, 'status'), ev(2, 'rewind'), ev(3, 'user_message')]
    const groups = groupEventsByTurn(events, [info(1, 3)])
    expect(groups[0].turn).toBe(0)
    expect(groups[0].events).toHaveLength(2)
    expect(groups[1].turn).toBe(1)
    expect(countUserMessages(events)).toBe(1)
  })
})

function seqOf(e: AgentEvent): number {
  return Number(e.id.split(':')[1])
}

// ---- 组件层：SessionTimeline ----

function mountTimeline() {
  const pinia = createPinia()
  setActivePinia(pinia)
  const session = useSessionStore()
  const taskStore = useTaskStore()
  const bundleStore = useFeedbackBundleStore()
  const fb = useFeedbackPanelStore()

  taskStore.tasks.push({ id: TID, status: 'completed' } as never)
  const bundle: FeedbackBundleDto = {
    task_id: TID,
    turns: [
      { turn: 1, start_event_seq: 2, user_prompt: '做A', summary: { files: 2, additions: 10, deletions: 3 } },
    ],
    cumulative: { files: 2, additions: 10, deletions: 3 },
    checkpoints: [],
  }
  bundleStore.bundles[TID] = bundle

  session.eventsBySession[TID] = [
    ev(1, 'status'),
    ev(2, 'user_message', '做A'),
    ev(3, 'tool_call'),
    ev(4, 'text_delta'),
  ]

  const host = document.createElement('div')
  document.body.appendChild(host)
  const app = createApp({
    render: () =>
      h(SessionTimeline, { taskId: TID, consumeForceScroll: () => false }),
  })
  app.use(pinia)
  app.mount(host)
  return { host, app, fb, session, bundleStore }
}

afterEach(() => {
  document.body.innerHTML = ''
})

describe('SessionTimeline 分组渲染', () => {
  it('渲染 Turn 头部：序号 / prompt / 文件数（同源于 bundle）', async () => {
    const { host } = mountTimeline()
    await nextTick()
    const header = host.querySelector('[data-testid="turn-header-1"]')!
    expect(header.textContent).toContain('Turn #1')
    expect(header.textContent).toContain('做A')
    expect(header.textContent).toContain('2 个文件')
    // 前导区不带头部
    expect(host.querySelector('[data-testid="turn-header-0"]')).toBeNull()
  })

  it('点「查看本轮变更」→ 面板 store activeTurn 同 Turn 且展开（AC-R3-06）', async () => {
    const { host, fb } = mountTimeline()
    await nextTick()
    const link = host.querySelector<HTMLButtonElement>('[data-testid="turn-link"]')!
    link.click()
    await nextTick()
    expect(fb.activeTurn).toBe(1)
    expect(fb.collapsed).toBe(false)
  })

  it('折叠 Turn 1 不影响 Turn 2；新 user_message 增量出组（AC-R3-05）', async () => {
    const { host, session, bundleStore } = mountTimeline()
    await nextTick()
    // data-testid 挂在包裹 div 上，可点的是内部的 button
    const headerBtn = () =>
      host.querySelector<HTMLButtonElement>('[data-testid="turn-header-1"] button')!
    headerBtn().click() // 折叠
    await nextTick()
    expect(headerBtn().getAttribute('aria-expanded')).toBe('false')

    // 新一轮进来：先有事件（bundle 尚落后）
    session.eventsBySession[TID].push(ev(5, 'user_message', '做B'), ev(6, 'text_delta'))
    await nextTick()
    expect(host.querySelector('[data-testid="turn-group-2"]')).not.toBeNull()
    // Turn 1 的折叠态没被打断
    expect(
      host.querySelector<HTMLButtonElement>('[data-testid="turn-header-1"] button')!.getAttribute('aria-expanded'),
    ).toBe('false')
    // bundle 补上后（store.load 拉的是 mock 后的 bundles[TID] 已含 turn1；此处再喂 turn2）
    bundleStore.bundles[TID] = {
      ...bundleStore.bundles[TID],
      turns: [
        ...bundleStore.bundles[TID].turns,
        { turn: 2, start_event_seq: 5, user_prompt: '做B', summary: { files: 1, additions: 2, deletions: 0 } },
      ],
    }
    await nextTick()
    expect(host.querySelector('[data-testid="turn-header-2"]')!.textContent).toContain('1 个文件')
  })

  it('旧任务：无 user_message → 平铺渲染、无报错（AC-R3-04）', async () => {
    const pinia = createPinia()
    setActivePinia(pinia)
    const st = useSessionStore()
    const ts = useTaskStore()
    ts.tasks.push({ id: 'old', status: 'completed' } as never)
    st.eventsBySession['old'] = [ev(1, 'status'), ev(2, 'text_delta')]
    const host2 = document.createElement('div')
    document.body.appendChild(host2)
    const app2 = createApp({ render: () => h(SessionTimeline, { taskId: 'old', consumeForceScroll: () => false }) })
    app2.use(pinia)
    app2.mount(host2)
    await nextTick()
    expect(host2.querySelector('[data-testid="turn-header-1"]')).toBeNull()
    // 平铺：TimelineEventView 直接渲染（无分组 header），也不该有空白 —— 至少渲染了事件
    expect(host2.querySelector('[data-testid="session-timeline"]')!.children.length).toBeGreaterThan(0)
  })
})
