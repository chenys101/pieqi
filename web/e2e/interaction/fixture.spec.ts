// 真点击用例（交互路径）—— **与前两层的判据不同**，先说清楚差别：
//
//   layout 层：渲染出来后**量位置**（能不能看见、会不会被挡住）。是"哨兵"。
//   visual 层：渲染出来后**比像素**（外观变没变）。是"外观回归"。
//   本层：    **真的点下去**，断言整条链的反应（状态变了 / 请求发出去了）。
//
// 为什么不能靠前两层推断：按钮"看得见、点得到"不等于"点了有用"。几何采样只能证明
// 元素没被遮挡，证明不了 onClick 绑没绑、参数对不对、请求有没有发出去。
//
// 覆盖三条最短的幸福路径（用户最常用的动作，坏了影响最大）：
//   ① 「允许一次」审批 → POST intervene { choice: approve }
//   ② 「更早的 N 轮」→ 更早的 Turn 真的挂进 DOM
//   ③ 输入框打字 → 点「发送」→ POST intervene { kind: append_prompt, text }
//
// 观测手段是 mock 后端的 `/__writes` 流水账（**记录但不改数据**，见 mock-api.mjs 的注释）：
// 它证明"请求发了、body 对"，而"点了之后 UI 怎么变"由用例自己驱动 store ——
// 与生产里"WS 推回快照"等价，避免 mock 副作用与我们的点击抢同一个因果。

import { test, expect } from '@playwright/test'
import { stabilize } from '../lib/measure'

const DETAIL = '/__e2e/detail'
const APPROVAL = '/__e2e/detail?mode=approval'

/** 读 mock 的写请求流水账（GET /__writes）。vite 把 /__writes 代理到夹具后端。 */
async function writes(page: import('@playwright/test').Page) {
  const res = await page.request.get('/__writes')
  expect(res.ok(), 'GET /__writes 失败：夹具后端的写请求通道没接通').toBe(true)
  const json = (await res.json()) as { writes: { method: string; path: string; body: any }[] }
  return json.writes
}

/**
 * 每条用例前清空流水账：否则上一条用例的请求会让这一条假绿。
 *
 * ⚠️ 清空**必须**和"读账"用同一个通道，而且要**反复确认清干净**。
 * 实测踩过的失败：前一条用例（L3 二次确认）失败时留下一条 `dec-l2` 的请求，
 * 下一条用例的 `beforeEach` 看似清空了，但读回时那条还在 —— 于是"输入文字
 * 本身不该发请求"这条负向断言直接红，报的还是别人的请求。
 * 这里用 `expect.poll` 让清空**收敛**（删完再读到空才继续），而不是删一次就信。
 */
test.beforeEach(async ({ page }) => {
  await expect
    .poll(
      async () => {
        const res = await page.request.delete('/__writes')
        expect(res.ok(), 'DELETE /__writes 失败：夹具后端的写请求通道没接通').toBe(true)
        const list = await writes(page)
        return list.length
      },
      { message: '夹具后端的写请求流水账清不空（有别的用例在并发写？）' },
    )
    .toBe(0)
})

// ─────────────────────────────────────────────────────────────────────────
// ① 审批：点「允许一次」→ 必须发出 intervene(decision/approve)
// ─────────────────────────────────────────────────────────────────────────
test('审批「允许一次」→ 发出 POST intervene(decision, approve)', async ({ page }) => {
  await page.goto(APPROVAL, { waitUntil: 'commit' })
  await page.waitForLoadState('load')
  await expect(page.locator('[data-e2e="approval-list"]')).toBeVisible()
  await stabilize(page)

  // L2 那张卡（非 L3）点下去不经过内联二次确认，直接发请求 —— 用它验证最短路径。
  // L3 的二次确认单独一条用例（见下）。
  //
  // ⚠️ 选择器的坑：`[data-e2e="approval-list"] > div` 命中不了卡片 ——
  // 容器**本身**是个带 `space-y-3` 的 div，卡片是它的直接子节点，所以
  // `> div` 只能选到「卡片集合」这一层（1 个元素），`.filter({hasText:'L2'})`
  // 再配上 `.first()` 会稳稳选中容器，于是点击落到容器里第一张卡（L2）上。
  // 实测踩过：断言 L3 的用例实际点的是 L2，请求体里 decision_id 是 dec-l2。
  // 判据：用**风险徽章的精确文本**当锚点（卡片里有 `L2 · 执行命令`），
  // 并且要求它唯一 —— 不唯一就说明夹具或卡片结构变了，应当红。
  //
  // 两层判据，都是实测校准的（别再凭直觉写）：
  //
  // ① **层级**：`approval-list` 容器的直接子节点就是卡片（实测 childCount=2，
  //    两个 div.rounded-lg），所以选择器是 `> div`，**不是** `> div > div`。
  //    多写一层会选中卡片内部的子块，`.filter()` 之后点击目标整个错位。
  // ② **锚点**：用风险徽章的精确文本（`L2 · 执行命令`，实测全页唯一 1 个），
  //    而不是对整张卡做 `hasText` 正则 —— 卡片文本是多个子元素拼起来的，
  //    带 `^` 锚点的跨元素正则匹配不到（实测 `/^需要授权\s*L2/` 直接超时）。
  const card = page
    .locator('[data-e2e="approval-list"] > div')
    .filter({ has: page.getByText('L2 · 执行命令', { exact: true }) })
  await expect(card, '没有找到 L2 审批卡（风险徽章文本变了？）').toHaveCount(1)
  const approve = card.getByRole('button', { name: '允许一次' })

  // 点之前先断言"没有任何写请求" —— 这是让"点了才有请求"成为因果证据的关键，
  // 否则一个页面加载时就发请求的实现也能让这条用例绿。
  expect(await writes(page), '点击之前就已经有写请求了，这条用例证明不了因果').toHaveLength(0)

  await approve.click()

  // 轮询等请求到达（点击 → store 异步 → fetch 有网络往返）
  await expect
    .poll(async () => (await writes(page)).length, { message: '点了「允许一次」但没有任何写请求' })
    .toBeGreaterThan(0)

  const w = (await writes(page))[0]
  expect(w.method).toBe('POST')
  expect(w.path, '请求路径必须是该任务的 intervene').toMatch(/^\/api\/tasks\/.+\/intervene$/)
  expect(w.body?.kind, 'kind 必须是 decision（不是 append_prompt）').toBe('decision')
  expect(w.body?.choice, '「允许一次」必须映射成 approve').toBe('approve')
  // decision_id 必须带上：后端靠它把决策投递到正确的挂起请求上，缺了会 404/找不到 pending。
  expect(w.body?.decision_id, '必须携带 decision_id').toBeTruthy()
})

