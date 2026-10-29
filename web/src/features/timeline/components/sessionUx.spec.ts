import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, type Component } from 'vue'
import { createPinia } from 'pinia'
import TextBubble from './TextBubble.vue'
import MarkdownBody from './MarkdownBody.vue'
import TimelineEventView from './TimelineEventView.vue'
import SessionHeader from '@/features/session/components/SessionHeader.vue'
import InterveneInput from '@/features/session/components/InterveneInput.vue'
import type { AgentEvent } from '@/types/event'
import type { Task } from '@/types/task'

// 详情页 UI 优化 · 需求 2 / 需求 3 的出口条件：
//   需求 2：中止按钮只有一个（输入框内那枚），顶部头部不得再有第二个；
//   需求 3：Agent 文本输出按 markdown 渲染，且**必须消毒**（agent 产物不可信）。

// InterveneInput 内含 PromptInput → useAppStore（斜杠补全数据源）。这两条用例
// 与补全无关，直接 mock 掉，免得为一个展示性断言去搭 Pinia（composer.spec 同款做法）。
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ completions: { commands: [], skills: [] } }),
}))

// SessionHeader 的「返回」用 useRouter()。这些用例不点它，但组件挂载时就要注入，
// 缺了会刷一屏 injection "Symbol(router)" not found 警告（测试仍会过，只是噪音）。
vi.mock('vue-router', () => ({
  useRouter: () => ({ back: vi.fn(), push: vi.fn() }),
}))

function mount(comp: Component, props: Record<string, unknown> = {}) {
  const host = document.createElement('div')
  document.body.appendChild(host)
  const app = createApp({ render: () => h(comp, props) })
  // InterveneInput 在 setup 里就要 pinia（截图粘贴的运行中提示走通知 store），
  // 缺了它整个组件都挂不起来 —— 这类"顺带的依赖"必须在这里统一满足。
  app.use(createPinia())
  app.mount(host)
  return { host, app }
}

function mountIntervene(props: Record<string, unknown>) {
  return mount(InterveneInput, props)
}

afterEach(() => {
  document.body.innerHTML = ''
})

// ---- 需求 3：markdown 渲染 ----

describe('需求 3：Agent 输出 markdown 渲染', () => {
  it('标题 / 列表 / 粗体 / 代码块 → 渲染成真实 HTML 结构', async () => {
    const md = ['# 标题一', '', '- 甲', '- 乙', '', '**加粗** 与 `code`', '', '```js', 'const a = 1', '```'].join('\n')
    const { host } = mount(TextBubble, { text: md })
    await nextTick()
    const body = host.querySelector('[data-testid="markdown-body"]')!
    expect(body).not.toBeNull()
    expect(body.querySelector('h1')!.textContent).toBe('标题一')
    expect([...body.querySelectorAll('li')].map((n) => n.textContent)).toEqual(['甲', '乙'])
    expect(body.querySelector('strong')!.textContent).toBe('加粗')
    expect(body.querySelector('pre code')!.textContent).toContain('const a = 1')
  })

  it('纯文本退回 whitespace-pre-wrap（不吃 markdown 的段落块）', async () => {
    const { host } = mount(TextBubble, { text: '一句话回复，没有 markdown 标记。' })
    await nextTick()
    expect(host.querySelector('[data-testid="markdown-body"]')).toBeNull()
    const plain = host.querySelector('[data-testid="plain-body"]')!
    expect(plain.className).toContain('whitespace-pre-wrap')
    expect(plain.textContent).toBe('一句话回复，没有 markdown 标记。')
  })

  it('随手写的单个 * / 下划线文件名不误判成强调（判据保守）', async () => {
    const { host } = mount(TextBubble, { text: '路径是 src/features/timeline/a_b_c.ts，3 * 4 = 12' })
    await nextTick()
    expect(host.querySelector('[data-testid="markdown-body"]')).toBeNull()
    expect(host.querySelector('[data-testid="plain-body"]')!.textContent).toContain('a_b_c.ts')
  })

  it('XSS：<script> / onerror / javascript: 一律被消毒掉', async () => {
    const evil = [
      '<script>window.__pwned = 1</script>',
      '<img src=x onerror="window.__pwned = 2">',
      '[点我](javascript:window.__pwned=3)',
      '<a href="javascript:alert(1)">link</a>',
    ].join('\n\n')
    const { host } = mount(MarkdownBody, { text: evil })
    await nextTick()
    const body = host.querySelector('[data-testid="markdown-body"]')!
    // script 节点与事件处理器必须不存在
    expect(body.querySelector('script')).toBeNull()
    expect(body.innerHTML).not.toContain('onerror')
    // javascript: 协议被剥除（DOMPurify 会去掉 href 或留空）
    for (const a of body.querySelectorAll('a')) {
      expect(a.getAttribute('href') ?? '').not.toMatch(/^javascript:/i)
    }
    expect((window as unknown as { __pwned?: number }).__pwned).toBeUndefined()
  })

  it('流式：文本增长时重新渲染（末尾代码块闭合后结构才成立）', async () => {
    const host = document.createElement('div')
    document.body.appendChild(host)
    const text = { value: '```js\nconst a = 1\n' }
    const app = createApp({ render: () => h(TextBubble, { text: text.value }) })
    app.mount(host)
    // 未闭合围栏：marked 仍按代码块渲染（原文保留），不抛错
    expect(host.querySelector('[data-testid="markdown-body"]')).not.toBeNull()

    text.value = '```js\nconst a = 1\n```\n'
    app.unmount()
    const host2 = document.createElement('div')
    document.body.appendChild(host2)
    createApp({ render: () => h(TextBubble, { text: text.value }) }).mount(host2)
    await nextTick()
    expect(host2.querySelector('pre code')!.textContent).toContain('const a = 1')
  })

  it('GFM 表格与任务列表也能渲染', async () => {
    const md = ['| A | B |', '| - | - |', '| 1 | 2 |', '', '- [x] 完成项'].join('\n')
    const { host } = mount(MarkdownBody, { text: md })
    await nextTick()
    const body = host.querySelector('[data-testid="markdown-body"]')!
    expect(body.querySelector('table')).not.toBeNull()
    expect(body.querySelectorAll('th')).toHaveLength(2)
    expect(body.querySelector('input[type="checkbox"]')).not.toBeNull()
  })
})

