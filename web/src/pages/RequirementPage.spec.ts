/**
 * 需求详情 header「新建需求」快捷入口（ac6）。
 * 只测入口存在与 project query；不启服务。
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, ref } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import type { Requirement, RequirementOutcome } from '@/types/requirement'
import type { Task } from '@/types/task'

const routeParams = ref({ id: 'req-1' })
const routeQuery = ref<Record<string, string>>({})

vi.mock('vue-router', () => ({
  useRoute: () => ({
    get params() {
      return routeParams.value
    },
    get query() {
      return routeQuery.value
    },
    get path() {
      return `/requirements/${routeParams.value.id}`
    },
  }),
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
}))

vi.mock('@/services/api/requirements', () => ({
  getProposal: vi.fn(async () => null),
  suggestSplit: vi.fn(),
  confirmRequirement: vi.fn(),
  discardProposal: vi.fn(),
  expandRequirement: vi.fn(),
  markRequirementDone: vi.fn(),
  openIssue: vi.fn(),
  confirmIssueFix: vi.fn(),
  resolveIssue: vi.fn(),
  humanVerifyAC: vi.fn(),
  getRequirement: vi.fn(),
  getRequirementOutcome: vi.fn(),
  listTasksForRequirement: vi.fn(),
  listRequirements: vi.fn(),
}))

vi.mock('@/stores/agent', () => ({
  useAgentStore: () => ({
    catalog: [],
    defaultAgent: '',
    modelsOf: () => [],
    modelsLoaded: {},
    loadModels: vi.fn(),
    loadCatalog: vi.fn(async () => {}),
  }),
}))

vi.mock('@/stores/notification', () => ({
  useNotificationStore: () => ({
    success: vi.fn(),
    error: vi.fn(),
  }),
}))

const current = ref<Requirement | null>(null)
const outcome = ref<RequirementOutcome | null>(null)
const tasks = ref<Task[]>([])
const loading = ref(false)
const error = ref('')

vi.mock('@/stores/requirement', () => ({
  useRequirementStore: () => ({
    // 模拟 pinia 对 store 内 ref 的自动 unwrap
    get current() {
      return current.value
    },
    get outcome() {
      return outcome.value
    },
    get tasks() {
      return tasks.value
    },
    get loading() {
      return loading.value
    },
    get error() {
      return error.value
    },
    loadDetail: vi.fn(async () => {}),
    loadList: vi.fn(async () => {}),
  }),
}))

import RequirementPage from './RequirementPage.vue'

const RouterLinkStub = {
  name: 'RouterLink',
  props: { to: { type: [String, Object], required: true } },
  inheritAttrs: false,
  setup(
    props: { to: unknown },
    { attrs, slots }: { attrs: Record<string, unknown>; slots: { default?: () => unknown } },
  ) {
    return () =>
      h(
        'a',
        {
          ...attrs,
          'data-to': JSON.stringify(props.to),
        },
        slots.default?.(),
      )
  },
}

function mountPage() {
  const pinia = createPinia()
  setActivePinia(pinia)
  const host = document.createElement('div')
  document.body.appendChild(host)
  const app = createApp({ render: () => h(RequirementPage) })
  app.use(pinia)
  app.component('RouterLink', RouterLinkStub)
  app.mount(host)
  return { host, app }
}

afterEach(() => {
  document.body.innerHTML = ''
  current.value = null
  outcome.value = null
  tasks.value = []
  loading.value = false
  error.value = ''
})

beforeEach(() => {
  current.value = {
    id: 'req-1',
    title: '流程优化',
    project_path: 'D:\\code\\go\\pieqi',
    status: 'active',
    graph_version: 1,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    acceptance: [],
    issues: [],
  }
})

describe('RequirementPage header「新建需求」', () => {
  it('header 操作区有「＋ 新建需求」入口', async () => {
    const { host } = mountPage()
    await nextTick()
    const link = host.querySelector('[data-testid="requirement-new-requirement"]')
    expect(link, '缺少新建需求入口').not.toBeNull()
    expect(link!.textContent).toContain('新建需求')
    // 只在 header，正文预览区不得再复制一份
    expect(host.querySelectorAll('[data-testid="requirement-new-requirement"]')).toHaveLength(1)
  })

  it('入口 to 带上当前需求的 project_path（query.project）', async () => {
    const { host } = mountPage()
    await nextTick()
    const link = host.querySelector('[data-testid="requirement-new-requirement"]')!
    const to = JSON.parse(link.getAttribute('data-to') || 'null')
    expect(to).toEqual({
      path: '/requirements/new',
      query: { project: 'D:\\code\\go\\pieqi' },
    })
  })

  it('project_path 为空时仍可进新建页（省略 query）', async () => {
    current.value = { ...current.value!, project_path: '' }
    const { host } = mountPage()
    await nextTick()
    const link = host.querySelector('[data-testid="requirement-new-requirement"]')!
    const to = JSON.parse(link.getAttribute('data-to') || 'null')
    expect(to).toBe('/requirements/new')
  })
})
