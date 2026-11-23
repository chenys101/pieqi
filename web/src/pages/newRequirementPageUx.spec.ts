import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { createApp, h, ref, type Component } from 'vue'
import { createPinia } from 'pinia'
import NewRequirementPage from './NewRequirementPage.vue'

const isMobile = ref(false)

vi.mock('@/composables/useResponsive', () => ({
  useResponsive: () => ({ isMobile, isWide: ref(false) }),
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn(), back: vi.fn() }),
}))

vi.mock('@/services/api/settings', () => ({
  getSettings: vi.fn(async () => ({ default_project_path: '', default_workflow_id: '' })),
}))

vi.mock('@/services/api/workflows', () => ({
  listWorkflows: vi.fn(async () => ({
    workflows: [{ id: 's1-plan-serial', label: 'S1', version: '1', is_default: true }],
    default_id: 's1-plan-serial',
  })),
}))

vi.mock('@/services/api/requirements', () => ({
  createRequirement: vi.fn(),
}))

const RouterLinkStub = {
  name: 'RouterLink',
  props: { to: { type: [String, Object], required: false } },
  setup(_: unknown, { slots }: { slots: { default?: () => unknown } }) {
    return () => h('a', slots.default?.())
  },
}

function mount(comp: Component, props: Record<string, unknown> = {}) {
  const host = document.createElement('div')
  document.body.appendChild(host)
  const app = createApp({ render: () => h(comp, props) })
  app.use(createPinia())
  app.component('RouterLink', RouterLinkStub)
  app.mount(host)
  return { host, app }
}

describe('ac3：新建需求页返回箭头按视口', () => {
  beforeEach(() => {
    isMobile.value = false
    document.body.innerHTML = ''
  })

  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('桌面（非 compact）：标题最左不渲染返回箭头', () => {
    isMobile.value = false
    const { host, app } = mount(NewRequirementPage)
    expect(host.querySelector('[data-testid="new-requirement-back"]')).toBeNull()
    expect(host.textContent).toContain('新建需求')
    app.unmount()
  })

  it('移动端 compact：保留返回箭头', () => {
    isMobile.value = true
    const { host, app } = mount(NewRequirementPage)
    const back = host.querySelector('[data-testid="new-requirement-back"]')
    expect(back).not.toBeNull()
    app.unmount()
  })
})

describe('新建需求材料门禁与入口分立', () => {
  beforeEach(() => {
    isMobile.value = false
    document.body.innerHTML = ''
  })

  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('空表单主按钮是「创建需求并启动 PE」且因缺材料禁用', async () => {
    const { host, app } = mount(NewRequirementPage)
    await Promise.resolve()
    await Promise.resolve()
    const btn = host.querySelector('[data-testid="new-requirement-submit"]') as HTMLButtonElement
    expect(btn, '缺少 sticky 创建按钮').not.toBeNull()
    expect(btn.textContent?.trim()).toBe('创建需求并启动 PE')
    expect(btn.disabled).toBe(true)
    // 空表优先拦标题；材料门禁在有标题+cwd 后才会冒出
    expect(host.querySelector('[data-testid="new-requirement-block-reason"]')?.textContent).toContain('标题')
    expect(host.textContent).not.toContain('S0 短路径')
    expect(host.textContent).toContain('新建任务')
    app.unmount()
  })
})
