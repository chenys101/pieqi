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

/** 两个 Turn 的挂载（需求 2：≥2 个 Turn 才渲染左侧 rail） */
function mountTimelineTwoTurns() {
  const pinia = createPinia()
  setActivePinia(pinia)
  const session = useSessionStore()
  const taskStore = useTaskStore()
  const bundleStore = useFeedbackBundleStore()

  taskStore.tasks.push({ id: TID, status: 'completed' } as never)
  const bundle: FeedbackBundleDto = {
    task_id: TID,
    turns: [
      { turn: 1, start_event_seq: 2, user_prompt: '做A', summary: { files: 2, additions: 10, deletions: 3 } },
      { turn: 2, start_event_seq: 5, user_prompt: '做B', summary: { files: 1, additions: 2, deletions: 0 } },
    ],
    cumulative: { files: 3, additions: 12, deletions: 3 },
    checkpoints: [],
  }
  bundleStore.bundles[TID] = bundle

  session.eventsBySession[TID] = [
    ev(1, 'status'),
    ev(2, 'user_message', '做A'),
    ev(3, 'tool_call'),
    ev(4, 'text_delta'),
    ev(5, 'user_message', '做B'),
    ev(6, 'text_delta'),
  ]

  const host = document.createElement('div')
  document.body.appendChild(host)
  const app = createApp({
    render: () => h(SessionTimeline, { taskId: TID, consumeForceScroll: () => false }),
  })
  app.use(pinia)
  app.mount(host)
  return { host, app, session, bundleStore }
}

afterEach(() => {
  document.body.innerHTML = ''
})

