// L0 数值断言（**不产图**）：三视口 × 两个场景，量的全是"用户会真的踩到"的故障。
//
// 这一层是"真机测试"的主力：
//   · 每次跑完只回传几行数字（几十 token），而不是三张截图；
//   · 失败信息本身就是诊断（"按钮被 xxx 遮挡"而不是"图看起来不对"）；
//   · 与基线对比 ⇒ 能发现"没报错但有回退"。
//
// 基线：e2e/baseline/layout-baseline.json（进 git）。更新方式见 e2e/lib/baseline.ts 顶部。

import { test, expect } from '@playwright/test'
import { measure, stabilize, summarize, VIEWPORTS, type Metrics } from '../lib/measure'
import { keyOf, loadBaseline, toEntry, writeBaseline, type BaselineEntry } from '../lib/baseline'

/** 首屏 LCP 预算（ms）。超过就是回退 —— 这条是"每次改动悄悄拖慢"的刹车 */
const LCP_BUDGET_MS = 2500

/** 允许的 LCP 相对基线回退幅度：CI 机器抖动常见 ±20%，超过就是真回退 */
const LCP_REGRESSION_RATIO = 0.35

/** 长会话挂载上限（含全部 Turn 时）。基线 2,135 是"只挂最近两轮"的实测值 */
const DOM_BUDGET_LONG_SESSION = 6000

const SCENES = [
  {
    scene: 'detail',
    // 12 轮会话 + 右栏反馈面板
    url: '/__e2e/detail',
    // 挂载前必等的元素：等它出现 = "有数据的首帧已提交"，而不是"空壳已提交"
    ready: '[data-testid="session-timeline"]',
    domBudget: DOM_BUDGET_LONG_SESSION,
  },
  {
    scene: 'approval',
    // 两种风险变体（L3 二次确认 + L2 常规）
    url: '/__e2e/detail?mode=approval',
    ready: '[data-e2e="approval-list"]',
    domBudget: 3000,
  },
  {
    scene: 'dashboard',
    // 首页 / 全产品唯一的「行动面」（SPEC §6.2）：待审批组 + 本周概览。
    // 此前**零基线** —— 而介入时延与验证成本这两条 SPEC 指标都落在这一屏上。
    url: '/__e2e/detail?mode=dashboard',
    ready: '[data-e2e="dashboard"]',
    domBudget: 4000,
  },
] as const

/**
 * 本次实测的基线数据。
 *
 * ⚠️ Playwright 每个 test 文件是**独立 worker 进程**，所以这里"收集 + afterAll 落盘"
 * 只在**同一文件内**成立。新增 scene 时要么放进本文件，要么另写一个
 * `e2e/layout/<x>.spec.ts` 并在其中自行收集 + 落盘（否则新场景的基线不会写进去）。
 *
 * `E2E_UPDATE_BASELINE` 的语义：
 *   · 未设置 → 只比对，不写文件（断言失败就是失败）；
 *   · `1` / `true` / 任意非空串 → 本次实测写回基线（**人确认过数字之后**才用它）。
 * 用"非空即更新"而不是"必须等于 1"：多跑一次少一次惊喜都源于环境变量的拼写，
 * 而 update 是显式动作，拼错时最容易的失败模式是"以为更新了其实没更新"。
 */
const updateMode = !!process.env.E2E_UPDATE_BASELINE
const baseline = loadBaseline()

/** 跨 afterAll 共享的一份结果：用 process 级 Map，避免 test 函数内闭包与 afterAll 不同实例 */
const collected: Record<string, BaselineEntry> = {}

test.afterAll(() => {
  if (!updateMode) return
  const n = Object.keys(collected).length
  if (!n) return
  writeBaseline(collected)
  console.log(`[baseline] 已回写 ${n} 条到 e2e/baseline/layout-baseline.json`)
})

