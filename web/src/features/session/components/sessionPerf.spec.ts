// 详情页两处加载优化 + 直达最新输出。
//
// 出口条件：
//   需求 2（按需挂载更早的 Turn）：
//     · 短会话（≤ 2 轮）**不截断** —— 省不下 DOM 却让用户白点一次，不值；
//     · 长会话只挂最近两轮，更早的收进「更早的 N 轮」；
//     · 点开后全部挂载，且**正在读的那一轮永远不被卸载**（否则轨道跳转
//       找不到节点、滚动位置也会跳）。
//   需求 3（直达最新输出）：
//     · 离开底部才出现，贴底时不出现（没得滚的会话同样不出现）；
//     · 点击回到最新。
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import SessionTimeline from './SessionTimeline.vue'
import { useSessionStore } from '@/stores/session'
import { useTaskStore } from '@/stores/task'
import { useFeedbackBundleStore } from '@/stores/feedbackBundle'
import type { AgentEvent } from '@/types/event'
import type { FeedbackBundleDto, TurnInfoDto } from '@/types/api'

const TID = 'long'

function ev(seq: number, type: AgentEvent['type'], text?: string): AgentEvent {
  return {
    id: `${TID}:${seq}`,
    taskId: TID,
    type,
    timestamp: new Date(0).toISOString(),
    payload: text !== undefined ? { text } : {},
  }
}

function info(turn: number, seq: number): TurnInfoDto {
  return { turn, start_event_seq: seq, user_prompt: `做${turn}`, summary: { files: 1, additions: 1, deletions: 0 } }
}

/**
 * 构造一个有 `turns` 轮的会话。
 *
 * 事件流按"每轮 3 条"铺开，是为了让"事件数超过门槛"这条辅助判据也成立 ——
 * 真实的超长会话正是这个形状（轮数不多、每轮事件极多）。
 */
function setup(turns: number, status = 'completed', perTurn = 3) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const session = useSessionStore()
  const taskStore = useTaskStore()
  const bundleStore = useFeedbackBundleStore()

  taskStore.tasks.push({ id: TID, status } as never)

  // 每轮 1 条提示词 + (perTurn-2) 条过程 + 1 条正文。
  // perTurn 默认 3：轮数少、事件也少 → 判据两侧都不该触发截断。
  const events: AgentEvent[] = []
  const turnInfos: TurnInfoDto[] = []
  let seq = 1
  for (let t = 1; t <= turns; t++) {
    turnInfos.push(info(t, seq))
    events.push(ev(seq++, 'user_message', `做${t}`))
    for (let k = 0; k < perTurn - 2; k++) events.push(ev(seq++, 'tool_call'))
    events.push(ev(seq++, 'text_delta', `结果${t}`))
  }
  session.eventsBySession[TID] = events
  bundleStore.bundles[TID] = {
    task_id: TID,
    turns: turnInfos,
    cumulative: { files: 0, additions: 0, deletions: 0 },
    checkpoints: [],
  } satisfies FeedbackBundleDto

  const host = document.createElement('div')
  document.body.appendChild(host)
  const app = createApp({
    render: () => h(SessionTimeline, { taskId: TID, consumeForceScroll: () => false }),
  })
  app.use(pinia)
  app.mount(host)
  return { host, app, session, taskStore }
}

/** 已挂载的 Turn 分组序号（从 DOM 反推，不读内部状态） */
function mountedTurns(host: HTMLElement): number[] {
  return [...host.querySelectorAll('[data-testid^="turn-group-"]')]
    .map((n) => Number(n.getAttribute('data-testid')!.replace('turn-group-', '')))
    .sort((a, b) => a - b)
}

afterEach(() => {
  document.body.innerHTML = ''
})

