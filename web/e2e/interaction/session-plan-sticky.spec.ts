// 会话页计划 sticky（Task.plan → composer 上方 PlanList）。
//
// 覆盖 create_plan / todo 抬升后的**可见契约**：
//   ① sticky 真挂在 SessionPage（不是时间线里某张卡）
//   ② 几何上在输入框**上方**（对齐 dsh TodoPanel dock）
//   ③ 条目内容来自 Task.plan（夹具里写死的三条）
//
// 为什么必须走 sessionpage 而不是默认 detail：
// 默认夹具 `mode=session` 只挂 SessionTimeline+InterveneInput，**没有** sticky 槽；
// 生产路径是 SessionPage，夹具用 `?mode=sessionpage` 对准那条。

import { test, expect } from '@playwright/test'
import { stabilize } from '../lib/measure'

// 与 c-header / new-task-entry 同口径：直接带 id，避免依赖 beforeEach 改写
// `?mode=sessionpage`（改写失败时 main 空、页面停在「任务不存在」）。
const SESSION_PAGE = '/__e2e/detail/e2e-long-12/sessionpage'

test.describe('session plan sticky', () => {
  test('composer 上方锚定计划清单，条目可读', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await page.goto(SESSION_PAGE, { waitUntil: 'domcontentloaded' })
    await stabilize(page)

    const sticky = page.locator('[data-testid="session-plan-sticky"]')
    await expect(sticky, 'SessionPage 未渲染 session-plan-sticky（Task.plan 没进夹具/没挂槽）').toBeVisible()

    const list = sticky.locator('[data-testid="plan-list"]')
    await expect(list).toBeVisible()
    await expect(list).toContainText('跳过 .pieqi 脏文件复制')
    await expect(list).toContainText('审批超时强制清 CurrentDecision')
    await expect(list).toContainText('配置弃用告警改为单次')

    const geo = await page.evaluate(() => {
      const s = document.querySelector('[data-testid="session-plan-sticky"]') as HTMLElement | null
      const ta = document.querySelector('textarea[aria-label="补充指令"]') as HTMLElement | null
      if (!s || !ta) return { ok: false as const, why: !s ? 'no-sticky' : 'no-textarea' }
      const sr = s.getBoundingClientRect()
      const tr = ta.getBoundingClientRect()
      return {
        ok: true as const,
        stickyBottom: Math.round(sr.bottom),
        taTop: Math.round(tr.top),
        stickyH: Math.round(sr.height),
      }
    })
    expect(geo.ok, `量几何失败：${'why' in geo ? geo.why : ''}`).toBe(true)
    if (!geo.ok) return
    expect(
      geo.stickyBottom,
      `计划 sticky 底边 ${geo.stickyBottom} 应 ≤ 输入框顶 ${geo.taTop}（必须锚在输入框上方）`,
    ).toBeLessThanOrEqual(geo.taTop + 2)
    expect(geo.stickyH, 'sticky 高度异常（空壳/被折叠成 0）').toBeGreaterThan(24)
  })
})
