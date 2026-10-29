// 「还没有工作目录」提示的两条真机判据（2026-10-29 线上截图回归）。
//
// 线上现象：项目已经选好（摘要写着 D:\code\go\pieqi、下拉框显示 pieqi），**点下创建、
// 按钮已经转圈**的那一刻，红字却跳出来说"还没有工作目录"。用户据此以为刚才的选择没生效。
//
// 成因（旧实现）：提示的判据写成 `!canSubmit && canSubmitContent`，而 canSubmit 里带
// `creating`（正在提交）—— 于是"提交中"被误报成"没有目录"（见 NewTaskPage 的 missingWorkdir）。
//
// 所以这里钉两条，缺一条都不算修好：
//   ① 提交中：**不许**出现这句（这正是线上截图那个状态）；
//   ② 真的没目录：**必须**出现（防止有人干脆把提示删掉换绿）。
//
// 运行：npx playwright test --project=interaction -g "还没有工作目录"
import { test, expect } from '@playwright/test'

/** 夹具里那条长会话的项目（与 new-task-entry.spec.ts 同一份；夹具用正斜杠） */
const LONG_PROJECT = 'D:/code/go/pieqi'

const missingHint = (page: import('@playwright/test').Page) => page.locator('[data-testid="project-missing"]')

test.describe('新建任务 · 「还没有工作目录」提示', () => {
  test('提交中不误报：项目已选好时，creating 期间不许说"没有工作目录"', async ({ page }) => {
    /**
     * 把 POST /api/tasks **挂住**。
     *
     * 为什么必须挂住：`creating` 的真实窗口只有一次往返（本地几毫秒，线上走隧道
     * 也就几百毫秒），用例想在那段时间里断言，靠"点完立刻查"必然是随机绿/红。
     * 挂住请求 = 把窗口拉到用例可控，且**不改被测代码**。
     */
    let heldPosts = 0
    /** 每个被挂住的请求一个放行句柄（用数组是为了不让 TS 把变量窄化成 null） */
    const releaseHeld: Array<() => void> = []
    await page.route('**/api/tasks', async (route) => {
      if (route.request().method() !== 'POST') return route.fallback()
      heldPosts += 1
      await new Promise<void>((resolve) => {
        releaseHeld.push(resolve)
      })
      // 用例已经断言完，别再真往夹具写：直接掐掉，页面停在当前状态即可
      await route.abort()
    })

    // 预选的 Windows 形态（\\ 与 / 由 groupKey 归一，和夹具的 D:/code/go/pieqi 是同一个项目）
    await page.goto(`/tasks/new?project=${encodeURIComponent('D:\\code\\go\\pieqi')}`, { waitUntil: 'load' })
    await expect(page.getByLabel('任务描述')).toBeVisible()

    // 前置：预选真的生效了 —— 否则这条用例测的是"没目录时也不报"，毫无意义
    await expect(page.locator('[data-testid="project-summary"]')).toContainText(LONG_PROJECT)
    await expect(page.locator('select[aria-label="历史项目"]')).toHaveValue(LONG_PROJECT)
    await expect(missingHint(page), '有目录却先报了一句').toHaveCount(0)

    await page.getByLabel('任务描述').fill('提交中不该说没有工作目录')
    const submit = page.locator('[data-testid="composer-submit"]')
    await expect(submit).toBeEnabled()
    await submit.click()

    // 判据不是"按钮变灰"（别的原因也会灰），而是**请求已经发出、还没回来**
    await expect.poll(() => heldPosts, { timeout: 5_000 }).toBeGreaterThan(0)
    await expect(submit, '提交中按钮应禁用（说明确实处在 creating 窗口里）').toBeDisabled()
    // ★ 回归点：旧实现此刻会冒出这句假提示
    await expect(missingHint(page), '项目已选好，提交中却报"还没有工作目录"').toHaveCount(0)

    for (const release of releaseHeld) release()
  })

  test('真的没目录时仍然会报（提示不是被删掉了）', async ({ page }) => {
    // 预选一个**不在历史项目里**的目录 ⇒ 页面落在"手填路径"形态（见 NewTaskPage 的预选块）。
    // 夹具的 settingsResponse 本来就没配 default_project_path，所以把输入框清空
    // 就是"真的没有任何工作目录"这个状态。
    await page.goto(`/tasks/new?project=${encodeURIComponent('D:\\zz\\no\\such\\project')}`, { waitUntil: 'load' })
    const input = page.locator('input[placeholder^="输入绝对路径"]')
    await expect(input).toHaveValue('D:\\zz\\no\\such\\project')

    await page.getByLabel('任务描述').fill('没有目录时要能说出为什么发不出去')
    await expect(missingHint(page), '有目录却先报了一句').toHaveCount(0)

    await input.fill('')
    await expect(missingHint(page), '真的没有目录，却没给任何提示').toBeVisible()
    await expect(page.locator('[data-testid="composer-submit"]')).toBeDisabled()
  })
})
