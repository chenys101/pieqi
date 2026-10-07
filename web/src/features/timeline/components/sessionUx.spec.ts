import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, type Component } from 'vue'
import TextBubble from './TextBubble.vue'
import MarkdownBody from './MarkdownBody.vue'
import SessionHeader from '@/features/session/components/SessionHeader.vue'
import InterveneInput from '@/features/session/components/InterveneInput.vue'
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
