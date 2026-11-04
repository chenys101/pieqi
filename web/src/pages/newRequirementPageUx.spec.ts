import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { createApp, h, ref, type Component } from 'vue'
import { createPinia } from 'pinia'
import NewRequirementPage from './NewRequirementPage.vue'

// V2.0.1 · ac3：PC「新建需求」标题去箭头。
// 桌面侧栏已是导航 → 不渲染 ←；窄屏 compact 保留（对齐 Session 页心智）。

const isMobile = ref(false)

vi.mock('@/composables/useResponsive', () => ({
  useResponsive: () => ({ isMobile, isWide: ref(false) }),
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn(), back: vi.fn() }),
}))

vi.mock('@/services/api/settings', () => ({
  getSettings: vi.fn(async () => ({ default_project_path: '' })),
}))

vi.mock('@/services/api/requirements', () => ({
  createRequirement: vi.fn(),
}))

/** 模板有 RouterLink；单测不点它，注册桩即可消「Failed to resolve」噪音 */
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
    expect(host.querySelector('button[aria-label="返回"]')).toBeNull()
    expect(host.textContent).toContain('新建需求')
    app.unmount()
  })

  it('移动端 compact：保留返回箭头', () => {
    isMobile.value = true
    const { host, app } = mount(NewRequirementPage)
    const back = host.querySelector('[data-testid="new-requirement-back"]')
    expect(back).not.toBeNull()
    expect(back!.getAttribute('aria-label')).toBe('返回')
    app.unmount()
  })
})
