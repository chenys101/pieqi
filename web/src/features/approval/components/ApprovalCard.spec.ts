// ApprovalCard：单 turn 免审按钮与文案纪律（V2.0.1 ac5）。
//
// 守住：
//   ① options 声明 approve_turn / approve_session 才出对应按钮；
//   ② 文案分清「本轮」vs「本会话」，禁止旧「同类免审」笼统文案；
//   ③ approve_same 绝不出现在可见按钮上；
//   ④ 点「本轮同类免审」走 store.approveTurn（不是 approveSession）。
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import ApprovalCard from './ApprovalCard.vue'
import type { ApprovalRequest } from '@/types/approval'

vi.mock('@/features/feedback', () => ({
  ApprovalDiffCard: { name: 'ApprovalDiffCard', render: () => null },
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn() }),
}))

const approve = vi.fn().mockResolvedValue(undefined)
const approveTurn = vi.fn().mockResolvedValue(undefined)
const approveSession = vi.fn().mockResolvedValue(undefined)
const deny = vi.fn().mockResolvedValue(undefined)

vi.mock('@/stores/approval', () => ({
  useApprovalStore: () => ({ approve, approveTurn, approveSession, deny }),
}))

function decision(over: Partial<ApprovalRequest> = {}): ApprovalRequest {
  return {
    id: 'req-1',
    taskId: 'task-1',
    kind: 'approval',
    tool: 'Bash',
    risk: 'L2',
    summary: 'go test ./internal/core',
    options: ['approve', 'deny', 'approve_turn', 'approve_session'],
    createdAt: '2026-11-04T00:00:00Z',
    ...over,
  }
}

let app: ReturnType<typeof createApp> | null = null
let host: HTMLElement | null = null

function mount(approval: ApprovalRequest) {
  const pinia = createPinia()
  setActivePinia(pinia)
  host = document.createElement('div')
  document.body.appendChild(host)
  app = createApp({ render: () => h(ApprovalCard, { approval }) })
  app.use(pinia)
  app.mount(host)
  return host
}

afterEach(() => {
  app?.unmount()
  host?.remove()
  app = null
  host = null
  approve.mockClear()
  approveTurn.mockClear()
  approveSession.mockClear()
  deny.mockClear()
})

describe('ApprovalCard · 单 turn 免审', () => {
  it('ACP options 齐全时同时露出「本轮」与「本会话」按钮，且文案不混淆', () => {
    const el = mount(decision())
    expect(el.querySelector('[data-testid="approval-approve-turn"]')!.textContent).toContain(
      '本轮同类免审',
    )
    expect(el.querySelector('[data-testid="approval-approve-session"]')!.textContent).toContain(
      '本会话同类免审',
    )
    // 旧笼统「同类免审」不得单独出现（会与本会话混淆）
    const labels = [...el.querySelectorAll('button')].map((b) => b.textContent?.trim() ?? '')
    expect(labels.some((t) => t === '同类免审')).toBe(false)
    expect(labels.some((t) => t.includes('approve_same'))).toBe(false)
    expect(el.textContent).not.toContain('approve_same')
  })

  it('options 无 approve_turn / approve_session 时不露出对应按钮（hook 路径）', () => {
    const el = mount(decision({ options: ['approve', 'deny'] }))
    expect(el.querySelector('[data-testid="approval-approve-turn"]')).toBeNull()
    expect(el.querySelector('[data-testid="approval-approve-session"]')).toBeNull()
    expect(el.querySelector('[data-testid="approval-approve"]')).not.toBeNull()
    expect(el.querySelector('[data-testid="approval-deny"]')).not.toBeNull()
  })

  it('仅有 approve_session 时不出本轮按钮', () => {
    const el = mount(decision({ options: ['approve', 'deny', 'approve_session'] }))
    expect(el.querySelector('[data-testid="approval-approve-turn"]')).toBeNull()
    expect(el.querySelector('[data-testid="approval-approve-session"]')).not.toBeNull()
  })

  it('点「本轮同类免审」调用 approveTurn，不误调 approveSession', async () => {
    const el = mount(decision())
    el.querySelector<HTMLButtonElement>('[data-testid="approval-approve-turn"]')!.click()
    await nextTick()
    expect(approveTurn).toHaveBeenCalledWith('task-1')
    expect(approveSession).not.toHaveBeenCalled()
    expect(approve).not.toHaveBeenCalled()
  })

  it('点「本会话同类免审」调用 approveSession', async () => {
    const el = mount(decision())
    el.querySelector<HTMLButtonElement>('[data-testid="approval-approve-session"]')!.click()
    await nextTick()
    expect(approveSession).toHaveBeenCalledWith('task-1')
    expect(approveTurn).not.toHaveBeenCalled()
  })

  it('L3 点本轮免确认条，文案写「本轮」而非「本会话」', async () => {
    const el = mount(decision({ risk: 'L3', summary: 'rm -rf build' }))
    el.querySelector<HTMLButtonElement>('[data-testid="approval-approve-turn"]')!.click()
    await nextTick()
    const confirm = el.querySelector('[data-testid="approval-l3-confirm"]')
    expect(confirm).not.toBeNull()
    expect(confirm!.textContent).toContain('本轮免审')
    expect(confirm!.textContent).not.toContain('本会话免审')
    // 尚未真正提交
    expect(approveTurn).not.toHaveBeenCalled()
  })
})
