// 线上复量 · R1 / R3：真实页面"还能不能用"。
//
// 与夹具层的根本区别（见 docs/testing/ui-e2e-online.md §3）：
//   · **只读靠机制**：每个用例先拦截所有非 GET 请求（route.abort），
//     不靠"记得别点"。内网访问线上实例时读写鉴权强度相同（auth.go:11），
//     一次误触就是真实副作用。
//   · **不假定数据形状**：可以断言"若有待审批卡则必须可见"，
//     不能断言"必须有" —— 线上随时可能没有待办。
//   · **判据只用与数据量无关的指标**：横向溢出、遮挡、主行动可见、控制台错误。
//     这些恰好就是"这屏还能不能用"，而与今天有几条任务无关。
//
// 运行：ONLINE_BASE_URL=http://127.0.0.1:3000 npx playwright test --project=online
import { test, expect, type Page } from '@playwright/test'
import { measure, stabilize } from '../lib/measure'

/**
 * 把页面锁成只读：任何非 GET/HEAD 的 /api 请求**就地失败**。
 *
 * 为什么 abort 而不是 fulfill 一个假 200：假成功会把"点了没反应"变成
 * "看起来正常"，掩盖真实的误触。abort 会让用例立刻红 —— 那正是我们要的。
 */
async function lockReadOnly(page: Page): Promise<void> {
  await page.route('**/api/**', (route) => {
    const m = route.request().method()
    if (m === 'GET' || m === 'HEAD') return route.continue()
    return route.abort('blockedbyclient')
  })
}

/** 收集控制台错误（排除 WS 重连与静态资源噪声 —— 线上有隧道，那些是常态）。 */
function trackErrors(page: Page): string[] {
  const errs: string[] = []
  const IGNORE = /WebSocket connection to|favicon|manifest\.webmanifest|ServiceWorker/i
  page.on('console', (m) => {
    if (m.type() === 'error' && !IGNORE.test(m.text())) errs.push(m.text())
  })
  page.on('pageerror', (e) => errs.push(String(e)))
  return errs
}

/**
 * 断言页面不是"被踢回登录/空壳"。
 *
 * 线上最常见的静默故障：鉴权失效 → 前端 401 后跳登录或渲染空态，
 * 而 HTTP 状态全是 200、控制台也未必报错。判据是"挂载点里有实际内容"。
 */
async function expectAppRendered(page: Page): Promise<void> {
  await expect(page.locator('#app')).toBeVisible()
  const children = await page.locator('#app > *').count()
  expect(children, '#app 挂载点里没有任何子节点：前端白屏（资源没加载或被 401 踢走）').toBeGreaterThan(0)
}

// ─────────────────────────────────────────────────────────────────────────
// R1 · Dashboard
// ─────────────────────────────────────────────────────────────────────────
test('R1 · Dashboard 可进入，无溢出/无遮挡/无控制台错误', async ({ page }) => {
  await lockReadOnly(page)
  const errs = trackErrors(page)

  await page.goto('/dashboard', { waitUntil: 'commit' })
  await page.waitForLoadState('load')
  await expectAppRendered(page)
  await stabilize(page)

  const m = await measure(page)

  // ① 整页横向溢出 —— 与数据量无关的硬判据（移动端最刺眼的事故）
  expect(m.hOverflow, `Dashboard 整页横向溢出 ${m.hOverflow}px`).toBe(0)

  // ② 关键交互元素不得被遮挡（线上可能有浮动元素/提示条，这一条会抓到）
  const blocked = m.interactive.blocked
  expect(
    blocked,
    '以下元素会被别的元素拦截点击：\n' + blocked.map((b) => `  · ${b.name} @(${b.at}) → ${b.hit}`).join('\n'),
  ).toEqual([])

  // ③ **若有**待审批卡，主行动按钮必须一屏内可见（不假定一定有）
  const approve = page.getByRole('button', { name: '允许一次' }).first()
  if (await approve.count()) {
    const box = await approve.boundingBox()
    const vp = page.viewportSize()!
    expect(box, '「允许一次」没有布局盒').not.toBeNull()
    expect(
      box!.y >= 0 && box!.y + box!.height <= vp.height,
      `待审批卡的「允许一次」不在视口内（y=${box!.y} h=${box!.height} vp=${vp.height}）：用户要滚动才能决策`,
    ).toBe(true)
  }

  // ④ 控制台无错误
  expect(errs, `Dashboard 控制台报错：\n${errs.join('\n')}`).toEqual([])
})

