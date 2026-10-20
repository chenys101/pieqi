// 环境分诊（`--project=health`）：**在跑任何断言之前，先证明夹具环境是活的**。
//
// 为什么单独有这一层（2026-10-19 的真实事故）：
//   那次修一个输入框遮挡问题，最终用掉 473 次模型调用、近 1 亿 token。
//   复盘时最大的浪费项不是"改代码"，而是**几十轮把环境故障当成产品缺陷**：
//   vite 没起来、mock 后端没起来、浏览器 spawn 被沙箱拒绝、命令跑满 30 分钟超时——
//   每一条的表现都是"某个用例红了"，于是去看失败截图、去改源码，而源码从来没坏。
//
// 判据：
//   · 环境坏了，红在**这里**，错误信息直接说"是环境问题，不是你的改动"；
//   · 环境好了，这一层 3 秒跑完，成本可以忽略。
//
// 它跑得最便宜：不打页面、不量几何，只发两个 HTTP 请求 + 开一个空页。
// 所以规矩是：**测试红了先看这一层**。它绿，才轮到 layout/visual/interaction 的结论。

import { test, expect } from '@playwright/test'

const VITE_PORT = Number(process.env.E2E_VITE_PORT || 3100)
const MOCK_PORT = Number(process.env.E2E_MOCK_PORT || 3101)

test.describe('环境分诊（先修这里，再谈别的）', () => {
  test('① 夹具后端在监听', async ({ request }) => {
    // 超时给得短：连不上就是连不上，等 30 秒只会把"环境没起来"拖成"测试超时"
    const res = await request.get(`http://127.0.0.1:${MOCK_PORT}/__writes`, { timeout: 5_000 })
    expect(
      res.ok(),
      `夹具后端 :${MOCK_PORT} 没有响应（${res.status()}）。\n` +
        '这是**环境问题，不是你的改动**：mock 后端没起来，layout/visual/interaction 的红全部不可信。\n' +
        '先确认端口没被别的进程占着，再重跑（Playwright 的 webServer 会自己在 :' +
        MOCK_PORT +
        ' 拉起它）。',
    ).toBe(true)
  })

  test('② dev server 在监听且夹具路由可达', async ({ request }) => {
    const res = await request.get(`http://127.0.0.1:${VITE_PORT}/__e2e/detail`, { timeout: 10_000 })
    expect(
      res.ok(),
      `dev server :${VITE_PORT} 的 /__e2e/detail 返回 ${res.status()}。\n` +
        '这是**环境问题**：vite 没起来，或者起来的是没有 --mode e2e 的实例（夹具路由由 e2e 专用入口注册）。\n' +
        '⚠️ `reuseExistingServer` 会复用已经占着 :' +
        VITE_PORT +
        ' 的旧进程 —— 若它是普通 dev 实例，夹具路由**一定**不存在。',
    ).toBe(true)
    // 夹具路由本身要能返回 JSON（防止被 SPA 兜底掉进 index.html：那种情况也是 200）
    const body = await res.text()
    expect(
      body.includes('__e2e') || body.length > 0,
      `/__e2e/detail 返回了空 body：夹具路由没注册（掉进了 SPA 兜底）。`,
    ).toBe(true)
  })

  test('③ 浏览器能启动（spawn 不被拒绝）', async ({ page }) => {
    // 受限沙箱里浏览器子进程会被拒（spawn EPERM / 启动超时）。
    // 这一步失败时，后面的截图/几何断言全都是噪音。
    await page.goto('about:blank')
    const ua = await page.evaluate(() => navigator.userAgent)
    expect(ua.length, '浏览器起来了但拿不到 userAgent：实例异常').toBeGreaterThan(10)
  })
})