// ---- 时间展示：提示词 / 结论的「时:分」在**内容下方** ----
//
// 定案来源：时间徽标原先是 Turn 头部两枚 + 气泡左侧一枚，2026-10 统一成
// 「跟着内容走、放在内容下方」。这里钉的是**结构契约**（DOM 顺序与 flex 方向），
// 因为 jsdom 不做布局：尺寸/是否真的在下方必须由 self-test.ps1 在真机上量
// （脚本已输出 prompt-at / result-at 的 dy、gap）。
describe('时间展示位置：内容下方', () => {
  function ev(type: AgentEvent['type'], text: string, at = '2026-10-13T08:07:00Z'): AgentEvent {
    return { id: `t:${type}`, taskId: 't', type, timestamp: at, payload: { text } }
  }

  it('用户消息：气泡在前、时间在后，容器是纵向 flex（下方靠右对齐）', async () => {
    const { host } = mount(TimelineEventView, { event: ev('user_message', '做A') })
    await nextTick()
    const row = host.firstElementChild as HTMLElement
    expect(row.className).toContain('flex-col')
    expect(row.className).toContain('items-end')

    const kids = [...row.children]
    expect(kids[0].getAttribute('data-testid')).toBe('user-message')
    expect(kids[1].getAttribute('data-testid')).toBe('prompt-at')
    // 时间文案本身就是事件的时间戳（本地时区渲染），不是渲染时刻
    expect(kids[1].textContent).toMatch(/^\d{2}:\d{2}$/)
    expect(kids[1].getAttribute('title')).toContain('发送于')
  })

  it('正文结论：气泡在前、时间在后，容器是纵向 flex（下方靠左对齐）', async () => {
    const { host } = mount(TimelineEventView, { event: ev('text_delta', '结果') })
    await nextTick()
    const row = host.firstElementChild as HTMLElement
    expect(row.className).toContain('flex-col')
    expect(row.className).toContain('items-start')

    const kids = [...row.children]
    expect(kids[0].getAttribute('data-testid')).toBe('text-bubble')
    expect(kids[1].getAttribute('data-testid')).toBe('result-at')
    expect(kids[1].getAttribute('title')).toContain('产出结论于')
  })

  it('过程事件（thinking / tool）不带时间戳 —— 一轮几十条会淹没时间线', async () => {
    for (const type of ['thinking_delta', 'tool_call', 'tool_result', 'status'] as const) {
      document.body.innerHTML = ''
      const { host } = mount(TimelineEventView, { event: ev(type, 'x') })
      await nextTick()
      expect(host.querySelector('[data-testid="prompt-at"]')).toBeNull()
      expect(host.querySelector('[data-testid="result-at"]')).toBeNull()
    }
  })

  it('时间戳缺失/非法时不渲染空徽标', async () => {
    const { host } = mount(TimelineEventView, { event: { ...ev('text_delta', '结果'), timestamp: '' } })
    await nextTick()
    expect(host.querySelector('[data-testid="result-at"]')).toBeNull()
    expect(host.textContent).toContain('结果')
  })
})

// ---- 需求 2：顶部中止按钮与输入框终止不重叠 ----

const baseTask = {
  id: 't1',
  title: '任务',
  project: 'p',
  prompt: 'x',
  status: 'running',
  createdAt: new Date(0).toISOString(),
  updatedAt: new Date(0).toISOString(),
} as unknown as Task

