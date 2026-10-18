// 视觉回归（`toHaveScreenshot`）：**机器判"变了没有"，模型不参与**。
//
// 为什么这一层能解决"重复截图"：
//   · 基线图存在仓库里（`__screenshots__/`，进 git），每次改动自动 diff；
//   · 失败只产出 expected/actual/diff 三张图，而且**不喂给模型** ——
//     只有"这个差异是设计变更还是 bug"这种需要判断的问题，才由人（或 vision 旁路）
//     看**一张** crop，而不是整页三张；
//   · 通过的用例零产物（screenshot: 'only-on-failure'）。
//
// 稳定性三件套全部在此生效：关动画（stabilize）、固定夹具（/__e2e/detail 用同一份数据）、
// 串行 worker（playwright.config.ts 的 visual project）。
//
// 更新基线：`npm run test:e2e:update-snapshots` —— 审过 diff 之后才跑。

import { test, expect } from '@playwright/test'
import { stabilize, VIEWPORTS } from '../lib/measure'

const SCENES = [
  { name: 'detail', url: '/__e2e/detail', ready: '[data-testid="session-timeline"]' },
  { name: 'approval', url: '/__e2e/detail?mode=approval', ready: '[data-e2e="approval-list"]' },
] as const

/** 主题：双主题对等（SPEC §5.5），两种都要有基线 */
const THEMES = ['light', 'dark'] as const

const MAX_DIFF_PIXEL_RATIO = 0.005 // 0.5% 以内算过（抗锯齿/字体渲染差异）

test.describe.configure({ mode: 'serial' })

for (const sc of SCENES) {
  for (const vp of VIEWPORTS) {
    for (const theme of THEMES) {
      test(`${sc.name} @ ${vp.name} (${theme})`, async ({ page }) => {
        // 主题由 index.html 内联脚本读 localStorage 决定；先注入再 goto，
        // 否则首帧会是浅色（视觉基线里出现"深色版是浅色底"这种事）。
        await page.addInitScript((t) => localStorage.setItem('pieqi.theme.pref', t), theme)

        await page.setViewportSize({ width: vp.width, height: vp.height })
        // 与 layout 用例同一判据：等元素真的出现，而不是等 DOMContentLoaded
        // （夹具路由在 DEV 下由 main.ts 顶层 await 注册，DOMContentLoaded 可能早于它）
        await page.goto(sc.url, { waitUntil: 'commit' })
        await page.waitForLoadState('load')
        await page.waitForSelector(sc.ready, { timeout: 15_000 })
        await stabilize(page)

        // 确认主题真的生效 —— 否则"深色基线"实际是浅色，回归就永远抓不到深色的问题
        await expect(page.locator('html')).toHaveAttribute('data-theme', theme)

        await expect(page).toHaveScreenshot(`${sc.name}-${vp.name}-${theme}.png`, {
          maxDiffPixelRatio: MAX_DIFF_PIXEL_RATIO,
          // threshold：单像素颜色差异容忍（抗锯齿）；它与 maxDiffPixelRatio 是两个维度
          threshold: 0.2,
          // 动图/视频类元素不参与（当前没有，但显式声明意图）
          animations: 'disabled',
          caret: 'hide',
        })
      })
    }
  }
}
