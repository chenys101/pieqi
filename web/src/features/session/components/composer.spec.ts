import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, type Component } from 'vue'
import PromptInput from './PromptInput.vue'
import InterveneInput from './InterveneInput.vue'

// T2 出口条件：输入条与正文左边界同线 + 全站外壳规格（AC-R8-01~08）+ 斜杠补全/双态按钮不回退（AC-R8-09）。
// 补全源用 mock：这些断言与网络无关，别让数据源失败把 R8 的判据一起拖黑。
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    completions: {
      commands: [
        { name: 'help', description: '显示帮助' },
        { name: 'hello', description: '打个招呼' },
      ],
      skills: [{ name: 'heal', description: '修复' }],
    },
  }),
}))

function mount(comp: Component, props: Record<string, unknown> = {}) {
  const host = document.createElement('div')
  document.body.appendChild(host)
  const app = createApp({ render: () => h(comp, props) })
  app.mount(host)
  return { host, app }
}

afterEach(() => {
  document.body.innerHTML = ''
})

const ta = (host: HTMLElement) => host.querySelector('textarea')!

/** 模拟一次真实输入：值是新的、光标在末尾 —— 两者必须同源（见 detectQuery 注释） */
async function type(host: HTMLElement, value: string) {
  const t = ta(host)
  t.value = value
  t.setSelectionRange(value.length, value.length)
  t.dispatchEvent(new Event('input'))
  await nextTick()
  await nextTick()
}

/** 补全菜单容器（贴输入框上方那块）；未弹出时为 null */
const menu = (host: HTMLElement) => host.querySelector('[class*="bottom-full"]')
/** 菜单里的候选按钮（分组标题是 div 不是 button，别把两者混在一个计数里） */
const menuItems = (host: HTMLElement) =>
  menu(host) ? [...menu(host)!.querySelectorAll('button')].map((b) => b.textContent) : []
/** 分组标题（「命令」/「Skills」） */
const groupLabels = (host: HTMLElement) =>
  menu(host)
    ? [...menu(host)!.children].filter((c) => c.tagName === 'DIV').map((c) => c.textContent)
    : []

describe('PromptInput（输入条外壳）', () => {
  it('复用 Textarea 外壳：surface-subtle / --radius-sm / 3px ring / text-tertiary / hover', () => {
    const cls = ta(mount(PromptInput, { modelValue: '' }).host).className
    for (const t of [
      'bg-surface-subtle',
      'rounded-[var(--radius-sm)]',
      'focus-visible:ring-[3px]',
      'focus-visible:ring-accent/40',
      'placeholder:text-text-tertiary',
      'hover:border-border-strong',
    ])
      expect(cls, `缺少 ${t}`).toContain(t)
    // 旧的裸 textarea 写法（bg-background + rounded-lg + text-muted/60）不得残留
    expect(cls).not.toContain('bg-background')
    expect(cls).not.toContain('placeholder:text-muted/60')
  })

  it('输入条不给自己拖高（拖高会把时间线顶走）', () => {
    expect(ta(mount(PromptInput, { modelValue: '' }).host).className).toContain('resize-none')
    expect(ta(mount(PromptInput, { modelValue: '' }).host).className).not.toContain('resize-y')
  })

  it('disabled 透传到原生元素', () => {
    expect(ta(mount(PromptInput, { modelValue: '', disabled: true }).host).disabled).toBe(true)
  })

  it('Ctrl+Enter 提交', () => {
    const onSubmit = vi.fn()
    const { host } = mount(PromptInput, { modelValue: 'x', onSubmit })
    ta(host).dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', ctrlKey: true, bubbles: true }))
    expect(onSubmit).toHaveBeenCalledTimes(1)
  })
})