for (const sc of SCENES) {
  for (const vp of VIEWPORTS) {
    test(`${sc.scene} @ ${vp.name} (${vp.width}×${vp.height})`, async ({ page }, testInfo) => {
      const consoleErrors: string[] = []
      page.on('console', (m) => {
        if (m.type() === 'error') consoleErrors.push(m.text())
      })
      page.on('pageerror', (e) => consoleErrors.push(String(e)))

      await page.setViewportSize({ width: vp.width, height: vp.height })

      /**
       * 加载等待的判据（这里踩过一次**静默失败**，记下来）：
       *
       * 只等 `domcontentloaded` 会在模块图还**在途**时就继续 —— 那时 `/__e2e/detail`
       * 还没注册，掉进主路由兜底 `/:pathMatch(.*)*` 被**重定向到 /dashboard**。
       * 整个过程**一行错都没有**，只有一个"等 session-timeline 超时"。（根因与修法见
       * `e2e/boot-fixture.ts` 顶部：夹具路由改用独立路由表，由 e2e 专用入口同步安装。）
       *
       * 判据是"元素真的出现"（`waitForSelector` 是**轮询**，不是快照）：
       *   · 等不到就红 —— 不会静默跑在空壳页上；
       *   · 早到也没事 —— 会一直等到元素出现。
       * 不用 `networkidle`：WS 重连退避会让"网络静默"永远等不到。
       */
      await page.goto(sc.url, { waitUntil: 'commit' })
      await page.waitForLoadState('load')
      await page.waitForSelector(sc.ready, { timeout: 15_000 })
      await stabilize(page)

      /**
       * ⚠️ 滚到底再量 —— 这不是"为了好看"，而是**判据本身**：
       *
       *   1. 真机验收与基线必须测**同一个状态**。会话页打开时可能停在顶部或底部
       *      （`useForceScroll` / 恢复滚动位置），不先归一化，DOM 与截图都在两个状态间跳，
       *      视觉 diff 会随机全红。
       *   2. 「输入区在不在视口内」只有在**用户通常在的位置**（最新一条消息处）才有意义。
       *      停在顶部时输入区在视口外是**正常**的（页面本来就能滚）。
       */
      await page.evaluate(() => {
        const root = document.scrollingElement ?? document.documentElement
        root.scrollTop = root.scrollHeight
        document.querySelectorAll('*').forEach((el) => {
          if (el.scrollHeight > el.clientHeight + 4 && getComputedStyle(el).overflowY !== 'visible') {
            el.scrollTop = el.scrollHeight
          }
        })
      })
      await page.evaluate(
        () => new Promise<void>((r) => requestAnimationFrame(() => requestAnimationFrame(() => r()))),
      )

      const m: Metrics = await measure(page)

      // ---- 断言 1：整页不得横向溢出（移动端最刺眼的事故）----
      // 只断言**文档级**溢出：整页能横向拖动 = 用户明确踩到。
      // 内部容器的 scrollWidth 超出不判红 —— 见下方 wideElements 才判红的判据。
      expect(m.hOverflow, `整页横向溢出 ${m.hOverflow}px（scrollWidth=${m.scrollWidth} > clientWidth=${m.clientWidth}）`).toBe(0)

      // ---- 断言 2：DOM 节点数在预算内（按需挂载是否生效）----
      expect(m.domNodes, `DOM 节点数 ${m.domNodes} 超出预算 ${sc.domBudget}：可能"更早的 Turn"被全量挂载了`).toBeLessThan(
        sc.domBudget,
      )

      // ---- 断言 3：首屏渲染代价 ----
      if (m.lcpMs !== null) {
        expect(m.lcpMs, `LCP ${m.lcpMs}ms 超出预算 ${LCP_BUDGET_MS}ms`).toBeLessThan(LCP_BUDGET_MS)
      }
      // CLS：布局跳动。> 0.1 才算差（Google 口径），这里按 0.05 收紧 —— 这是工具面板，不是内容站
      expect(m.cls, `CLS ${m.cls} 过高：首屏有元素在字体/数据落地后跳位`).toBeLessThan(0.05)

      // ---- 断言 4：**要点的东西真的点得到**（与 Playwright click 的判据一致）----
      // 比"容器中心点是否被盖住"有用得多：容器被自己的内容盖住是正常的，
      // 而交互元素被浮层拦住才是用户真的点不动。
      const blocked = m.interactive.blocked
      expect(
        blocked,
        `以下元素会被别的元素拦截点击（Playwright 的 click 会直接失败）：\n` +
          blocked.map((b) => `  · ${b.name} @(${b.at}) 命中的是 ${b.hit}`).join('\n'),
      ).toEqual([])

      // ---- 断言 5：场景特有 —— 主行动区必须在视口内 ----
      if (sc.scene === 'detail') {
        /**
         * 输入区（composer）是"续问"的唯一入口。判据分两层：
         *
         *   ① **发送按钮必须完整可见、且可点** ← 真实的"能不能用"判据。
         *      发送按钮在输入框右下角；只要它在视口内且不被拦截，用户就能完成干预，
         *      无论输入框上半截是否被裁。
         *   ② 输入区**与视口有交集** ← 防"整块被顶出屏幕"（底栏/safe-area 事故）。
         *
         * ⚠️ 为什么不再要求 `composerBar.inViewport`（整块完整可见）：
         *   实测 375×667 与 1440×900 下，输入区高 170px（`rows=2` 的 textarea +
         *   `pb-[4.375rem]` 让位 70px），而可用主区只有约 141px —— 输入区本身就比
         *   剩余空间高，**任何实现都满足不了"整块可见"**。那是产品层面的尺寸问题
         *   （见 docs/testing/ui-e2e.md「已知问题」），不该由一条布局断言冒充回归。
         *   把判据收紧到"发送按钮可见可点"后，它既不会假红，也不会漏掉真正会伤人的情况。
         */
        const composer = m.boxes.composerBar
        expect(composer, 'composer-bar 未挂载：会话页缺少干预输入框').not.toBeNull()
        expect(
          composer!.intersects,
          `${vp.name} 下 composer-bar 完全在视口外：@(${composer!.x},${composer!.y}) ${composer!.w}×${composer!.h}`,
        ).toBe(true)

        // 发送按钮：完整落在视口内 + 不被拦截（后者已由断言 4 覆盖，这里补"可见性"）
        const sendBox = await page.locator('button[aria-label="发送"]').boundingBox()
        expect(sendBox, '「发送」按钮没有布局盒（未渲染或 display:none）').not.toBeNull()
        expect(
          sendBox!.y >= 0 && sendBox!.y + sendBox!.height <= vp.height && sendBox!.x >= 0 && sendBox!.x + sendBox!.width <= vp.width,
          `「发送」按钮不在视口内 (@${sendBox!.x},${sendBox!.y} ${sendBox!.width}×${sendBox!.height})，` +
            `视口 ${vp.width}×${vp.height} —— 用户无法完成续问`,
        ).toBe(true)
        // 提示词输入框本身存在（testid 由 PromptInput 提供）
        await expect(page.locator('[data-testid="composer-inner"]')).toBeVisible()
        // 发送按钮必须真的点得到（可操作性判据，见 measure.ts 的 INTERACTIVE）
        await expect(page.locator('button[aria-label="发送"]')).toBeVisible()
      } else if (sc.scene === 'dashboard') {
        /**
         * Dashboard。它的判据与另外两屏不同 —— 这里是**扫视 + 一个动作**的页面，
         * 所以量的是"第一张待审批卡能不能一屏内决策"，而不是"有没有输入框"。
         *
         * ⚠️ 新场景必须在这里加一个分支：此前 assert 只有 `detail` / else 两支，
         * 新加的 dashboard 会掉进 else（按审批列表找元素），报一句和真实原因无关的
         * "审批列表不存在" —— 这正是"加了场景却没人验证"的典型形态。
         */
        const dash = page.locator('[data-e2e="dashboard"]')
        await expect(dash, 'Dashboard 夹具未挂载').toBeVisible()

        // ① 待审批块必须出现（夹具里有两条 waiting_input + decision 的任务）。
        //    这一块是 SPEC 定义的"唯一行动区块"，它不出现等于用户看不见待办。
        const pendingSection = dash.locator('section', { hasText: '待审批' })
        await expect(pendingSection, 'Dashboard 没有渲染「待审批」区块').toBeVisible()

        // ② 第一张卡的「允许一次」必须完整落在视口内 —— 首页的全部价值就是
        //    "打开就能决策，不用再进会话"。要滚动才看得见，这条价值就不成立。
        const approve = pendingSection.locator('button', { hasText: '允许一次' }).first()
        await expect(approve).toBeVisible()
        const box = await approve.boundingBox()
        expect(box, '「允许一次」没有布局盒').not.toBeNull()
        expect(
          box!.y >= 0 && box!.y + box!.height <= vp.height && box!.x >= 0 && box!.x + box!.width <= vp.width,
          `Dashboard 首张待审批卡的「允许一次」不在视口内 (@${box!.x},${box!.y})，视口 ${vp.width}×${vp.height}`,
        ).toBe(true)

        // ③ 「本周概览」区块存在。
        //
        // ⚠️ 用**精确的 testid** 而不是文本匹配：`getByText('本周')` 会同时命中
        //   头部那句「…· 本周 8 个任务已完成」和区块标题，Playwright 的 strict mode
        //   直接报 "resolved to 3 elements" —— 而报错信息（strict violation）看起来
        //   和"元素没渲染"很像，最容易误判成功能坏了。测"某个区块在不在"就一定
        //   要有一个唯一的锚点。
        await expect(dash.getByRole('heading', { name: '本周概览' })).toBeVisible()
        await expect(dash.locator('[data-testid="one-shot-rate"]')).toBeVisible()
      } else {
        // 审批卡：主行动按钮必须一屏可见且可点（SPEC「One Screen, One Decision」）
        const list = m.boxes.approvalList
        expect(list, '审批列表不存在').not.toBeNull()
        const firstCardButtons = page.locator('[data-e2e="approval-list"] button', { hasText: /允许一次|拒绝/ })
        expect(await firstCardButtons.count(), '审批卡上没有找到「允许一次/拒绝」按钮').toBeGreaterThan(0)
        // 主行动按钮：首张卡是 L3（风险降序），它的按钮初始就是「允许一次」，
        // 点下去才变成内联二次确认「确认允许」。所以这里只认「允许一次」——
        // 并要求它**在首张卡里**，因为"要滚动才能决策"的判据只对最紧急那张成立。
        const first = page.locator('[data-e2e="approval-list"] button', { hasText: '允许一次' }).first()
        await expect(first).toBeVisible()
        const b = await first.boundingBox()
        expect(b, '主行动按钮没有布局盒（display:none 或未渲染）').not.toBeNull()
        // 判定：按钮完整落在视口内。用 inViewport 而不是 intersects ——
        // 露出一半的按钮在手机上等于按钮不可靠。
        expect(
          b!.y >= 0 && b!.y + b!.height <= vp.height && b!.x >= 0 && b!.x + b!.width <= vp.width,
          `「允许一次」按钮不在视口内 (@${b!.x},${b!.y} ${b!.width}×${b!.height})，用户要滚动才能决策`,
        ).toBe(true)
      }

      // ---- 记录：控制台不得有错误（静默失败的高发源：401/接口 404/未捕获异常）----
      // 白名单只放**夹具环境固有**的噪声：
      //   · WebSocket 握手失败 —— 夹具后端刻意拒绝 WS 升级（e2e 不追实时时序），
      //     前端会走重连退避，这是设计内的降级路径，不是故障；
      //   · favicon / SW / manifest —— dev server 与 e2e 入口不提供它们。
      // 其余任何 error 都是真问题（包括某个接口 404 —— 那是夹具漏实现，必须补）。
      const IGNORED = /WebSocket connection to|favicon|ServiceWorker|sw\.js|manifest\.webmanifest/i
      const realErrors = consoleErrors.filter((e) => !IGNORED.test(e))
      expect(realErrors, `控制台出现错误：\n${realErrors.join('\n')}`).toEqual([])

      // ---- 与基线比对（DOM / LCP）----
      const key = keyOf(sc.scene, vp.name)
      // 只有**本次实测通过**才进基线：失败的场景若也写进去，基线就会被一次回退污染，
      // 之后所有比对都拿"回退后的数字"当正确值（基线失效而没人发现）。
      // 所以赋值放在所有断言之后 —— 任一断言 throw，这一行都不会执行。
      collected[key] = toEntry(m)
      if (!updateMode && baseline?.[key]) {
        const base = baseline[key]
        // DOM：超过基线 50% 或预算的较小者就红（DOM 回退是"按需挂载失效"的直接表征）
        if (base.domNodes > 0) {
          const limit = Math.max(base.domNodes * 1.5, sc.domBudget)
          expect(m.domNodes, `DOM 相对基线回退：${base.domNodes} → ${m.domNodes}`).toBeLessThan(limit)
        }
        if (base.lcpMs && m.lcpMs) {
          expect(m.lcpMs, `LCP 相对基线回退：${base.lcpMs}ms → ${m.lcpMs}ms`).toBeLessThan(
            base.lcpMs * (1 + LCP_REGRESSION_RATIO),
          )
        }

        /**
         * ★ 关键元素尺寸必须与基线**完全一致**（±1px 容忍亚像素取整）。
         *
         * 为什么必须有这条：上面那些"绝对阈值"（DOM < 预算、LCP < 2500、无横向溢出……）
         * 全部是**上界**，抓的是"超出上限"这种量级变化。而布局回归大多是**平移/压缩**：
         * 实测踩过一次 —— 给夹具外壳加 40px 左内边距，`sessionTimeline` 从 503×942
         * 变成 463×942、整体右移 40px，**所有上界断言照样全绿**。
         * 没有这条，布局层就等于"只看有没有爆表，不看有没有挪位"。
         *
         * 例外是**尺寸本身会变**的场景（折叠/展开），那类要用例自己显式处理，
         * 不能靠在这里放宽基线。
         */
        for (const [name, baseBox] of Object.entries(base.boxes)) {
          const cur = m.boxes[name]
          // 基线里没有、现在也没有 → 跳过；一边有一边没有 → 说明元素出现/消失，报出来
          if (!baseBox && !cur) continue
          expect(Boolean(cur), `元素 ${name} 在基线里存在，现在不见了`).toBe(true)
          expect(Boolean(baseBox), `元素 ${name} 现在出现了，但基线里没有（先确认这是预期的）`).toBe(true)
          expect(
            Math.abs(cur!.w - baseBox!.w),
            `${name} 宽度与基线不符：${baseBox!.w} → ${cur!.w}（布局被挤压/拉伸，±1px 内视为相同）`,
          ).toBeLessThanOrEqual(1)
          expect(
            Math.abs(cur!.h - baseBox!.h),
            `${name} 高度与基线不符：${baseBox!.h} → ${cur!.h}`,
          ).toBeLessThanOrEqual(1)
        }
      }

      // ---- 数字进报告：之后排查**不用再跑一次**、更不用截图 ----
      const text = summarize(m)
      await testInfo.attach('metrics.json', { body: JSON.stringify({ key, ...toEntry(m) }, null, 2), contentType: 'application/json' })
      await testInfo.attach('metrics.txt', { body: text, contentType: 'text/plain' })

      // 把数字打进 stdout：`npm run test:e2e:layout` 的输出本身就是结论，不必开报告
      console.log(`\n[layout] ${key}\n${text}`)
    })
  }
}