describe('需求 2：中止入口唯一', () => {
  it('头部不再有「中止」按钮（桌面档）', () => {
    const { host } = mount(SessionHeader, { task: baseTask, canCancel: true, showFeedback: true })
    expect(host.textContent).not.toContain('中止')
    // 反馈与删除仍在（只摘掉中止这一个）
    expect(host.textContent).toContain('反馈')
    expect(host.textContent).toContain('删除')
  })

  it('移动端 compact 头部同样没有「中止」图标按钮', () => {
    const { host } = mount(SessionHeader, { task: baseTask, canCancel: true, compact: true })
    expect(host.textContent).not.toContain('中止')
    // 移动端「删除」本来就不常驻
    expect(host.textContent).not.toContain('删除')
  })

  it('详情页头部展示本任务的 agent（需求：详情页要能看出跑在哪个 agent 上）', () => {
    // agent 业务名 → 展示名（目录未加载时由 store 的兜底目录给出 Claude Code）
    const header = mount(SessionHeader, {
      task: { ...baseTask, agent: 'claude' } as unknown as Task,
      canCancel: true,
    })
    const badge = header.host.querySelector('[data-testid="session-agent"]')
    expect(badge).not.toBeNull()
    expect(badge!.textContent).toContain('Claude Code')
    // 悬停说明带业务名：同名/改名的情形下这是唯一能对上的标识
    expect(badge!.getAttribute('title')).toContain('claude')
  })

  it('桌面元信息：身份线索靠左成组，用量贴右（不再挤在标题下方一小段）', () => {
    const header = mount(SessionHeader, {
      task: {
        ...baseTask,
        agent: 'claude',
        usage: { used: 42_000, size: 200_000, costUsd: 0.42, hasCost: true },
      } as unknown as Task,
      canCancel: true,
    })
    const meta = header.host.querySelector('[data-testid="session-meta"]')!
    expect(meta, '缺少桌面元信息左组').not.toBeNull()
    expect(meta.className).toContain('flex-1')
    expect(meta.textContent).toContain('#t1')
    expect(meta.textContent).toContain('Claude Code')
    // 用量是左组的**兄弟**，并且自己贴右；顺序反了会变成"用量跟在项目名后面"
    const usage = meta.nextElementSibling as HTMLElement | null
    expect(usage, '用量没有渲染在元信息行').not.toBeNull()
    expect(usage!.className).toContain('ml-auto')
    expect(usage!.textContent).toContain('42.0k/200k')
  })

  it('移动端：agent 不再占头部行，改为状态浮层里可见（输入条上另见 composer-agent）', async () => {
    const header = mount(SessionHeader, {
      task: { ...baseTask, agent: 'claude', status: 'running' } as unknown as Task,
      canCancel: true,
      compact: true,
    })
    // 旧的"执行中"agent 行已移除（方案 C：agent 下移到输入条）
    expect(header.host.querySelector('[data-testid="session-agent-compact"]')).toBeNull()

    // 状态点单独成键；点开是状态详情，agent 仍可在这里看到
    const status = header.host.querySelector<HTMLButtonElement>('[data-testid="session-status"]')!
    expect(status).not.toBeNull()
    expect(status.getAttribute('aria-label')).toContain('运行中')
    status.click()
    await nextTick()
    const pop = header.host.querySelector('[data-testid="session-status-pop"]')
    expect(pop).not.toBeNull()
    expect(pop!.textContent).toContain('运行中')
    expect(header.host.querySelector('[data-testid="session-status-agent"]')!.textContent).toContain('Claude Code')
  })

  it('移动端：状态点与任务列表是两个独立入口（不共用 ● 5）', () => {
    const header = mount(SessionHeader, {
      task: { ...baseTask, agent: 'claude', status: 'running' } as unknown as Task,
      canCancel: true,
      compact: true,
    })
    expect(header.host.querySelector('[data-testid="session-status"]')).not.toBeNull()
    // store 里没有其他任务 → 任务按钮不渲染；有任务时见 otherTasks.spec
    expect(header.host.querySelector('[data-testid="session-tasks"]')).toBeNull()
    // 旧的合并入口（hub）没有 testid，但名字也不该再出现在头部
    expect(header.host.textContent).not.toContain('状态与任务')
  })

  it('运行中的中止入口只剩输入框那一枚（emit cancel 一次）', () => {
    // 头部：**不点击**返回/删除（它们各自要 router / emit，与本用例无关），
    // 只收集"能触发中止"的按钮 —— 判据是 title 文案里有没有「中止」。
    const header = mount(SessionHeader, { task: baseTask, canCancel: true })
    const headerCancelish = [...header.host.querySelectorAll('button')].filter(
      (b) => (b.getAttribute('title') ?? '').includes('中止') || b.textContent?.includes('中止'),
    )
    expect(headerCancelish).toHaveLength(0)

    document.body.innerHTML = ''
    let fromComposer = 0
    const composer = mountIntervene({ canCancel: true, canSend: false, onCancel: () => (fromComposer += 1) })
    const cancelBtn = composer.host.querySelector<HTMLButtonElement>('button[aria-label="中止"]')!
    expect(cancelBtn).not.toBeNull()
    cancelBtn.click()
    expect(fromComposer).toBe(1)
    // 运行中不给发送按钮（双态互斥）
    expect(composer.host.querySelector('button[aria-label="发送"]')).toBeNull()
  })
})