describe('需求 2：只挂载最近两个 Turn', () => {
  it('短会话（2 轮、事件也不多）不截断 —— 省不下 DOM，别让用户白点一次', async () => {
    // 判据是 `groups.length`（本 fixture 无前导区，2 轮 ⇒ 2 组），
    // 只有「分组数 > MOUNT_WINDOW(2)」时才截断 ⇒ 2 轮整个挂载。
    // 事件数也必须留在 HEAVY_EVENT_THRESHOLD 之下，否则会命中"轮数少但很重"那条。
    const { host } = setup(2)
    await nextTick()
    expect(host.querySelector('[data-testid="load-older-turns"]')).toBeNull()
    expect(mountedTurns(host)).toEqual([1, 2])
  })

  it('3 轮（3 组）进入窗口，收起最早 1 轮', async () => {
    const { host } = setup(3)
    await nextTick()
    const more = host.querySelector<HTMLButtonElement>('[data-testid="load-older-turns"]')!
    expect(more).not.toBeNull()
    // window 从 groups.length - 2 = 1 开始 ⇒ 只挂 turn 2/3，turn 1 收起
    expect(more.textContent).toContain('更早的 1 轮')
    expect(mountedTurns(host)).toEqual([2, 3])
  })

  it('长会话（10 轮）只挂最后两轮，更早的收进「更早的 N 轮」', async () => {
    const { host } = setup(10)
    await nextTick()
    const more = host.querySelector<HTMLButtonElement>('[data-testid="load-older-turns"]')!
    expect(more).not.toBeNull()
    // 本 fixture 的事件流以 user_message 开头 ⇒ **没有 turn 0 前导组**，
    // 10 轮就是 10 个分组，窗口保留最后 2 ⇒ 收起 8。
    // （注意别照搬"含前导区就 +1"的经验：前导组只在首条 user_message 之前
    //   有系统事件时才存在，是**数据形状**决定的，不是轮数的函数。）
    expect(more.textContent).toContain('更早的 8 轮')
    expect(mountedTurns(host)).toEqual([9, 10])
    // 被收起的轮**不在 DOM 里**（这才是省下渲染的地方；折叠只藏过程、不省 DOM）
    expect(host.querySelector('[data-testid="turn-group-1"]')).toBeNull()
  })

  it('点「更早的 N 轮」→ 全部挂载，按钮消失', async () => {
    const { host } = setup(10)
    await nextTick()
    host.querySelector<HTMLButtonElement>('[data-testid="load-older-turns"]')!.click()
    await nextTick()
    expect(host.querySelector('[data-testid="load-older-turns"]')).toBeNull()
    expect(mountedTurns(host)).toEqual([1, 2, 3, 4, 5, 6, 7, 8, 9, 10])
  })

  it('轮数少但事件量极大（>160）同样截断 —— 判据是"重不重"，不是只看轮数', async () => {
    // 3 轮 × 每轮 80 条事件 = 240 条：属于"看着轮数少、实际很重"。
    // 只挂最后 2 轮（[2,3]），与轮数多时的窗口规则一致。
    const { host } = setup(3, 'completed', 80)
    await nextTick()

    const more = host.querySelector<HTMLButtonElement>('[data-testid="load-older-turns"]')!
    expect(more).not.toBeNull()
    expect(more.textContent).toContain('更早的 1 轮')
    expect(mountedTurns(host)).toEqual([2, 3])
  })

  it('正在读的轮不会被卸载：轨道跳转会先钉住该轮（否则目标节点根本不存在）', async () => {
    const { host } = setup(10)
    await nextTick()
    expect(mountedTurns(host)).toEqual([9, 10])

    // 轨道上的第 5 轮泡泡（点击 → 钉住 + 平滑滚动）
    const bubble = host.querySelector<HTMLButtonElement>('[data-testid="turn-rail-5"]')!
    expect(bubble).not.toBeNull()
    bubble.click()
    await nextTick()

    // 目标轮已补挂，且窗口没有把 9/10 换掉
    expect(mountedTurns(host)).toContain(5)
    expect(mountedTurns(host)).toContain(10)
  })

  it('平铺兜底（旧任务无 TurnInfo）不受影响', async () => {
    const pinia = createPinia()
    setActivePinia(pinia)
    const session = useSessionStore()
    const taskStore = useTaskStore()
    taskStore.tasks.push({ id: 'old', status: 'completed' } as never)
    session.eventsBySession['old'] = [
      { id: 'old:1', taskId: 'old', type: 'status', timestamp: new Date(0).toISOString(), payload: { text: '跑起来了' } },
      { id: 'old:2', taskId: 'old', type: 'text_delta', timestamp: new Date(0).toISOString(), payload: { text: '结果' } },
    ]
    const host = document.createElement('div')
    document.body.appendChild(host)
    createApp({ render: () => h(SessionTimeline, { taskId: 'old', consumeForceScroll: () => false }) })
      .use(pinia)
      .mount(host)
    await nextTick()
    expect(host.querySelector('[data-testid="load-older-turns"]')).toBeNull()
    expect(host.querySelector('[data-testid="session-timeline"]')!.textContent).toContain('结果')
  })
})

