// 真点击 · 详情页「在「x」新建任务」入口（用户 2026-10-24 要求）：
//   ① 移动端：入口**出现**、点在拇指区、点下去**真的导航**到新建页；
//   ② 新建页的**项目 = 当前任务的项目**（预选，且标着「来自当前任务」）；
//   ③ 用户改了项目之后，那枚徽标消失（它说的只能是事实）；
//   ④ 平板/桌面：**没有**这个入口（那边有侧栏「＋ 新建任务」，同一件事不给两个入口）。
//
// 为什么这一层必须真有（而不是靠 layout 的量位置）：
//   「入口存在且没被挡住」证明不了"点了有用"。这条链路曾经在夹具里**静默失效**两次：
//     · 夹具路由表没有 `/tasks/new` ⇒ 点了停在原地；
//     · 夹具 Shell 的模式在 setup 里只算一次 ⇒ 点了之后实例被复用、URL 变了但界面不动。
//   两次都不报错，看起来都是"没反应"。判据只能是"点完之后 URL 与新页面内容都对"。
//
// 运行：npx playwright test --project=interaction -g "新建任务入口"
import { test, expect, type Page } from '@playwright/test'

/** 夹具里那条 12 轮长会话（waiting_input + 待审批），项目固定是 pieqi */
const LONG_ID = 'e2e-long-12'
const LONG_PROJECT = 'D:/code/go/pieqi'
const SESSION_PAGE = `/__e2e/detail/${LONG_ID}/sessionpage`

const entry = (page: Page) => page.locator('[data-testid="session-new-task"]')

/** 打开整页会话详情并等它渲染完（时间线出现 = 有数据的首帧已提交） */
async function openSession(page: Page): Promise<void> {
  await page.goto(SESSION_PAGE, { waitUntil: 'commit' })
  await page.waitForLoadState('load')
  await expect(page.locator('[data-testid="session-timeline"]')).toBeVisible({ timeout: 15_000 })
}

test.describe('新建任务入口 · 移动端', () => {
  test.use({ viewport: { width: 375, height: 667 } })

  test('入口出现、落在拇指区，点下去带着当前任务的项目进新建页', async ({ page }) => {
    await openSession(page)

    const link = entry(page)
    await expect(link, '移动端详情页没有「新建任务」入口').toBeVisible()
    await expect(link).toContainText('pieqi') // 名字来自任务的项目（project_id）

    /**
     * 几何判据（与"用户能不能单手点到"直接相关）：
     *   · 命中区高度 ≥44（iOS HIG / Material 的下限，本仓库 AC-R8-04 同口径）；
     *   · 整块在视口内 —— 用户不该为了"再开一个任务"去滚动；
     *   · 在输入条**下方**、底栏**上方**：它不是输入框的一部分，也不是导航项。
     */
    const box = await link.boundingBox()
    expect(box, '入口没有布局盒（未渲染或 display:none）').not.toBeNull()
    expect(box!.height, `入口命中区只有 ${box!.height}px 高（应 ≥44）`).toBeGreaterThanOrEqual(44)
    expect(box!.y + box!.height, '入口超出视口底部').toBeLessThanOrEqual(667)
    const composer = await page.locator('[data-testid="composer-bar"]').boundingBox()
    const nav = await page.locator('nav[aria-label="主导航"]').boundingBox()
    expect(composer, '输入条不存在').not.toBeNull()
    expect(nav, '移动端底栏不存在').not.toBeNull()
    expect(box!.y, '入口与输入条重叠').toBeGreaterThanOrEqual(composer!.y + composer!.height)
    expect(box!.y + box!.height, '入口与底栏重叠（会挡住点击）').toBeLessThanOrEqual(nav!.y)

    // ★ 真点击（click 自带可操作性检查：被遮挡会红在这里）
    await link.click()
    // URL 判据只认"路径对、project 段对"。**不**断言百分号编码的形态：
    // `D:/code/...` 里没有需要编码的字符，`?project=D%3A%2F…` 与 `?project=D:/…`
    // 在浏览器里是同一个查询值（实测 Playwright 的 toHaveURL 拿到的就是后者）。
    // 真正要守的是**页面读到了它**（下面两条），编码形态由 vue-router 负责。
    await expect(page).toHaveURL(/\/tasks\/new\?project=/)

    // 判据是"新页面的项目已经是当前任务的"，而不是"URL 里有参数"：
    // 页面可能根本没读到它（历史上正是这样：URL 变了、项目却还是最近用过的那个）。
    await expect(page.locator('[data-testid="project-summary"]')).toContainText(LONG_PROJECT)
    await expect(page.locator('[data-testid="project-from-task"]'), '预选项目没有标注出处').toHaveText('来自当前任务')
    await expect(page.locator('select[aria-label="历史项目"]')).toHaveValue(LONG_PROJECT)
    // 新页面是**新建任务**，不是别的东西（同名 testid 在会话页也有）
    await expect(page.getByLabel('任务描述')).toBeVisible()
  })

  test('用户改了项目，那枚「来自当前任务」就消失（徽标只说事实）', async ({ page }) => {
    // 自定义路径形态进来（预选目录不在历史项目里 ⇒ 走输入框）。
    // 这里用的是**手输旧形态**（直接一段路径、不编码）—— 同一个页面要能同时认两种取值，
    // 判据见 app/router.ts 的 newTaskProjectOf。
    await page.goto(`/__e2e/detail?mode=newtask&project=${encodeURIComponent('D:\\other\\workspace\\zzz')}`, {
      waitUntil: 'load',
    })
    await expect(page.locator('[data-testid="project-from-task"]')).toHaveText('来自当前任务')

    const input = page.getByPlaceholder('输入绝对路径，如 G:\\workspace\\erp')
    await expect(input).toHaveValue('D:\\other\\workspace\\zzz')
    await input.fill('D:\\other\\workspace\\edited')
    await expect(page.locator('[data-testid="project-from-task"]'), '用户改了项目，徽标却还在说"来自当前任务"').toHaveCount(0)
  })
})

for (const vp of [
  { name: 'tablet', width: 834, height: 1112 },
  { name: 'desktop', width: 1440, height: 900 },
] as const) {
  test(`新建任务入口 · ${vp.name} 不出现（侧栏已有「＋ 新建任务」）`, async ({ page }) => {
    await page.setViewportSize({ width: vp.width, height: vp.height })
    await openSession(page)
    await expect(entry(page), `${vp.name} 上不该有移动端专属的新建入口（同一件事两个入口）`).toHaveCount(0)
  })
}