describe('PromptInput（斜杠补全）', () => {
  it('输入 / 弹出 Commands + Skills 分组', async () => {
    const { host } = mount(PromptInput, { modelValue: '' })
    expect(menu(host)).toBeNull()
    await type(host, '/')
    expect(groupLabels(host)).toEqual(['命令', 'Skills'])
    expect(menuItems(host)).toHaveLength(3)
  })

  it('查询词按**当前**光标计算，不慢一个字符', async () => {
    const { host } = mount(PromptInput, { modelValue: '' })
    await type(host, '/hel')
    expect(menuItems(host)).toHaveLength(2)
    // 若 detectQuery 读的是 props.modelValue（要等父组件重渲染），这里会仍停在 /hel = 2 条
    await type(host, '/hell')
    expect(menuItems(host)).toHaveLength(1)
    expect(menuItems(host)[0]).toMatch(/^\/hello/)
  })

  it('↑↓ + Enter 插入候选，并在其后留空格', async () => {
    const onUpdate = vi.fn()
    const { host } = mount(PromptInput, { modelValue: '', 'onUpdate:modelValue': onUpdate })
    await type(host, '/hell')
    ta(host).dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
    ta(host).dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
    expect(onUpdate).toHaveBeenCalledWith('/hello ')
  })

  it('Escape 关闭菜单', async () => {
    const { host } = mount(PromptInput, { modelValue: '' })
    await type(host, '/')
    expect(menuItems(host).length).toBeGreaterThan(0)
    ta(host).dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await nextTick()
    expect(menuItems(host)).toHaveLength(0)
  })

  it('无匹配（空格后）不弹菜单', async () => {
    const { host } = mount(PromptInput, { modelValue: '' })
    await type(host, '/hel x')
    expect(menuItems(host)).toHaveLength(0)
  })
})

describe('InterveneInput', () => {
  it('内层与正文同宽（max-w-3xl）且水平居中 —— AC-R8-01 的结构前提', () => {
    const { host } = mount(InterveneInput, { canCancel: false, canSend: true })
    const inner = host.firstElementChild!.firstElementChild!
    expect(inner.className).toContain('mx-auto')
    expect(inner.className).toContain('max-w-3xl')
    expect(inner.className).toContain('w-full')
    // 外条负责满宽 border-top / 底色，内层只管对齐
    expect(host.firstElementChild!.className).toContain('border-t')
  })

  it('双态按钮：视觉 36 但命中区外扩到 ≥44（after:-inset-1 = 4px×2）', () => {
    const { host } = mount(InterveneInput, { canCancel: false, canSend: true })
    const send = host.querySelector('button[aria-label="发送"]')!
    for (const t of ['relative', 'after:absolute', 'after:-inset-1', 'after:content-[\'\']', 'rounded-[var(--radius-sm)]', 'focus-visible:ring-[3px]'])
      expect(send.className, `缺少 ${t}`).toContain(t)

    document.body.innerHTML = ''
    const c = mount(InterveneInput, { canCancel: true, canSend: false })
    const cancel = c.host.querySelector('button[aria-label="中止"]')!
    expect(cancel.className).toContain('after:-inset-1')
    expect(c.host.querySelector('button[aria-label="发送"]')).toBeNull()
  })

  it('运行中：textarea disabled 且只给「中止」', () => {
    const onCancel = vi.fn()
    const { host } = mount(InterveneInput, { canCancel: true, canSend: false, onCancel })
    expect(ta(host).disabled).toBe(true)
    ;(host.querySelector('button[aria-label="中止"]') as HTMLButtonElement).click()
    expect(onCancel).toHaveBeenCalledTimes(1)
  })

  it('终态：空文本不可发送，有文本才 emit send 并清空', async () => {
    const onSend = vi.fn()
    const { host } = mount(InterveneInput, { canCancel: false, canSend: true, onSend })
    const send = () => host.querySelector('button[aria-label="发送"]') as HTMLButtonElement
    expect(send().disabled).toBe(true)

    await type(host, '补充一句')
    expect(send().disabled).toBe(false)
    send().click()
    expect(onSend).toHaveBeenCalledWith('补充一句')
  })

  it('canSend=false 时不可发送（决策横幅未就绪）', async () => {
    const { host } = mount(InterveneInput, { canCancel: false, canSend: false })
    await type(host, '文字')
    expect((host.querySelector('button[aria-label="发送"]') as HTMLButtonElement).disabled).toBe(true)
  })
})
