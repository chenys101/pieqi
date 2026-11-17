import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, type App } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import PlannedStartPanel from './PlannedStartPanel.vue'
import { useAgentStore } from '@/stores/agent'
import { useTaskStore } from '@/stores/task'
import { useNotificationStore } from '@/stores/notification'
import type { Task } from '@/types/task'

vi.mock('@/services/api/tasks', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/services/api/tasks')>()),
  startPlannedTask: vi.fn(),
}))

vi.mock('@/services/api/agents', () => ({
  getAgents: vi.fn(async () => ({
    default: 'claude',
    agents: [
      { name: 'claude', display_name: 'Claude Code', transport: 'SDK Bridge' },
      { name: 'qoder', display_name: 'Qoder CLI', transport: 'ACP' },
    ],
  })),
  getAgentModels: vi.fn(async () => ({ models: [], modes: [] })),
}))

import * as tasksApi from '@/services/api/tasks'

const mounted: App[] = []

function mount(props: Record<string, unknown> = {}) {
  const host = document.createElement('div')
  document.body.appendChild(host)
  const pinia = createPinia()
  setActivePinia(pinia)
  const app = createApp({
    render: () =>
      h(PlannedStartPanel, {
        taskId: 't-planned',
        agent: '',
        model: '',
        docked: false,
        ...props,
      }),
  })
  app.use(pinia)
  app.mount(host)
  mounted.push(app)
  return { host, pinia }
}

afterEach(() => {
  for (const app of mounted.splice(0)) app.unmount()
  document.body.innerHTML = ''
  vi.mocked(tasksApi.startPlannedTask).mockReset()
})

beforeEach(() => {
  setActivePinia(createPinia())
})

function startedTask(over: Partial<Task> = {}): Task {
  return {
    id: 't-planned',
    title: '实现登录',
    prompt: '实现登录',
    project: 'app',
    projectPath: 'D:/app',
    status: 'pending',
    agent: 'claude',
    model: '',
    mode: '',
    modes: [],
    sessionId: 't-planned',
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:01:00Z',
    interventions: [],
    ...over,
  }
}

describe('PlannedStartPanel', () => {
  it('缺 agent 时禁用启动并提示对齐后端文案', async () => {
    const { host } = mount({ agent: '' })
    await nextTick()
    const btn = host.querySelector('[data-testid="planned-start-btn"]') as HTMLButtonElement
    expect(btn.disabled).toBe(true)
    const hint = host.querySelector('[data-testid="planned-agent-required"]')
    expect(hint?.textContent).toContain('agent required before start')
  })

  it('已有 agent 时可一键启动', async () => {
    vi.mocked(tasksApi.startPlannedTask).mockResolvedValue(startedTask())
    const { host } = mount({ agent: 'claude', model: '' })
    const agentStore = useAgentStore()
    agentStore.catalog = [
      {
        id: 'claude',
        name: 'Claude Code',
        transport: 'SDK Bridge',
        capabilities: [],
      },
    ]
    await nextTick()
    const btn = host.querySelector('[data-testid="planned-start-btn"]') as HTMLButtonElement
    expect(btn.disabled).toBe(false)
    btn.click()
    await nextTick()
    await nextTick()
    expect(tasksApi.startPlannedTask).toHaveBeenCalledWith('t-planned', {
      agent: 'claude',
      model: undefined,
    })
    const taskStore = useTaskStore()
    expect(taskStore.byId('t-planned')?.status).toBe('pending')
  })

  it('选 agent 后可启动并带上 model', async () => {
    vi.mocked(tasksApi.startPlannedTask).mockResolvedValue(
      startedTask({ agent: 'qoder', model: 'Qwen3.8-Flash' }),
    )
    const { host } = mount({ agent: '' })
    const agentStore = useAgentStore()
    agentStore.catalog = [
      { id: 'claude', name: 'Claude Code', transport: 'SDK Bridge', capabilities: [] },
      { id: 'qoder', name: 'Qoder CLI', transport: 'ACP', capabilities: [] },
    ]
    await nextTick()

    const selectWrap = host.querySelector('[data-testid="planned-agent-select"]')
    const select = selectWrap?.querySelector('select') as HTMLSelectElement
    expect(select).toBeTruthy()
    select.value = 'qoder'
    select.dispatchEvent(new Event('change'))
    await nextTick()

    const btn = host.querySelector('[data-testid="planned-start-btn"]') as HTMLButtonElement
    expect(btn.disabled).toBe(false)
    btn.click()
    await nextTick()
    await nextTick()
    expect(tasksApi.startPlannedTask).toHaveBeenCalledWith('t-planned', {
      agent: 'qoder',
      model: undefined,
    })
  })

  it('启动失败时 toast 展示后端错误', async () => {
    vi.mocked(tasksApi.startPlannedTask).mockRejectedValue(new Error('agent required before start'))
    const { host } = mount({ agent: 'claude' })
    const agentStore = useAgentStore()
    agentStore.catalog = [
      { id: 'claude', name: 'Claude Code', transport: 'SDK Bridge', capabilities: [] },
    ]
    await nextTick()
    const btn = host.querySelector('[data-testid="planned-start-btn"]') as HTMLButtonElement
    btn.click()
    // onStart 是 async：等 rejection 落到 catch 再断言 toast
    await vi.waitFor(() => {
      const notify = useNotificationStore()
      expect(notify.toasts.some((t) => t.message.includes('agent required before start'))).toBe(true)
    })
  })
})
