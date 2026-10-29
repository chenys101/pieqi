import { describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, type Component } from 'vue'
import { createPinia } from 'pinia'
import SessionHeader from './SessionHeader.vue'
import InterveneInput from './InterveneInput.vue'
import type { Task } from '@/types/task'

// 详情页 UI 优化 · 需求 2 的出口条件（**属于 session 域**）：
//   中止按钮只有一个（输入框内那枚），顶部头部不得再有第二个。
//
// 原先这几条与 timeline 的 Markdown / 时间戳用例混在
// `features/timeline/components/sessionUx.spec.ts` 里 —— 测试与实现分居两个域，
// 改 session 头部时要先去 timeline 目录找测试。按实现归属拆开。

// InterveneInput 内含 PromptInput → useAppStore（斜杠补全数据源）。这些用例
// 与补全无关，直接 mock 掉，免得为一个展示性断言去搭完整 store。
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