describe('需求 3：直达最新输出', () => {
  it('贴底（无滚动）时不出现', async () => {
    const { host } = setup(3)
    await nextTick()
    expect(host.querySelector('[data-testid="jump-latest"]')).toBeNull()
  })

  it('上拉离开底部后出现，点击后回到最新', async () => {
    const { host } = setup(3)
    await nextTick()
    const root = host.querySelector<HTMLElement>('[data-testid="session-timeline"]')!

    // jsdom 不做布局：给出几何让"贴底判定"能算出一个非贴底的结果
    Object.defineProperty(root, 'scrollHeight', { value: 2000, configurable: true })
    Object.defineProperty(root, 'clientHeight', { value: 600, configurable: true })
    root.scrollTop = 0 // 距底 1400px → 离开底部

    root.dispatchEvent(new Event('scroll'))
    await nextTick()
    const btn = host.querySelector<HTMLButtonElement>('[data-testid="jump-latest"]')!
    expect(btn).not.toBeNull()

    // 点击 → 平滑滚到底（jsdom 无 scrollTo → 降级为直接设 scrollTop）
    btn.click()
    await nextTick()
    expect(root.scrollTop).toBe(2000)
  })

  it('回到贴底后按钮消失（与底部跟随共用同一个阈值判据）', async () => {
    const { host } = setup(3)
    await nextTick()
    const root = host.querySelector<HTMLElement>('[data-testid="session-timeline"]')!
    Object.defineProperty(root, 'scrollHeight', { value: 2000, configurable: true })
    Object.defineProperty(root, 'clientHeight', { value: 600, configurable: true })

    root.scrollTop = 0
    root.dispatchEvent(new Event('scroll'))
    await nextTick()
    expect(host.querySelector('[data-testid="jump-latest"]')).not.toBeNull()

    root.scrollTop = 2000 // 贴底
    root.dispatchEvent(new Event('scroll'))
    await nextTick()
    expect(host.querySelector('[data-testid="jump-latest"]')).toBeNull()
  })
})

// ============================================================================
// 白屏回归：滚动处理不得挂在 window 上、也不得在滚动里重入
// ============================================================================

describe('白屏回归：滚动监听的作用域与重入', () => {
  /**
   * 曾经的元凶：`window.addEventListener('scroll', onScrollAll, true)`。
   * 捕获阶段的全窗口监听会在**任何**容器滚动时触发（输入框、反馈面板的 diff 列表…），
   * 于是每 tick 都跑一遍逐 Turn 的 getBoundingClientRect（强制同步布局）；
   * 12 轮就是每 tick 12 次，叠加"滚动中插入/卸载 Turn"就成了布局抖动 → 白屏。
   */
  it('滚动无关容器不触发时间线的高亮重算', async () => {
    const { host } = setup(10)
    await nextTick()

    // 造一个与时间线无关的可滚动容器，滚动它
    const other = document.createElement('div')
    other.style.overflowY = 'auto'
    document.body.appendChild(other)

    const root = host.querySelector<HTMLElement>('[data-testid="session-timeline"]')!
    let rootQueried = 0
    const realQS = root.querySelectorAll.bind(root)
    root.querySelectorAll = ((sel: string) => {
      rootQueried += 1
      return realQS(sel as never)
    }) as never

    other.dispatchEvent(new Event('scroll'))
    await nextTick()

    // 时间线自己的节点没被查询 = 没跑 trackActive
    expect(rootQueried).toBe(0)
  })

  it('点「直达最新输出」不重入滚动处理（否则按钮会闪一下又回来）', async () => {
    const { host } = setup(3)
    await nextTick()
    const root = host.querySelector<HTMLElement>('[data-testid="session-timeline"]')!
    Object.defineProperty(root, 'scrollHeight', { value: 2000, configurable: true })
    Object.defineProperty(root, 'clientHeight', { value: 600, configurable: true })

    root.scrollTop = 0
    root.dispatchEvent(new Event('scroll'))
    await nextTick()
    const btn = host.querySelector<HTMLButtonElement>('[data-testid="jump-latest"]')!
    expect(btn).not.toBeNull()

    // 点击后：jsdom 无 scrollTo → 直接贴底。button 的显隐交给后续 scroll 事件，
    // 不能在 click 里手动重算 —— 那会用"还没滚到"的几何把 atBottom 写回 false
    btn.click()
    await nextTick()
    expect(root.scrollTop).toBe(2000)

    // 模拟平滑滚动最终派发的 scroll（贴底）→ 按钮消失，且不会自己冒回来
    root.dispatchEvent(new Event('scroll'))
    await nextTick()
    expect(host.querySelector('[data-testid="jump-latest"]')).toBeNull()
  })
})
