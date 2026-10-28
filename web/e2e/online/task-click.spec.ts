// 线上复量 · R5：「点任务能进详情」的**真点击**回归（2026-10-23 用户反馈：移动端点任务不生效）。
//
// 为什么要单开一条用例，而不是并进 pages.spec：
//   · 前四条都是「打开一屏、量尺寸」—— 它们验证的是**渲染**，而这次坏的是**行为**：
//     列表看得见、点下去没反应。渲染层面的断言（含 m.interactive.blocked 的四角采样）
//     对「点了没反应」是全盲的：请求根本没发出、或发出后被 4xx/5xx 打回，页面都还是那副样子。
//   · 判据因此必须是「点完之后 URL 变成 /sessions/<id>，且详情页真的渲染出来了」，
//     而不是「链接存在」。
//
// 仍然是**只读**：只点 RouterLink（纯前端路由，不发写请求），并照旧拦截所有非 GET。
//
// 运行：npm run test:e2e:online -- --url=http://127.0.0.1:3000 -g "@online R5"
import { test, expect, type Page } from '@playwright/test'

async function lockReadOnly(page: Page): Promise<void> {
  await page.route('**/api/**', (route) => {
    const m = route.request().method()
    if (m === 'GET' || m === 'HEAD') return route.continue()
    return route.abort('blockedbyclient')
  })
}

/** 控制台错误 + 失败的 /api 响应（两者都是「点了没反应」的现场证据） */
function track(page: Page): { errs: string[]; bad: string[] } {
  const errs: string[] = []
  const bad: string[] = []
  const IGNORE = /WebSocket connection to|favicon|manifest\.webmanifest|ServiceWorker/i
  page.on('console', (m) => {
    if (m.type() === 'error' && !IGNORE.test(m.text())) errs.push(m.text())
  })
  page.on('pageerror', (e) => errs.push(String(e)))
  page.on('response', (r) => {
    const u = new URL(r.url())
    if (u.pathname.startsWith('/api/') && r.status() >= 400) bad.push(`${r.status()} ${r.request().method()} ${u.pathname}`)
  })
  return { errs, bad }
}

/** 取任务列表里第一个任务（只读请求，不用页面自己去拿） */
async function firstTask(page: Page): Promise<{ id: string; title: string } | null> {
  const res = await page.request.get('/api/tasks', { timeout: 20_000 })
  expect(res.status(), 'GET /api/tasks 失败：服务不在或鉴权不通').toBe(200)
  const body = (await res.json()) as {
    projects?: { tasks?: { id: string; title?: string; prompt?: string }[] }[]
  }
  const t = body.projects?.flatMap((p) => p.tasks ?? [])[0]
  return t ? { id: t.id, title: t.title || t.prompt || '' } : null
}

for (const vp of [
  { name: 'mobile', width: 375, height: 667 },
  { name: 'desktop', width: 1440, height: 900 },
] as const) {
  test(`@online R5 · 任务列表点任务能进详情（${vp.name}）`, async ({ page }) => {
    await page.setViewportSize({ width: vp.width, height: vp.height })
    await lockReadOnly(page)
    const { errs, bad } = track(page)

    const target = await firstTask(page)
    test.skip(!target, '线上没有任务（全新部署），跳过 —— 不是故障')
    const id = target!.id

    // 进入列表的入口按档位不同：移动端走底栏「任务」，桌面走侧栏任务树。
    // 两处用的是**同一个** taskStore + 同一套路由，坏起来就是坏的同一处，
    // 所以两档都要真点一遍。
    await page.goto(vp.name === 'mobile' ? '/tasks' : '/dashboard', { waitUntil: 'load' })
    await expect(page.locator('#app > *').first()).toBeVisible()

    if (vp.name === 'mobile') {
      // 项目默认收起，先展开第一个项目
      const head = page.locator('button.tb-head').first()
      await expect(head, '任务页没有任何项目分组').toBeVisible({ timeout: 10_000 })
      await head.click()
    } else {
      // 侧栏任务树：先展开第一个项目节点（与移动端同一个 taskTree store）
      const treeToggle = page.locator('aside button[aria-expanded="false"]').first()
      if (await treeToggle.count()) await treeToggle.click()
    }

    const link = page.locator(`a[href="/sessions/${id}"]`).first()
    await expect(link, `列表里找不到指向 /sessions/${id} 的链接`).toBeVisible({ timeout: 10_000 })

    // ★ 真点击。click() 自带可操作性检查（可见、稳定、不被遮挡），
    //   所以"点不到"会红在这里，而不是被 elementFromPoint 采样放过。
    await link.click()

    await page.waitForURL(`**/sessions/${id}`, { timeout: 8_000 })

    // 判据二：进得去还要**渲染出来**。详情接口 4xx/5xx 时页面会停在「任务不存在」，
    // URL 却已经是详情页 —— 那样"点生效了"是个假阳性。
    const empty = page.getByText('任务不存在或已删除')
    await expect(page.locator('[data-testid="session-timeline"]'), '详情页没有渲染出时间线容器').toBeVisible({
      timeout: 15_000,
    })
    await expect(empty, '详情页显示了「任务不存在或已删除」—— 但列表里有它：两端数据不一致或详情接口失败').toHaveCount(0)

    expect(bad, `点击过程中有失败的接口调用：\n${bad.join('\n')}`).toEqual([])
    expect(errs, `点击过程中控制台报错：\n${errs.join('\n')}`).toEqual([])
  })
}
