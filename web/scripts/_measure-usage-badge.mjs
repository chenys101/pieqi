// 用量角标的真机验证：直接灌一条 WS `task_usage` 事件（与 agent 上报走**同一条**路径），
// 看角标是否出现、几何是否合理、是否把标题挤没。
//
// 为什么走 WS 而不是等真 agent 上报：本机 dsh 的 ACP initialize 就失败（模型路由没配好），
// qoder 本轮也在 Git Bash 首次安装期报 500 —— 两者都到不了"产出 usage"那一步。
// 而 `task_usage` 的**消费端**（normalizer → dispatcher → taskStore → UsageBadge）
// 才是这次要验的东西；从 WS 注入一条等价载荷，覆盖的就是那段真实代码。
import { chromium } from 'playwright'

const CHROME = process.env.CHROME ?? 'C:/Program Files/Google/Chrome/Application/chrome.exe'
const BASE = process.env.BASE ?? 'http://127.0.0.1:3101'
const TASK = process.env.TASK
const VIEWPORTS = [
  { name: 'mobile', width: 390, height: 844 },
  { name: 'tablet', width: 768, height: 900 },
  { name: 'desktop', width: 1280, height: 900 },
]

const browser = await chromium.launch({ executablePath: CHROME, headless: true })
const rows = []
const problems = []
try {
  for (const vp of VIEWPORTS) {
    const page = await browser.newPage({ viewport: { width: vp.width, height: vp.height } })
    const errs = []
    page.on('pageerror', (e) => errs.push(e.message))
    page.on('console', (m) => {
      if (m.type() === 'error') errs.push(m.text())
    })

    await page.goto(`${BASE}/sessions/${TASK}`, { waitUntil: 'domcontentloaded' })
    await page.waitForSelector('[data-testid="composer-inner"] textarea', { timeout: 20000 })
    await page.waitForTimeout(1200)

    const before = await page.evaluate(() => {
      const h = document.querySelector('header')
      return {
        headerH: Math.round(h.getBoundingClientRect().height),
        titleW: Math.round(document.querySelector('header h1')?.getBoundingClientRect().width ?? 0),
        titleText: document.querySelector('header h1')?.textContent?.trim() ?? '',
        hasBadge: !!document.querySelector('[role="progressbar"]'),
      }
    })

    // 用页面**自己的** WS 连接不可行（组件内部持有），改为直接调 store？
    // 也不行 —— store 不在 window 上。最稳的做法：从后端触发一次真实广播。
    // 见脚本外的 orchestration：调用 POST /api/__test/broadcast_usage。
    // 这里只负责：等角标出现 → 量几何。
    await page
      .waitForFunction(() => !!document.querySelector('[role="progressbar"]'), { timeout: 15000 })
      .catch(() => {})

    const after = await page.evaluate(() => {
      const h = document.querySelector('header')
      const badge = document.querySelector('[role="progressbar"]')
      const host = badge?.parentElement
      const title = document.querySelector('header h1')
      const fill = badge?.firstElementChild
      const br = badge?.getBoundingClientRect()
      const hr = host?.getBoundingClientRect()
      const tr = title?.getBoundingClientRect()
      return {
        headerH: Math.round(h.getBoundingClientRect().height),
        hasBadge: !!badge,
        badgeW: br ? Math.round(br.width) : 0,
        badgeH: br ? Math.round(br.height) : 0,
        fillW: fill ? Math.round(fill.getBoundingClientRect().width) : 0,
        hostText: host?.textContent?.trim() ?? '',
        titleW: tr ? Math.round(tr.width) : 0,
        titleText: title?.textContent?.trim() ?? '',
        hostLeft: hr ? Math.round(hr.left) : 0,
        hostTop: hr ? Math.round(hr.top) : 0,
        titleRight: tr ? Math.round(tr.right) : 0,
        titleTop: tr ? Math.round(tr.top) : 0,
        titleBottom: tr ? Math.round(tr.bottom) : 0,
        // 标题是否被截断（truncate 生效时 scrollWidth > clientWidth）
        titleClipped: title ? title.scrollWidth > title.clientWidth + 1 : false,
      }
    })

    rows.push({ vp: vp.name, before, after })
    if (!after.hasBadge) problems.push(`${vp.name}: 注入 task_usage 后角标仍未出现`)
    if (after.badgeW === 0 || after.badgeH === 0) problems.push(`${vp.name}: 角标尺寸为 0`)
    // 重叠判据必须**先判是否同一行**：角标在移动端与标题同行（头部那一行），
    // 在桌面档则在**元信息行**（标题下方）。只比左右边界会把"上下两行"误报成重叠
    // —— 2026-10-09 就是这么误报了一次。判定条件：垂直区间有交集才算同一行。
    const sameRow = after.titleBottom > after.hostTop && after.titleTop < after.hostTop + after.badgeH
    if (sameRow && after.titleRight > after.hostLeft + 2) {
      problems.push(
        `${vp.name}: 同行内标题右(${after.titleRight}) 与角标左(${after.hostLeft}) 重叠`,
      )
    }
    if (errs.length) problems.push(`${vp.name}: 控制台 error —— ${errs[0].slice(0, 120)}`)
    await page.close()
  }
} finally {
  await browser.close()
}

console.log('===== 用量角标（真机，WS 注入 task_usage）=====')
console.log('视口      头部高(前→后)  角标 尺寸    填充宽 文本            标题宽(前→后) 标题被截断')
for (const r of rows) {
  console.log(
    `${r.vp.padEnd(9)} ${String(r.before.headerH).padStart(3)}→${String(r.after.headerH).padEnd(3)}      ` +
      `${String(r.after.hasBadge).padEnd(4)} ${String(`${r.after.badgeW}×${r.after.badgeH}`).padEnd(7)} ` +
      `${String(r.after.fillW).padStart(5)}  ${r.after.hostText.padEnd(14)} ` +
      `${String(r.before.titleW).padStart(4)}→${String(r.after.titleW).padEnd(4)} ${String(r.after.titleClipped).padEnd(5)}`,
  )
}
console.log('')
if (problems.length) {
  console.log(`❌ ${problems.length} 个问题：`)
  for (const p of problems) console.log(`   - ${p}`)
} else {
  console.log('✅ 角标出现、尺寸非 0、未与标题重叠、无控制台 error')
}
process.exit(problems.length ? 1 : 0)
