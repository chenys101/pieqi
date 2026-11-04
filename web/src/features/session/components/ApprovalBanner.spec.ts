import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import ApprovalBanner from './ApprovalBanner.vue'
import type { ApprovalRequest } from '@/types/approval'

// 前瞻 Diff 不在本测范围；避免拉反馈面板的一串依赖。
vi.mock('@/features/feedback', () => ({
  ApprovalDiffCard: { name: 'ApprovalDiffCard', render: () => null },
}))

function decision(over: Partial<ApprovalRequest> = {}): ApprovalRequest {
  return {
    id: 'req-1',
    taskId: 'task-1',
    kind: 'approval',
    tool: 'Bash',
    summary: 'git status',
    options: ['approve', 'deny'],
    createdAt: '2026-01-01T00:00:00Z',
    ...over,
  }
}

function mount(props: { decision: ApprovalRequest; loading?: boolean }) {
  const host = document.createElement('div')
  document.body.appendChild(host)
  const app = createApp({ render: () => h(ApprovalBanner, props) })
  app.mount(host)
  return { host, app }
}

afterEach(() => {
  document.body.innerHTML = ''
  vi.restoreAllMocks()
})

describe('ApprovalBanner · 该审必见', () => {
  it('决策出现时渲染可点入口（批准/拒绝）并带 testid', async () => {
    vi.spyOn(console, 'info').mockImplementation(() => {})
    const { host } = mount({ decision: decision() })
    await nextTick()
    const root = host.querySelector('[data-testid="approval-banner"]')
    expect(root).not.toBeNull()
    expect(host.textContent).toContain('需决策：Bash')
    expect(host.textContent).toContain('批准')
    expect(host.textContent).toContain('拒绝')
  })

  it('挂载时滚动锚定 + decision_rendered 对照日志', async () => {
    const info = vi.spyOn(console, 'info').mockImplementation(() => {})
    const scrollIntoView = vi.fn()
    // jsdom 的 Element.prototype.scrollIntoView 缺省；挂上以便断言调用。
    Element.prototype.scrollIntoView = scrollIntoView

    mount({ decision: decision({ id: 'req-anchor', tool: 'Edit' }) })
    await nextTick()
    await nextTick()

    expect(info).toHaveBeenCalledWith(
      '[pieqi] decision_rendered',
      expect.objectContaining({
        decision_id: 'req-anchor',
        task_id: 'task-1',
        tool: 'Edit',
        kind: 'approval',
      }),
    )
    expect(scrollIntoView).toHaveBeenCalledWith({ block: 'nearest', behavior: 'smooth' })
  })
})