// ─────────────────────────────────────────────────────────────────────────
// R2 · 会话详情（取一个**真实**任务 id）
// ─────────────────────────────────────────────────────────────────────────
test('R2 · 会话详情可进入，输入区可用（不做任何点击）', async ({ page, request }) => {
  await lockReadOnly(page)
  const errs = trackErrors(page)

  // 先从真实接口取一个任务 id（只读请求）。
  // 判据：**不假定线上一定有任务** —— 全新部署时可能一个都没有，那时跳过而不是红。
  const res = await request.get('/api/tasks')
  expect(res.status()).toBe(200)
  const body = (await res.json()) as { projects?: { tasks?: { id: string }[] }[] }
  const firstId = body.projects?.flatMap((p) => p.tasks ?? [])[0]?.id
  test.skip(!firstId, '线上没有任务：跳过详情页（不假定数据形状，这是线上层与夹具层的根本区别）')

  await page.goto(`/sessions/${firstId}`, { waitUntil: 'commit' })
  await page.waitForLoadState('load')
  await expectAppRendered(page)
  // 等时间线容器出现（真实任务可能没有 events，但那也应有容器）
  await page.waitForSelector('[data-testid="session-timeline"]', { timeout: 15_000 })
  await stabilize(page)

  const m = await measure(page)
  expect(m.hOverflow, `会话详情整页横向溢出 ${m.hOverflow}px`).toBe(0)

  // 输入区存在 + 发送按钮在视口内（不 fill、不 point —— 只 verify 可见性）
  const composer = page.locator('[data-testid="composer-bar"]')
  await expect(composer, '会话详情缺少输入区（composer-bar）').toBeVisible()
  const send = page.getByRole('button', { name: '发送' }).or(page.getByRole('button', { name: '中止' }))
  await expect(send.first(), '输入区没有发送/中止按钮').toBeVisible()

  // 交互元素不得被遮挡（线上可能有浮动提示条压住按钮）
  const blocked = m.interactive.blocked
  expect(
    blocked,
    '以下元素会被别的元素拦截点击：\n' + blocked.map((b) => `  · ${b.name} @(${b.at}) → ${b.hit}`).join('\n'),
  ).toEqual([])

  expect(errs, `会话详情控制台报错：\n${errs.join('\n')}`).toEqual([])
})

// ─────────────────────────────────────────────────────────────────────────
// R3 · 设置页
// ─────────────────────────────────────────────────────────────────────────
test('R3 · 设置页可进入，四组齐全且开关可交互', async ({ page }) => {
  await lockReadOnly(page)
  const errs = trackErrors(page)

  await page.goto('/settings', { waitUntil: 'commit' })
  await page.waitForLoadState('load')
  await expectAppRendered(page)
  await stabilize(page)

  const m = await measure(page)
  expect(m.hOverflow, `设置页整页横向溢出 ${m.hOverflow}px`).toBe(0)

  // 四组标题必须都在（顺序也是信息：组① 审批与自动化 排最上）
  for (const title of ['审批与自动化', '连接与账号', '外观与偏好', '数据与关于']) {
    await expect(page.getByRole('heading', { name: title }), `缺少分组「${title}」`).toBeVisible()
  }

  // 前两个可用开关必须存在（= getSettings 成功）。
  // 与夹具层同一条判据：加载失败时开关全 disabled，而「减弱动效」不依赖该请求，
  // 所以"存在任意一个可用开关"是空转的（这条在夹具层已经被实测证伪过一次）。
  const enabled = page.locator('button[role="switch"]:not([disabled])')
  expect(await enabled.count(), '没有任何可用开关：settings 没加载成功').toBeGreaterThan(1)

  expect(errs, `设置页控制台报错：\n${errs.join('\n')}`).toEqual([])
})
