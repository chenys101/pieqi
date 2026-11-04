import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import BottomNav from '@/components/ui/BottomNav.vue'
import AppSidebar from './AppSidebar.vue'
import { useTaskStore } from '@/stores/task'
import type { Task } from '@/types/task'

vi.mock('@/composables/useWebSocket', () => ({
  useWebSocket: () => ({ connection: { value: 'connected' } }),
}))

function waitingTask(id: string): Task {
  return {
    id,
    title: id,
    project: 'p',
    projectPath: '/p',
    prompt: 'x',
    status: 'waiting_input',
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
    decision: {
      id: `d-${id}`,
      taskId: id,
      kind: 'approval',
      tool: 'Bash',
      summary: 'ls',
      options: ['approve', 'deny'],
      createdAt: '2026-01-01T00:00:00Z',
    },
  } as unknown as Task
}

async function mountShell() {
  const pinia = createPinia()
  setActivePinia(pinia)
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: { template: '<div/>' } },
      { path: '/dashboard', component: { template: '<div/>' } },
      { path: '/tasks', component: { template: '<div/>' } },
      { path: '/settings', component: { template: '<div/>' } },
      { path: '/requirements', component: { template: '<div/>' } },
      { path: '/tasks/new', component: { template: '<div/>' } },
      { path: '/requirements/new', component: { template: '<div/>' } },
    ],
  })
  await router.push('/tasks')
  await router.isReady()

  const host = document.createElement('div')
  document.body.appendChild(host)
  const app = createApp({
    render: () => h('div', [h(BottomNav), h(AppSidebar)]),
  })
  app.use(pinia)
  app.use(router)
  app.mount(host)
  return { host, app }
}

afterEach(() => {
  document.body.innerHTML = ''
  vi.restoreAllMocks()
})

describe('待办计数 · 侧栏 / 底栏（ac2）', () => {
  it('有 CurrentDecision 时侧栏显示数字、底栏显示待办点', async () => {
    const { host } = await mountShell()
    useTaskStore().applySnapshot([waitingTask('a'), waitingTask('b')])
    await nextTick()

    const badge = host.querySelector('[data-testid="sidebar-pending-count"]')
    expect(badge).not.toBeNull()
    expect(badge!.textContent).toBe('2')

    const dot = host.querySelector('[data-testid="nav-pending-dot"]')
    expect(dot).not.toBeNull()
    expect(dot!.getAttribute('data-pending-count')).toBe('2')
    expect(dot!.getAttribute('aria-label')).toContain('2')
  })

  it('无待决策时不渲染角标', async () => {
    const { host } = await mountShell()
    await nextTick()
    expect(host.querySelector('[data-testid="sidebar-pending-count"]')).toBeNull()
    expect(host.querySelector('[data-testid="nav-pending-dot"]')).toBeNull()
  })
})
