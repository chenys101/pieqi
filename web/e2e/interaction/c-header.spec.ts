// 详情页方案 C 头部（2026-10-29 用户定案）的真浏览器回归：
//   · 当前状态是一个独立裸圆点按钮（点开是状态详情，agent 在其中可见）；
//   · 其他任务是另一个独立按钮（描边 + 堆叠图标 + 数量），点开是顶部下拉，最多 3 个；
//   · 反馈是无文字方形面板按钮；新建是 accent 实心圆 + 加号；
//   · agent 与用量下移到输入条，运行中（输入禁用）仍然可见。
//
// 为什么用几何/样式断言：这一版的核心就是"四个入口要被区分开"。
// 只断言"都存在"证明不了状态点和任务按钮不是同一个东西，所以额外量边框与圆角。
import { test, expect, type Page } from '@playwright/test'

const SESSION_PAGE = '/__e2e/detail/e2e-long-12/sessionpage'

async function openSession(page: Page): Promise<void> {
  await page.goto(SESSION_PAGE, { waitUntil: 'commit' })
  await page.waitForLoadState('load')
  await expect(page.locator('[data-testid="session-timeline"]')).toBeVisible({ timeout: 15_000 })
}

test.describe('详情页 C 头部 · 状态 / 任务 / 反馈 / Agent', () => {
  test.use({ viewport: { width: 375, height: 667 } })

  test('状态点与任务列表是两个独立入口，各自打开自己的浮层', async ({ page }) => {
    await openSession(page)

    const status = page.locator('[data-testid="session-status"]')
    const tasks = page.locator('[data-testid="session-tasks"]')
    await expect(status, '状态点按钮不存在').toBeVisible()
    await expect(tasks, '任务列表按钮不存在').toBeVisible()

    // 形状判据：状态点无边框；任务按钮有 1px 描边 + 圆形。
    // 这是"两个入口不是同一个按钮"的直接证据（也是用户反馈的原话）。
    const statusBorder = await status.evaluate((el) => getComputedStyle(el).borderTopWidth)
    const tasksBorder = await tasks.evaluate((el) => getComputedStyle(el).borderTopWidth)
    expect(statusBorder, '状态点不该有按钮边框').toBe('0px')
    expect(tasksBorder, '任务列表按钮应有描边').toBe('1px')

    // 状态：点开是状态详情，agent 在浮层里可见
    await status.click()
    await expect(page.locator('[data-testid="session-status-pop"]')).toBeVisible()
    await expect(page.locator('[data-testid="session-status-agent"]')).toContainText('Claude Code')
    await page.keyboard.press('Escape')
    await expect(page.locator('[data-testid="session-status-pop"]')).toHaveCount(0)

    // 其他任务：点开顶部下拉；最多 3 个（夹具里远多于 3 个）
    await tasks.click()
    const pop = page.locator('[data-testid="session-tasks-pop"]')
    await expect(pop).toBeVisible()
    await expect(pop).toContainText('其他运行中任务')
    await expect(pop.locator('[data-testid^="session-other-task-"]')).toHaveCount(3)
    // 待决策置顶：第一条任务行带「需决策」状态
    await expect(pop.locator('[data-testid^="session-other-task-"]').first()).toContainText('需决策')
    await page.keyboard.press('Escape')
    await expect(pop).toHaveCount(0)
  })

  test('反馈是方形无文字按钮，新建是实心圆；agent/用量在输入条', async ({ page }) => {
    await openSession(page)

    const feedback = page.locator('[data-testid="session-feedback"]')
    const newTask = page.locator('[data-testid="session-new-task"]')
    await expect(feedback).toBeVisible()
    await expect(newTask).toBeVisible()

    // 反馈：no visible text（图标按钮）；新建：有加号图标。
    expect((await feedback.innerText()).trim(), '反馈按钮不该有可见文字').toBe('')
    await expect(newTask.locator('svg')).toBeVisible()

    // 方形 vs 实心圆：半径/背景是两套完全不同的视觉语言
    const fbStyle = await feedback.evaluate((el) => {
      const s = getComputedStyle(el)
      return { radius: s.borderTopLeftRadius, bg: s.backgroundColor }
    })
    const newStyle = await newTask.evaluate((el) => {
      const s = getComputedStyle(el)
      return { radius: s.borderTopLeftRadius, bg: s.backgroundColor }
    })
    expect(parseFloat(fbStyle.radius), '反馈应为方形（8px 圆角）').toBeLessThan(999)
    expect(parseFloat(newStyle.radius), '新建应为圆形').toBeGreaterThan(999)
    expect(newStyle.bg, '新建应是 accent 实心').not.toBe('rgba(0, 0, 0, 0)')

    // agent + 用量都在输入条；当前任务在夹具里是等待决策（输入禁用），仍然可见
    const agent = page.locator('[data-testid="composer-agent"]')
    await expect(agent).toBeVisible()
    await expect(agent).toContainText('Claude Code')
    await expect(page.locator('[data-testid="composer-actions"] [role="progressbar"]')).toBeVisible()
  })
})