describe('SessionTimeline 分组渲染', () => {
  it('渲染 Turn 头部：序号 / 文件数；该轮文案只在正文气泡出现一次（去重，需求 1）', async () => {
    const { host } = mountTimeline()
    await nextTick()
    const header = host.querySelector('[data-testid="turn-header-1"]')!
    expect(header.textContent).toContain('Turn #1')
    expect(header.textContent).toContain('2 个文件')
    // 展开态：头部不再重复该轮文案（改由正文 UserBubble 独占）
    expect(header.textContent).not.toContain('做A')
    // 整条时间线里「做A」只出现一次
    const timeline = host.querySelector('[data-testid="session-timeline"]')!
    expect(timeline.textContent!.split('做A').length - 1).toBe(1)
    // 前导区不带头部
    expect(host.querySelector('[data-testid="turn-header-0"]')).toBeNull()
  })

  it('折叠 Turn 1 → 头部补回该轮文案，且仍只出现一次（折叠态例外，需求 1）', async () => {
    const { host } = mountTimeline()
    await nextTick()
    const headerBtn = () =>
      host.querySelector<HTMLButtonElement>('[data-testid="turn-header-1"] button')!
    headerBtn().click() // 折叠：正文被隐藏
    await nextTick()
    const header = host.querySelector('[data-testid="turn-header-1"]')!
    expect(header.textContent).toContain('做A') // 头部补回，避免折叠后丢失「这轮在做什么」
    const timeline = host.querySelector('[data-testid="session-timeline"]')!
    expect(timeline.textContent!.split('做A').length - 1).toBe(1) // 任何时刻只出现一次
  })

  it('≥2 个 Turn → 渲染左侧跳转 rail；点击泡泡切换高亮（需求 2）', async () => {
    const { host } = mountTimelineTwoTurns()
    await nextTick()
    expect(host.querySelector('[data-testid="turn-rail"]')).not.toBeNull()
    expect(host.querySelector('[data-testid="turn-rail-1"]')).not.toBeNull()
    const btn2 = host.querySelector<HTMLButtonElement>('[data-testid="turn-rail-2"]')!
    expect(btn2).not.toBeNull()
    btn2.click()
    await nextTick()
    expect(btn2.getAttribute('aria-current')).toBe('true')
  })

  it('只有 1 个 Turn → 不渲染 rail（需求 2）', async () => {
    const { host } = mountTimeline()
    await nextTick()
    expect(host.querySelector('[data-testid="turn-rail"]')).toBeNull()
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

// ============================================================================
// QA 对抗性验证（详情页 UI 优化：Turn 头部去重 + 左侧快速跳转泡泡）
// 目标：证明「真的对」，而非「文件存在」。以下用例由 QA 独立编写。
// ============================================================================

/** 子串出现次数（用于「同一句文案恰好出现 N 次」的强断言） */
function countOf(hay: string, needle: string): number {
  return hay.split(needle).length - 1
}

/** 通用挂载：自定义 bundle.turns 与事件流（对抗性用例专用） */
function mountTimelineCustom(opts: {
  turns: TurnInfoDto[]
  events: AgentEvent[]
  consumeForceScroll?: () => boolean
}) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const session = useSessionStore()
  const taskStore = useTaskStore()
  const bundleStore = useFeedbackBundleStore()
  const fb = useFeedbackPanelStore()

  taskStore.tasks.push({ id: TID, status: 'completed' } as never)
  bundleStore.bundles[TID] = {
    task_id: TID,
    turns: opts.turns,
    cumulative: { files: 0, additions: 0, deletions: 0 },
    checkpoints: [],
  }
  session.eventsBySession[TID] = opts.events

  const host = document.createElement('div')
  document.body.appendChild(host)
  const app = createApp({
    render: () =>
      h(SessionTimeline, {
        taskId: TID,
        consumeForceScroll: opts.consumeForceScroll ?? (() => false),
      }),
  })
  app.use(pinia)
  app.mount(host)
  return { host, app, fb, session, bundleStore }
}

describe('QA 对抗性验证：Turn 去重 + 左侧 rail', () => {
  // B1 —— 去重是全局唯一（展开态）
  it('[B1] 2 个 Turn 展开态：「做A」「做B」在整页各恰好出现 1 次', async () => {
    const { host } = mountTimelineTwoTurns()
    await nextTick()
    const text = host.textContent ?? ''
    expect(countOf(text, '做A')).toBe(1)
    expect(countOf(text, '做B')).toBe(1)
    // 且头部（展开态）确实不含该轮文案
    expect(host.querySelector('[data-testid="turn-header-1"]')!.textContent).not.toContain('做A')
    expect(host.querySelector('[data-testid="turn-header-2"]')!.textContent).not.toContain('做B')
  })

  // B2 —— 折叠态例外：头部补回、正文隐藏，全程仍只出现一次
  it('[B2] 折叠 Turn 1 → 头部含「做A」且整页仍仅 1 次；再展开回到正文独占', async () => {
    const { host } = mountTimelineTwoTurns()
    await nextTick()
    const header1 = () => host.querySelector('[data-testid="turn-header-1"]')!
    const headerBtn1 = () =>
      host.querySelector<HTMLButtonElement>('[data-testid="turn-header-1"] button')!

    headerBtn1().click() // 折叠
    await nextTick()
    expect(header1().textContent).toContain('做A')
    expect(countOf(host.textContent ?? '', '做A')).toBe(1)
    expect(countOf(host.textContent ?? '', '做B')).toBe(1) // Turn 2 不受影响

    headerBtn1().click() // 再展开
    await nextTick()
    expect(header1().textContent).not.toContain('做A') // 头部不再有
    expect(host.querySelector('[data-testid="turn-group-1"]')!.textContent).toContain('做A') // 正文独占
    expect(countOf(host.textContent ?? '', '做A')).toBe(1)
  })

  // B3 —— rail 出现条件 + turn 0 不计入
  it('[B3] rail 出现条件：0/1/2 Turn 有/无；泡泡数 == turn>0 组数，无 turn-rail-0', async () => {
    // 0 Turn（turns=[]）→ 平铺兜底，无 rail
    const zero = mountTimelineCustom({
      turns: [],
      events: [ev(1, 'status'), ev(2, 'text_delta', 'x')],
    })
    await nextTick()
    expect(zero.host.querySelector('[data-testid="turn-rail"]')).toBeNull()

    // 1 Turn（含前导 turn 0，groups=2 但 railTurns=1）→ 仍无 rail
    const one = mountTimeline()
    await nextTick()
    expect(one.host.querySelector('[data-testid="turn-rail"]')).toBeNull()

    // 2 Turn（含前导 turn 0）→ 有 rail；泡泡恰为 2（turn 0 不计入）
    const two = mountTimelineTwoTurns()
    await nextTick()
    expect(two.host.querySelector('[data-testid="turn-rail"]')).not.toBeNull()
    expect(two.host.querySelector('[data-testid="turn-rail-1"]')).not.toBeNull()
    expect(two.host.querySelector('[data-testid="turn-rail-2"]')).not.toBeNull()
    expect(two.host.querySelector('[data-testid="turn-rail-0"]')).toBeNull()
    const bubbles = two.host.querySelectorAll('[data-testid^="turn-rail-"]')
    expect(bubbles.length).toBe(2)
  })

  // B4 —— 点击行为 + 不抛异常（jsdom scrollTo 降级）
  it('[B4] 点击 turn-rail-2：高亮切换、aria-current 互斥，且不抛异常', async () => {
    const { host } = mountTimelineTwoTurns()
    await nextTick()
    // 目标 section 必须存在，否则走不到 scrollTo 分支（无法验证降级路径）
    expect(host.querySelector('[data-testid="turn-group-2"]')).not.toBeNull()

    const q = (t: number) =>
      host.querySelector<HTMLButtonElement>(`[data-testid="turn-rail-${t}"]`)!

    expect(() => q(2).click()).not.toThrow()
    await nextTick()
    expect(q(2).getAttribute('aria-current')).toBe('true')
    expect(q(1).getAttribute('aria-current')).toBeNull()

    // 反向再点一次，确认互斥而非叠加
    expect(() => q(1).click()).not.toThrow()
    await nextTick()
    expect(q(1).getAttribute('aria-current')).toBe('true')
    expect(q(2).getAttribute('aria-current')).toBeNull()
  })

  // B4b —— 降级路径确实生效：jsdom 无 Element.scrollTo → 命中 catch，按几何设置 scrollTop
  it('[B4b] jumpToTurn 降级：无 scrollTo 时按几何把 scrollTop 设为目标偏移', async () => {
    const { host } = mountTimelineTwoTurns()
    await nextTick()
    const root = host.querySelector<HTMLElement>('[data-testid="session-timeline"]')!
    const node2 = host.querySelector<HTMLElement>('[data-testid="turn-group-2"]')!

    // 构造真实几何：容器 top=100 / 目标 top=500 / 当前 scrollTop=0 → 目标偏移 = 400
    root.getBoundingClientRect = () => ({ top: 100 } as unknown as DOMRect)
    node2.getBoundingClientRect = () => ({ top: 500 } as unknown as DOMRect)
    root.scrollTop = 0

    // 前置事实：jsdom 不实现 Element.scrollTo → 必然抛 TypeError → 必走 catch 降级
    expect(typeof root.scrollTo).toBe('undefined')

    host.querySelector<HTMLButtonElement>('[data-testid="turn-rail-2"]')!.click()
    await nextTick()
    expect(root.scrollTop).toBe(400)
  })

  // B5 —— 健壮性：rail 项对应 section 缺失时 trackActive 不抛错
  it('[B5] rail 有项但 turn-group 节点缺失 → trackActive 不抛错（降级跳过）', async () => {
    // bundle 已知 turn1@2 / turn2@5，事件流只覆盖 turn 1（无 seq>=5 的事件）
    const { host } = mountTimelineCustom({
      turns: [info(1, 2), info(2, 5)],
      events: [
        ev(1, 'status'),
        ev(2, 'user_message', '做A'),
        ev(3, 'tool_call'),
        ev(4, 'text_delta'),
      ],
    })
    await nextTick()
    await nextTick() // onMounted → nextTick(trackActive) 需 2 个微任务周期才落定初始高亮
    // rail 有两个泡泡（groups 由 bundle.turns 派生，turn 2 无事件也成组）
    expect(host.querySelector('[data-testid="turn-rail-2"]')).not.toBeNull()
    // 观察：即便 turn 2 无任何事件，groups 仍由 bundle.turns 派生 → section 照样渲染（空组）。
    // 因此「bundle 已知 turn2 但事件流没覆盖」并不天然产生缺失节点；缺失节点由下方人为移除构造。
    expect(host.querySelector('[data-testid="turn-group-2"]')).not.toBeNull()

    const aria = (t: number) =>
      host.querySelector<HTMLButtonElement>(`[data-testid="turn-rail-${t}"]`)!.getAttribute('aria-current')

    // 前置基线：jsdom 下所有 rect 为 0 → 初始 trackActive 命中最后一个 Turn（turn 2）
    expect(aria(2)).toBe('true')

    // 人为移除 turn 2 的 section，制造「rail 有项但节点不存在」
    host.querySelector('[data-testid="turn-group-2"]')?.remove()
    expect(host.querySelector('[data-testid="turn-group-2"]')).toBeNull()

    const root = host.querySelector<HTMLElement>('[data-testid="session-timeline"]')!
    // 触发 trackActive（滚动事件）—— 不得抛错
    expect(() => root.dispatchEvent(new Event('scroll'))).not.toThrow()
    await nextTick()
    // 缺失节点被跳过：高亮落到仍存在的 turn 1（而非停在已移除的 turn 2）
    // 若 trackActive 在缺节点处抛错，activeTurn 不会更新 → 此断言必失败，故它是真正的守卫验证
    expect(aria(1)).toBe('true')
    expect(aria(2)).toBeNull()
  })

  // B6 —— 回归：事件增长仍消费 forceScroll 标记（useTimelineScroll 接线未破）
  it('[B6] 事件增长时消费 forceScroll 标记（滚动跟随接线未破）', async () => {
    let calls = 0
    const { session } = mountTimelineCustom({
      turns: [info(1, 2)],
      events: [ev(1, 'status'), ev(2, 'user_message', '做A')],
      consumeForceScroll: () => {
        calls += 1
        return false
      },
    })
    await nextTick()
    expect(calls).toBe(0) // 初始不消费
    session.eventsBySession[TID].push(ev(3, 'text_delta', 'hi'))
    await nextTick()
    expect(calls).toBeGreaterThan(0) // 事件增长 → 消费标记
  })

  // C3 —— 锚点未被改名（jumpToTurn / trackActive 依赖）
  it('[C3] data-testid 锚点 turn-group-<turn> 保持原名', async () => {
    const { host } = mountTimelineTwoTurns()
    await nextTick()
    expect(host.querySelector('[data-testid="turn-group-0"]')).not.toBeNull()
    expect(host.querySelector('[data-testid="turn-group-1"]')).not.toBeNull()
    expect(host.querySelector('[data-testid="turn-group-2"]')).not.toBeNull()
    // 滚动容器锚点仍在真正的滚动元素上（rail 是它的兄弟，不在其内）
    const scroller = host.querySelector('[data-testid="session-timeline"]')!
    expect(scroller.className).toContain('overflow-y-auto')
    expect(scroller.querySelector('[data-testid="turn-rail"]')).toBeNull()
  })
})
