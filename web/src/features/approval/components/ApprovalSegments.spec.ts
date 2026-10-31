import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h } from 'vue'
import ApprovalSegments from './ApprovalSegments.vue'
import type { ApprovalSegment } from '@/types/approval'

function mount(segments?: ApprovalSegment[]) {
  const host = document.createElement('div')
  document.body.appendChild(host)
  createApp({ render: () => h(ApprovalSegments, { segments }) }).mount(host)
  return host
}

afterEach(() => {
  document.body.innerHTML = ''
})

describe('ApprovalSegments', () => {
  it('少于 2 段不渲染', () => {
    const host = mount([{ text: 'go test', kind: 'read', risk: 'L0' }])
    expect(host.querySelector('[data-testid="approval-segments"]')).toBeNull()
  })

  it('复合命令标出需关注段', () => {
    const host = mount([
      { text: 'Remove-Item build', kind: 'delete', risk: 'L3' },
      { text: 'npx vitest run', kind: 'read', risk: 'L0' },
    ])
    const root = host.querySelector('[data-testid="approval-segments"]')
    expect(root).not.toBeNull()
    const items = root!.querySelectorAll('li')
    expect(items).toHaveLength(2)
    expect(items[0].textContent).toContain('L3')
    expect(items[0].className).toMatch(/warning/)
    expect(items[1].textContent).toContain('npx vitest')
  })
})