// ─────────────────────────────────────────────────────────────────────────
// ② L3 二次确认：点「允许一次」**不发请求**，先弹内联确认；再点「确认允许」才发
// ─────────────────────────────────────────────────────────────────────────
test('L3 审批 → 先内联二次确认，再发请求', async ({ page }) => {
  await page.goto(APPROVAL, { waitUntil: 'commit' })
  await page.waitForLoadState('load')
  await expect(page.locator('[data-e2e="approval-list"]')).toBeVisible()
  await stabilize(page)

  // 与上一条同一个选择器判据（见那里的注释）：用风险徽章的**精确文本**当锚点，且要求唯一。
  const card = page
    .locator('[data-e2e="approval-list"] > div')
    .filter({ has: page.getByText('L3 · 破坏性操作', { exact: true }) })
  await expect(card, '没有找到 L3 审批卡（风险徽章文本变了？）').toHaveCount(1)

  await card.getByRole('button', { name: '允许一次' }).click()

  // ★ 关键断言（负向）：L3 第一下**不能**发请求。
  //   破坏性操作不经确认直接执行，是这套 UI 里最不能接受的结果。
  await card.getByText('此操作不可逆，确认允许？').waitFor()
  expect(await writes(page), 'L3 第一下点击就发了请求 —— 二次确认被绕过').toHaveLength(0)

  await card.getByRole('button', { name: '确认允许' }).click()
  await expect.poll(async () => (await writes(page)).length, { message: '确认后仍未发出请求' }).toBe(1)
  expect((await writes(page))[0].body?.choice).toBe('approve')
})

// ─────────────────────────────────────────────────────────────────────────
// ③ 更早的 Turn：点「更早的 N 轮」→ 被收起的 Turn 真的挂进 DOM
// ─────────────────────────────────────────────────────────────────────────
test('点「更早的 N 轮」→ 更早的 Turn 被挂载', async ({ page }) => {
  await page.goto(DETAIL, { waitUntil: 'commit' })
  await page.waitForLoadState('load')
  await expect(page.locator('[data-testid="session-timeline"]')).toBeVisible()
  await stabilize(page)

  const loadOlder = page.locator('[data-testid="load-older-turns"]')
  await expect(loadOlder, '长会话（12 轮）应出现「更早的 N 轮」按钮；没有说明窗口化没生效').toBeVisible()

  // 被收起时，第 1 轮**不该在 DOM 里**（v-if 而非显隐）—— 这正是省渲染代价的地方。
  await expect(page.locator('[data-testid="turn-group-1"]')).toHaveCount(0)

  const before = await page.locator('[data-testid^="turn-group-"]').count()
  await loadOlder.click()
  await expect(page.locator('[data-testid="turn-group-1"]'), '点了之后第 1 轮仍未挂载').toHaveCount(1)

  const after = await page.locator('[data-testid^="turn-group-"]').count()
  expect(after, `点「更早的 N 轮」后挂载的 Turn 数没有增加（${before} → ${after}）`).toBeGreaterThan(before)
})

// ─────────────────────────────────────────────────────────────────────────
// ④ 发送：输入文字 → 点「发送」→ 发出 intervene(append_prompt, text)
// ─────────────────────────────────────────────────────────────────────────
test('输入后点「发送」→ 发出 POST intervene(append_prompt, text)', async ({ page }) => {
  await page.goto(DETAIL, { waitUntil: 'commit' })
  await page.waitForLoadState('load')
  await expect(page.locator('[data-testid="composer-bar"]')).toBeVisible()
  await stabilize(page)

  const text = '帮我把 layout 的失败用例补一下'
  const box = page.getByLabel('补充指令')
  await box.fill(text)

  // 发送按钮用无障碍名定位（生产代码里已有 aria-label="发送"）。
  const send = page.getByRole('button', { name: '发送' })
  await expect(send, '找不到发送按钮（aria-label="发送"）').toBeVisible()
  expect(await writes(page), '输入文字本身不该发请求').toHaveLength(0)

  await send.click()
  await expect.poll(async () => (await writes(page)).length, { message: '点了发送但没有请求' }).toBe(1)

  const w = (await writes(page))[0]
  expect(w.path).toMatch(/\/intervene$/)
  expect(w.body?.kind, '发送文字必须是 append_prompt').toBe('append_prompt')
  expect(w.body?.text, '请求里的 text 必须与输入一致（不能丢字/截断）').toBe(text)
})
