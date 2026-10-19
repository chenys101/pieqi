// 线上复量 · 输入框多行内容不被动作行盖住（真实实例验证）。
//
// 背景：这个缺陷是"移动端输入框内容多时被模型选择器盖住"，夹具层已覆盖，
// 但**夹具层绿不等于线上绿**（AGENTS.md 的纪律）。这里在真实会话页上再验一次。
//
// 只读纪律：不点发送、只 fill 与滚动；结束时清空输入框。
import { test, expect, type Page } from '@playwright/test'

async function lockReadOnly(page: Page): Promise<void> {
  await page.route('**/api/**', (route) => {
    const m = route.request().method()
    if (m === 'GET' || m === 'HEAD') return route.continue()
    return route.abort('blockedbyclient')
  })
}

test('@online · 会话页输入框：10 行内容滚到底不被动作行盖住', async ({ page, request }) => {
  await lockReadOnly(page)

  const res = await request.get('/api/tasks', { timeout: 20_000 })
  expect(res.status()).toBe(200)
  const body = (await res.json()) as { projects?: { tasks?: { id: string }[] }[] }
  const id = body.projects?.flatMap((p) => p.tasks ?? [])[0]?.id
  test.skip(!id, '线上没有任务：跳过')

  await page.setViewportSize({ width: 375, height: 667 })
  await page.goto(`/sessions/${id}`, { waitUntil: 'commit' })
  await page.waitForLoadState('load')
  await page.waitForSelector('[data-testid="composer-bar"]', { timeout: 15_000 })

  // 等输入区定高完成（异步 resize）：resizer 底边不再压住 composer 顶边
  await expect
    .poll(
      async () =>
        page.evaluate(() => {
          const rz = document.querySelector('[data-testid="content-resizer"]')?.getBoundingClientRect()
          const cb = document.querySelector('[data-testid="composer-bar"]')?.getBoundingClientRect()
          if (!rz || !cb) return 0
          return Math.round(rz.bottom - cb.top)
        }),
      { timeout: 3000 },
    )
    .toBeLessThanOrEqual(0)

  const box = page.getByLabel('补充指令')
  await expect(box, '找不到输入框').toBeVisible()
  await box.fill(Array.from({ length: 10 }, (_, i) => `第 ${i + 1} 行内容`).join('\n'))
  await page.waitForTimeout(250)

  await page.evaluate(() => {
    const ta = document.querySelector('textarea[aria-label="补充指令"]') as HTMLTextAreaElement | null
    if (ta) ta.scrollTop = ta.scrollHeight
  })
  await page.waitForTimeout(150)

  const g = await page.evaluate(() => {
    const ta = document.querySelector('textarea[aria-label="补充指令"]') as HTMLTextAreaElement
    const actions = document.querySelector('[data-testid="composer-actions"]') as HTMLElement
    const taR = ta.getBoundingClientRect()
    const acR = actions.getBoundingClientRect()
    const cs = getComputedStyle(ta)
    const padTop = parseFloat(cs.paddingTop)
    const padBottom = parseFloat(cs.paddingBottom)
    const textTop = taR.top + padTop
    const textBottom = taR.bottom - padBottom
    return {
      boxH: Math.round(taR.height),
      visibleTextH: Math.round(textBottom - textTop),
      coveredPx: Math.round(Math.max(0, Math.min(textBottom, acR.bottom) - Math.max(textTop, acR.top))),
    }
  })

  // ① 可视文字区必须装得下至少两行（行高约 21.7px）
  expect(
    g.visibleTextH,
    `可视文字区只有 ${g.visibleTextH}px（盒子 ${g.boxH}px）：装不下一行以上 —— 让位区把可视区吃掉了`,
  ).toBeGreaterThan(40)

  // ② 滚到底后动作行不得盖住文字区
  expect(g.coveredPx, `动作行仍盖住文字区 ${g.coveredPx}px：用户看不到被盖住的最后几行`).toBeLessThanOrEqual(0)

  // 只读收尾：清空输入框（fill 是本地状态，不发请求）
  await box.fill('')
})
