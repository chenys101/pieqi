import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick, type Component } from 'vue'
import { createPinia } from 'pinia'
import TextBubble from './TextBubble.vue'
import MarkdownBody from './MarkdownBody.vue'
import TimelineEventView from './TimelineEventView.vue'
import type { AgentEvent } from '@/types/event'

// 详情页 UI 优化 · 需求 3 的出口条件（**timeline 域**）：
//   Agent 文本输出按 markdown 渲染，且**必须消毒**（agent 产物不可信）。
//
// 同文件原先还混着「中止入口唯一」等 SessionHeader / InterveneInput 的用例，
// 已按实现归属拆到 `features/session/components/sessionHeaderUx.spec.ts`。

function mount(comp: Component, props: Record<string, unknown> = {}) {
  const host = document.createElement('div')
  document.body.appendChild(host)
  const app = createApp({ render: () => h(comp, props) })
  // TimelineEventView 里的 RewindCard 会用到通知等 store，统一在这里注入 pinia。
  app.use(createPinia())
  app.mount(host)
  return { host, app }
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

