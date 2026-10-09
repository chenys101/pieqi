// 上下文用量显示的单测：usageRatio 边界 + UsageBadge 渲染契约。
//
// ⚠️ 这里能验的只有"逻辑与 DOM 契约"（有没有渲染、文本对不对）。
// **尺寸、遮挡、进度条视觉宽度 jsdom 一律量不出来** —— 涉及那些的改动必须过
// web/scripts/self-test.ps1 真机验证（见 AGENTS.md 的 UI 验收纪律）。
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick, type Component } from 'vue'
import UsageBadge from './UsageBadge.vue'
import { usageRatio, type TaskUsage } from '@/types/task'

function usage(over: Partial<TaskUsage> = {}): TaskUsage {
  return { used: 50000, size: 200000, hasCost: false, ...over }
}

function mount(comp: Component, props: Record<string, unknown> = {}) {
  const host = document.createElement('div')
  document.body.appendChild(host)
  const app = createApp({ render: () => h(comp, props) })
  app.mount(host)
  return { host, app }
}

afterEach(() => {
  document.body.innerHTML = ''
})

describe('usageRatio', () => {
  it('常规比例', () => {
    expect(usageRatio(usage({ used: 50000, size: 200000 }))).toBe(0.25)
  })

  it('size<=0 / 未上报 → undefined（调用方据此隐藏，而不是显示 0%）', () => {
    expect(usageRatio(undefined)).toBeUndefined()
    expect(usageRatio(usage({ size: 0 }))).toBeUndefined()
    expect(usageRatio(usage({ size: -1 }))).toBeUndefined()
  })

  // agent 报的 used 可能因压缩/重算而短暂超过 size。进度条宽度超过 100% 会
  // 溢出容器（视觉上是那条紫线戳出圆角），必须夹住。
  it('used > size 夹到 1（不溢出容器）', () => {
    expect(usageRatio(usage({ used: 250000, size: 200000 }))).toBe(1)
  })

  it('负数夹到 0', () => {
    expect(usageRatio(usage({ used: -5, size: 200000 }))).toBe(0)
  })
})

describe('UsageBadge', () => {
  it('桌面形态：显示 used/size 紧凑数', async () => {
    const { host } = mount(UsageBadge, { usage: usage({ used: 12345, size: 200000 }) })
    await nextTick()
    expect(host.textContent).toContain('12.3k')
    expect(host.textContent).toContain('200k')
  })

  it('dense 形态（移动端）：只给百分比，不给 token 数', async () => {
    const { host } = mount(UsageBadge, { usage: usage({ used: 50000, size: 200000 }), dense: true })
    await nextTick()
    expect(host.textContent).toContain('25%')
    expect(host.textContent).not.toContain('50k')
  })

  // hasCost=false 时显示金额会把"agent 没报成本"读成"免费"。
  it('未报成本时不显示金额', async () => {
    const { host } = mount(UsageBadge, { usage: usage({ hasCost: false, costUsd: undefined }) })
    await nextTick()
    expect(host.textContent).not.toContain('$')
  })

  it('报了成本才显示金额', async () => {
    const { host } = mount(UsageBadge, { usage: usage({ hasCost: true, costUsd: 1.239 }) })
    await nextTick()
    expect(host.textContent).toContain('$1.24')
  })

  // 高水位转警示色：真到 90% 时下一轮往往就装不下了，警示要来得及被看见。
  it('≥85% 转警示色', async () => {
    const calm = mount(UsageBadge, { usage: usage({ used: 100000, size: 200000 }) })
    await nextTick()
    expect(calm.host.innerHTML).not.toContain('bg-warning')
    expect(calm.host.innerHTML).toContain('bg-accent')

    const hot = mount(UsageBadge, { usage: usage({ used: 180000, size: 200000 }) })
    await nextTick()
    expect(hot.host.innerHTML).toContain('bg-warning')
  })

  it('进度条宽度是夹住后的比例（不溢出容器）', async () => {
    const { host } = mount(UsageBadge, { usage: usage({ used: 250000, size: 200000 }) })
    await nextTick()
    // 内层填充条是唯一带 inline width 的元素
    const fill = host.querySelector('[style*="width"]')
    expect(fill).not.toBeNull()
    expect(fill!.getAttribute('style')).toContain('width: 100%')
  })

  it('title 给出完整数字（悬浮时能看到未取整的值）', async () => {
    const { host } = mount(UsageBadge, { usage: usage({ used: 12345, size: 200000 }) })
    await nextTick()
    const title = host.querySelector('[title]')!.getAttribute('title')!
    expect(title).toContain('12345 / 200000')
    expect(title).toContain('6.2%')
  })
})
