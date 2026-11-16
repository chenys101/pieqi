/**
 * SplitBlockedPanel：保存 emit 字段（分支 / AC 行 / body / resuggest）。
 */
import { describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import SplitBlockedPanel from './SplitBlockedPanel.vue'

function mount(props: Record<string, unknown>, on: Record<string, (...a: unknown[]) => void> = {}) {
  const host = document.createElement('div')
  document.body.appendChild(host)
  const app = createApp({
    render: () =>
      h(SplitBlockedPanel, {
        ...props,
        onSave: on.save,
        onDiscard: on.discard,
      }),
  })
  app.mount(host)
  return {
    host,
    unmount: () => {
      app.unmount()
      host.remove()
    },
  }
}

describe('SplitBlockedPanel', () => {
  it('保存并重新拆分：emit 分支、AC 行、body、resuggest=true', async () => {
    const save = vi.fn()
    const { host, unmount } = mount(
      {
        mode: 'gate',
        candidateBranch: 'feat/auth',
        body: '旧说明',
        acceptance: [],
      },
      { save },
    )
    await nextTick()
    const branch = host.querySelector('[data-testid="split-polish-branch"]') as HTMLInputElement
    const ac = host.querySelector('[data-testid="split-polish-ac"]') as HTMLTextAreaElement
    const body = host.querySelector('[data-testid="split-polish-body"]') as HTMLTextAreaElement
    expect(branch.value).toBe('feat/auth')

    branch.value = 'feat/login'
    branch.dispatchEvent(new Event('input'))
    ac.value = '登录成功\n刷新失败跳登录'
    ac.dispatchEvent(new Event('input'))
    body.value = '新说明'
    body.dispatchEvent(new Event('input'))
    await nextTick()

    ;(host.querySelector('[data-testid="split-polish-save-resuggest"]') as HTMLButtonElement).click()
    await nextTick()

    expect(save).toHaveBeenCalledTimes(1)
    const payload = save.mock.calls[0][0] as {
      integration_branch: string
      body: string
      acceptance: Array<{ id: string; text: string }>
      resuggest: boolean
    }
    expect(payload.integration_branch).toBe('feat/login')
    expect(payload.body).toBe('新说明')
    expect(payload.resuggest).toBe(true)
    expect(payload.acceptance.map((a) => a.text)).toEqual(['登录成功', '刷新失败跳登录'])
    expect(payload.acceptance[0].id).toBe('ac1')
    unmount()
  })
})
