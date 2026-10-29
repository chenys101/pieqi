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

  /**
   * 回归：「点状态弹层展示不全」（2026-10-29 用户反馈）。
   *
   * 根因是**布局**而不是内容：状态详情浮层原先以 28px 触发按钮为中心
   * （`left-1/2 -translate-x-1/2`）且宽度固定 224px。「其他任务」按钮在
   * `otherCount === 0` 时不渲染（用户手上只有一个在途任务就是这种常见形态），
   * 状态按钮于是成为右簇最左那个 ⇒ 面板右缘 = 按钮中心 + 112 越出视口。
   * 375 宽实测 [173, 397]：右缘 22px 被外层 `overflow-hidden` 裁掉，
   * 右对齐的 Agent / 项目 值只剩开头几个字（dsh→d、pieqi→pie）。
   *
   * 判据必须是**几何**的：jsdom 里 rect 全是 0，内容断言（"agent 存在"）
   * 在旧实现下同样是绿的 —— 这正是这个 bug 躲过既有用例的原因。
   */
  test.describe('只有一个任务时（otherCount === 0，头部右簇剩三个按钮）', () => {
    // mock 服务端的 /api/tasks 只回当前这一条。
    //
    // 为什么必须落在**服务端**：应用启动时 `setupProviders()` 会自己 loadTasks()，
    // 把夹具页在 beforeEnter 里灌的 store 覆盖成全量清单 —— 只在夹具页 seed 一条任务，
    // 「其他任务」按钮过一帧又出现，用例测的就不是出问题的布局了（静默假绿）。
    test.use({ extraHTTPHeaders: { 'x-e2e-solo': '1' } })

    for (const width of [320, 375, 430]) {
      test(`只有一个任务时（无「其他任务」按钮）状态浮层完整落在视口内 @${width}`, async ({ page }) => {
        await page.setViewportSize({ width, height: 667 })
        await page.goto(SESSION_PAGE, { waitUntil: 'commit' })
        await page.waitForLoadState('load')
        await expect(page.locator('[data-testid="session-timeline"]')).toBeVisible({ timeout: 15_000 })
        // 前置条件必须成立，否则这条用例测的不是出问题的布局（静默假绿的入口）
        await expect(page.locator('[data-testid="session-tasks"]'), '「其他任务」按钮不该渲染').toHaveCount(0)

        await page.locator('[data-testid="session-status"]').click()
        const pop = page.locator('[data-testid="session-status-pop"]')
        await expect(pop).toBeVisible()

        const report = await pop.evaluate((el) => {
          const pr = el.getBoundingClientRect()
          // 逐个溢出祖先核对：面板必须完全落在它的 padding box 内，否则就是被裁
          const clippedBy: Array<{
            el: string
            clipRight: number
            popRight: number
          }> = []
          for (let a = el.parentElement; a; a = a.parentElement) {
            const s = getComputedStyle(a)
            if (s.overflow === 'visible' && s.overflowX === 'visible' && s.overflowY === 'visible') continue
            const ar = a.getBoundingClientRect()
            if (pr.right > ar.right + 0.5 || pr.left < ar.left - 0.5) {
              clippedBy.push({
                el: `${a.tagName}.${String(a.className)}`,
                clipRight: ar.right,
                popRight: pr.right,
              })
            }
          }
          return {
            vw: document.documentElement.clientWidth,
            pop: { left: pr.left, right: pr.right, width: pr.width },
            clippedBy,
          }
        })
        expect(report.clippedBy, `状态浮层被祖先裁剪：${JSON.stringify(report.clippedBy)}`).toEqual([])
        expect(report.pop.left, '左缘越界').toBeGreaterThanOrEqual(0)
        expect(report.pop.right, '右缘越界').toBeLessThanOrEqual(report.vw + 0.5)

        // 值本身也不能被 CSS 截断（夹具里的 agent / 项目 都是短名，应完整可读）
        for (const id of ['session-status-agent', 'session-status-project']) {
          const val = page.locator(`[data-testid="${id}"] span`).last()
          const overflow = await val.evaluate((el) => ({
            scroll: el.scrollWidth,
            client: el.clientWidth,
          }))
          expect(overflow.scroll, `${id} 的值被截断`).toBeLessThanOrEqual(overflow.client)
        }

        await page.keyboard.press('Escape')
        await expect(pop).toHaveCount(0)
      })
    }
  })

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
