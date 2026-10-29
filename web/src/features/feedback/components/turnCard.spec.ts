// TurnCard / TurnSummary 的行为锁：
//   · 目录分组默认全展开（2026-10 需求）—— 不是"默认收起"；
//   · 「全部收起」用 groupReset 信号能连目录一起收；
//   · 筛选命中时强制展开、只留命中的文件；
//   · 文件行的「查看」走 artifacts store 的应用内查看层；
//   · 每轮总结条的按钮把 turn 抛给页面（三档落点由页面决定）。
import { createApp, h, nextTick, reactive } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, describe, expect, it } from 'vitest'
import TurnCard from './TurnCard.vue'
import TurnSummary from '@/features/session/components/TurnSummary.vue'
import { useArtifactsStore } from '@/stores/artifacts'
import type { ArtifactDto, TurnInfoDto } from '@/types/api'

const TID = 't1'

const changes = [
  { path: 'web/src/a.vue', operation: 'modify' as const, turn: 1, status: 'success' as const, additions: 2, deletions: 1 },
  { path: 'web/src/b.vue', operation: 'create' as const, turn: 1, status: 'success' as const, additions: 3, deletions: 0 },
  { path: 'internal/api/c.go', operation: 'modify' as const, turn: 1, status: 'success' as const, additions: 1, deletions: 0 },
]

const turn: TurnInfoDto = {
  turn: 1,
  start_event_seq: 1,
  user_prompt: '做 A',
  summary: { files: 3, additions: 6, deletions: 1 },
  changes,
}

const artifacts: ArtifactDto[] = changes.map((c) => ({
  path: c.path,
  name: c.path.split('/').pop()!,
  kind: 'code',
  open: `/api/tasks/${TID}/file?path=${encodeURIComponent(c.path)}`,
}))

let app: ReturnType<typeof createApp> | null = null
let host: HTMLElement | null = null

afterEach(() => {
  app?.unmount()
  host?.remove()
  app = null
  host = null
})

function mountCard(extra: Record<string, unknown> = {}) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const store = useArtifactsStore()
  store.byTask[TID] = artifacts

  host = document.createElement('div')
  document.body.appendChild(host)
  const state = reactive({
    expanded: true,
    filter: '',
    groupReset: { seq: 0, open: true },
    toggled: 0,
  })
  app = createApp({
    render: () =>
      h(TurnCard, {
        taskId: TID,
        turn,
        checkpointed: false,
        canRewind: true,
        artifacts: new Map(artifacts.map((a) => [a.path, a])),
        expanded: state.expanded,
        filter: state.filter,
        groupReset: state.groupReset,
        onToggle: () => (state.toggled += 1),
        ...extra,
      }),
  })
  app.use(pinia)
  app.mount(host)
  return { host, store, state }
}

function rows(el: HTMLElement) {
  return [...el.querySelectorAll<HTMLElement>('[data-file-row]')].map((r) => r.getAttribute('data-file-row'))
}

describe('TurnCard', () => {
  it('目录分组默认全展开（不是默认收起）', async () => {
    const { host: el } = mountCard()
    await nextTick()
    expect(el.querySelectorAll('[data-dir]').length).toBe(2)
    expect(rows(el).sort()).toEqual(['internal/api/c.go', 'web/src/a.vue', 'web/src/b.vue'])
  })

  it('点目录头只收这一组；再点恢复', async () => {
    const { host: el } = mountCard()
    await nextTick()
    el.querySelector<HTMLElement>('[data-dir="web/src"] [data-dir-toggle]')!.click()
    await nextTick()
    expect(rows(el)).toEqual(['internal/api/c.go'])
    el.querySelector<HTMLElement>('[data-dir="web/src"] [data-dir-toggle]')!.click()
    await nextTick()
    expect(rows(el)).toHaveLength(3)
  })

  it('groupReset 信号：连目录一起收 / 一起开', async () => {
    const { host: el, state } = mountCard()
    await nextTick()
    state.groupReset = { seq: 1, open: false }
    await nextTick()
    expect(rows(el)).toHaveLength(0)
    // 目录头仍在（收起 ≠ 消失，用户还能一组组点开）
    expect(el.querySelectorAll('[data-dir-toggle]').length).toBe(2)
    state.groupReset = { seq: 2, open: true }
    await nextTick()
    expect(rows(el)).toHaveLength(3)
  })

  it('expanded=false 时整块文件列表不渲染（受控收起）', async () => {
    const { host: el, state } = mountCard()
    await nextTick()
    state.expanded = false
    await nextTick()
    expect(rows(el)).toHaveLength(0)
    expect(el.querySelectorAll('[data-dir-toggle]').length).toBe(0)
  })

  it('筛选命中时强制展开，只留命中的文件行', async () => {
    const { host: el, state } = mountCard()
    await nextTick()
    state.filter = 'a.vue'
    await nextTick()
    expect(rows(el)).toEqual(['web/src/a.vue'])
    // 不匹配的目录整组不渲染
    expect(el.querySelectorAll('[data-dir]').length).toBe(1)
  })

  it('点「查看」走 artifacts store 的应用内查看层', async () => {
    const { host: el, store } = mountCard()
    await nextTick()
    el.querySelector<HTMLElement>('[data-view="web/src/a.vue"]')!.click()
    await nextTick()
    expect(store.viewing).toEqual({ taskId: TID, path: 'web/src/a.vue', kind: 'code' })
  })

  it('点 Turn 头抛 toggle（展开态由面板受控）', async () => {
    const { host: el, state } = mountCard()
    await nextTick()
    el.querySelector<HTMLElement>('button[aria-expanded]')!.click()
    await nextTick()
    expect(state.toggled).toBe(1)
  })
})

describe('TurnSummary', () => {
  it('显示本轮文件数与增删；点按钮把 turn 抛给页面', async () => {
    const pinia = createPinia()
    setActivePinia(pinia)
    host = document.createElement('div')
    document.body.appendChild(host)
    let focused = 0
    app = createApp({
      render: () =>
        h(TurnSummary, {
          turn: 7,
          summary: { files: 9, additions: 2008, deletions: 1872 },
          done: true,
          onFocus: (t: number) => (focused = t),
        }),
    })
    app.use(pinia)
    app.mount(host)
    await nextTick()

    const text = host.textContent!.replace(/\s+/g, ' ')
    expect(text).toContain('本轮完成')
    expect(text).toContain('9 个文件')
    expect(text).toContain('+2008')
    expect(text).toContain('-1872')

    host.querySelector<HTMLElement>('[data-testid="turn-summary-link"]')!.click()
    await nextTick()
    expect(focused).toBe(7)
  })

  it('运行中的轮次文案是「本轮进行中」', async () => {
    const pinia = createPinia()
    setActivePinia(pinia)
    host = document.createElement('div')
    document.body.appendChild(host)
    app = createApp({
      render: () =>
        h(TurnSummary, {
          turn: 2,
          summary: { files: 1, additions: 1, deletions: 0 },
          done: false,
        }),
    })
    app.use(pinia)
    app.mount(host)
    await nextTick()
    expect(host.textContent).toContain('本轮进行中')
  })
})
