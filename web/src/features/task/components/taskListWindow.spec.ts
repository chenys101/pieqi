// 任务列表「单项目只渲染最近 N 个 + 加载更多」。
//
// 出口条件（对应这次的性能优化）：
//   1. 默认只渲染最近 TASKS_PER_PROJECT 个（g.tasks 已按 updatedAt 倒序）；
//   2. 计数 / 状态聚合**仍按全量**，否则会出现「项目写着 12 个、只列出 4 个」的自相矛盾；
//   3. 点「加载更多」后全部渲染，且这个状态进 store —— 侧栏与移动端共用一份；
//   4. 筛选期间**不截断**：搜索命中的任务不能被留在「加载更多」后面（那等于搜不到）。
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import TaskBrowserGroup from './TaskBrowserGroup.vue'
import { TASKS_PER_PROJECT } from '../types'
import type { BrowserGroup } from '../types'
import type { Task } from '@/types/task'
import { useTaskTreeStore } from '@/stores/taskTree'

vi.mock('vue-router', () => ({
  RouterLink: { props: ['to'], template: '<a><slot /></a>' },
}))

function task(id: string, over: Partial<Task> = {}): Task {
  return {
    id,
    title: `任务 ${id}`,
    prompt: 'p',
    project: 'erp',
    projectPath: 'G:/ws/erp',
    status: 'completed',
    agent: 'claude',
    model: '',
    sessionId: 's1',
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
    interventions: [],
    ...over,
  }
}

/** 构造一个 BrowserGroup（截断逻辑在页面里，这里只测组件契约） */
function group(over: Partial<BrowserGroup> = {}): BrowserGroup {
  const tasks = Array.from({ length: 7 }, (_, i) => task(`t${i}`))
  const visible = tasks.slice(0, TASKS_PER_PROJECT)
  return {
    key: 'g:/ws/erp',
    name: 'erp',
    path: 'G:/ws/erp',
    tasks,
    visible,
    hiddenCount: tasks.length - visible.length,
    open: true,
    dim: false,
    agg: '7 已完成',
    ...over,
  }
}

function mount(g: BrowserGroup) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const host = document.createElement('div')
  document.body.appendChild(host)
  const app = createApp({ render: () => h(TaskBrowserGroup, { group: g }) })
  app.use(pinia)
  app.mount(host)
  return { host, app, tree: useTaskTreeStore() }
}

afterEach(() => {
  document.body.innerHTML = ''
})

describe('单项目截断渲染', () => {
  it('默认只渲染 visible，不渲染全部（这是这次优化的本体）', async () => {
    const { host } = mount(group())
    await nextTick()
    // 7 个任务，只挂 4 行
    expect(host.querySelectorAll('.tb-item')).toHaveLength(TASKS_PER_PROJECT)
    expect(host.textContent).not.toContain('任务 t6')
  })

  it('项目行的数字仍报全量 —— 不能出现「写着 7 个、只列 4 个」自相矛盾', async () => {
    const { host } = mount(group())
    await nextTick()
    const head = host.querySelector('.tb-head')!
    expect(head.textContent).toContain('7')
  })

  it('「加载更多」报出剩余条数，点击后 emit expand', async () => {
    const onExpand = vi.fn()
    const pinia = createPinia()
    setActivePinia(pinia)
    const host = document.createElement('div')
    document.body.appendChild(host)
    createApp({
      render: () => h(TaskBrowserGroup, { group: group(), onExpand }),
    })
      .use(pinia)
      .mount(host)
    await nextTick()

    const more = host.querySelector<HTMLButtonElement>('[data-testid="group-more-g:/ws/erp"]')!
    expect(more).not.toBeNull()
    expect(more.textContent).toContain('还有 3 个')
    more.click()
    expect(onExpand).toHaveBeenCalledTimes(1)
  })

  it('没有剩余时不出「加载更多」', async () => {
    const tasks = [task('a'), task('b')]
    const { host } = mount(group({ tasks, visible: tasks, hiddenCount: 0 }))
    await nextTick()
    expect(host.querySelector('[data-testid^="group-more-"]')).toBeNull()
  })
})

describe('展开态存在 store（两个容器共用一份）', () => {
  it('expandProject 置位；setAllProjects(false) 时复位', () => {
    const pinia = createPinia()
    setActivePinia(pinia)
    const tree = useTaskTreeStore()

    expect(tree.isProjectExpanded('k')).toBe(false)
    tree.expandProject('k')
    expect(tree.isProjectExpanded('k')).toBe(true)

    // 收起全部 → 「加载更多」也复位，否则再次展开会直接摊开全部任务，
    // 与「展开全部」的字面承诺（展开一层）不符
    tree.setProjectOpen('k', true)
    tree.setAllProjects(['k'], false)
    expect(tree.isProjectExpanded('k')).toBe(false)
  })

  it('收起/展开项目不影响「加载更多」状态（两件事）', () => {
    const pinia = createPinia()
    setActivePinia(pinia)
    const tree = useTaskTreeStore()
    tree.expandProject('k')
    tree.toggleProject('k') // 展开项目
    tree.toggleProject('k') // 再收起
    // 用户"我要看全部"的意图不该被项目折叠吞掉
    expect(tree.isProjectExpanded('k')).toBe(true)
  })
})
