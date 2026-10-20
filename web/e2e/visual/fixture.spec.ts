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
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { stabilize, VIEWPORTS } from '../lib/measure'

const SCENES = [
  {
    name: 'detail',
    url: '/__e2e/detail',
    // ⚠️ ready 必须等到**最后一轮的内容**，不能只等时间线容器。
    //
    // 容器先出现、Turn 再逐个渲染 —— 等容器 = 截到"轮次还没挂全"的中间态。
    // 实测：语义卡（在 Turn 12 里）确实渲染了、位置也在视口内（探针验证过），
    // 但截图里一张都没有，而且**截图字节与改动前完全一致** —— 说明拍的是
    // 旧的一帧。这类"图看起来正常、其实少了一半内容"的基线最危险：
    // 它会让你在后续改动里拿一张残图当标准答案。
    //
    // 判据：等 Turn 12 的最后一组语义卡出现（它就是夹具里最后渲染的东西）。
    ready: '[data-testid="session-timeline"] [data-testid="tool-semantic-plan"]',
  },
  { name: 'approval', url: '/__e2e/detail?mode=approval', ready: '[data-e2e="approval-list"]' },
  // 首页（SPEC §6.2）：此前零基线。它有两个"选择性渲染"的区块 ——
  // 待审批只在有待办时出现、本周概览只在有历史时出现 —— 正是最该被像素守着的东西。
  { name: 'dashboard', url: '/__e2e/detail?mode=dashboard', ready: '[data-e2e="dashboard"]' },
  // Agents 页：能力矩阵。守"三张卡长得不一样"（此前能力行完全相同）。
  {
    name: 'agents',
    url: '/__e2e/detail?mode=agents',
    ready: '[data-e2e="agents"] [data-testid="capability-matrix"]',
  },
  // 设置页：表单是「标签挤压 / 开关换行」的高发区，此前零覆盖。
  { name: 'settings', url: '/__e2e/detail?mode=settings', ready: '[data-e2e="settings"]' },
  // 新建任务页：主流程入口（这里坏了用户连任务都建不了）。
  {
    name: 'newtask',
    url: '/__e2e/detail?mode=newtask',
    ready: '[data-e2e="newtask"] [data-testid="composer-submit"]',
  },
] as const

/** 主题：双主题对等（SPEC §5.5），两种都要有基线 */
const THEMES = ['light', 'dark'] as const

const MAX_DIFF_PIXEL_RATIO = 0.005 // 0.5% 以内算过（抗锯齿/字体渲染差异）

/** 失败产物落点（与 playwright.config.ts 的 OUTPUT_DIR 一致，仅用于报错里给出路径） */
const OUTPUT_PREFIX = process.env.E2E_OUTPUT_DIR || join(tmpdir(), 'pieqi-e2e')

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

        /**
         * ★ 视觉失败要回答"**哪里**变了"，而不只是"变了"。
         *
         * `toHaveScreenshot(maxDiffPixelRatio)` 的默认报错会给出上中下三段裁剪
         * （Playwright 1.4x 的 "Expected/Actual" 缩略 + diff 区域），但那是**图**，
         * 要人去看。这里再把它换算成**文字**：哪一块矩形、占页面多少、涉及什么元素。
         *
         * 判据：截图从"第一眼证据"降级为"文字说不清时才看的最后手段"。
         * 本次实现只做**降级路径**：toHaveScreenshot 失败时先打一张文本结论，
         * 让 SUMMARY 里那一条至少带一个可读的差异比例与区域。
         */
        const shotName = `${sc.name}-${vp.name}-${theme}.png`
        let shotError: Error | null = null
        try {
          await expect(page).toHaveScreenshot(shotName, {
            maxDiffPixelRatio: MAX_DIFF_PIXEL_RATIO,
            // threshold：单像素颜色差异容忍（抗锯齿）；它与 maxDiffPixelRatio 是两个维度
            threshold: 0.2,
            // 动图/视频类元素不参与（当前没有，但显式声明意图）
            animations: 'disabled',
            caret: 'hide',
          })
        } catch (e) {
          shotError = e as Error
        }
        if (shotError) {
          // 差异区域：让 Playwright 把 "N pixels differ" 那行原样带到结论里，
          // 再补一句"看哪里的建议顺序"，避免直接跳去看图。
          const raw = shotError.message
          const diffLine = raw
            .split('\n')
            .map((l) => l.trim())
            .find((l) => /pixels? (are )?different|diff/i.test(l) && /\d/.test(l))
          const where = `场景=${sc.name} 视口=${vp.width}×${vp.height} 主题=${theme}`
          throw new Error(
            `视觉基线不一致（${where}）\n` +
              `  ${diffLine ?? '（Playwright 未给出像素差异行，看下面的原始报错）'}\n` +
              `  建议顺序：① 先确认这是**设计变更**还是**回归**（改的是这一个场景吗？）；\n` +
              `            ② 是设计变更才跑 \`npm run test:e2e:update-snapshots\` 更新基线；\n` +
              `            ③ **只有** 上两步都无法从数字/文字判断时，才看一张 ` +
              `${OUTPUT_PREFIX}/${shotName.replace('.png', '')} 的 diff 图。\n` +
              `  原始报错首行：${raw.split('\n')[0]}`,
          )
        }
      })
    }
  }
}
