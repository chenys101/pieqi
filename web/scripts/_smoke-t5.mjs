// T5/T6 冒烟：真实任务详情页上验证 R3 分组渲染 + Turn↔反馈面板联动。
// 靶点 = web dev server（不是原型 HTML —— 靶点错配教训）。
// 断言：① 分组头渲染（或旧任务平铺不报错）② 「查看本轮变更」→ 面板出现
// ring-accent 的高亮 TurnCard 且 data-turn 一致（AC-R3-06）③ 控制台零 error。
import { createRequire } from 'node:module'
const require = createRequire(import.meta.url)
const { chromium } = require('playwright')

const BASE = 'http://127.0.0.1:5174'
const CHROME = process.env.CHROME ?? 'C:/Program Files/Google/Chrome/Application/chrome.exe'

let taskId = null
let withTurns = null
try {
  const res = await fetch('http://127.0.0.1:3000/api/tasks')
  const data = await res.json()
  const ids = (data.projects ?? []).flatMap((p) => p.tasks ?? []).map((t) => t.id)
  taskId = ids[0] ?? null
  // 优先找一个真有 TurnInfo 的（旧任务 turns=[]，走平铺兜底路径）
  for (const id of ids) {
    try {
      const fb = await (await fetch(`http://127.0.0.1:3000/api/tasks/${id}/feedback`)).json()
      if ((fb.turns ?? []).length > 0) { withTurns = id; break }
    } catch { /* 跳过 */ }
  }
} catch { /* 后端不在 */ }
if (!taskId) {
  console.log('SKIP：无可用任务')
  process.exit(0)
}
if (withTurns) taskId = withTurns
// 外部可强制指定任务（如测旧任务平铺：SMOKE_TASK_ID=<旧任务id>）
if (process.env.SMOKE_TASK_ID) taskId = process.env.SMOKE_TASK_ID

const browser = await chromium.launch({ executablePath: CHROME, headless: true })
const page = await browser.newPage({ viewport: { width: 1280, height: 800 } })
const errors = []
page.on('console', (m) => m.type() === 'error' && errors.push(m.text()))
page.on('pageerror', (e) => errors.push(String(e)))

await page.goto(`${BASE}/sessions/${taskId}`, { waitUntil: 'domcontentloaded', timeout: 20000 })
// 等时间线渲染出分组头或平铺事件（旧任务两种都合法）
await page.waitForSelector('[data-testid="session-timeline"] > div > *', { timeout: 15000 })
await page.waitForTimeout(1200) // 等 /feedback 回来（增强头部）

const groups = await page.locator('[data-testid^="turn-group-"]').count()
const headers = await page.locator('[data-testid^="turn-header-"]').count()
const links = await page.locator('[data-testid="turn-link"]').count()
console.log(`分组=${groups} 头部=${headers} 链接=${links}`)

let ok = true
if (groups > 0) {
  // AC-R3-01：头数 = 组数（前导区无头，组数 ≥ 头数）
  if (headers > groups) { console.log('✗ 头部多于分组'); ok = false }
  // 同源数字：头部里有 "个文件"（info 已到）
  const hasFiles = await page.locator('[data-testid^="turn-header-"]', { hasText: '个文件' }).count()
  console.log(`带文件数头部=${hasFiles}`)

  if (links > 0) {
    await page.locator('[data-testid="turn-link"]').first().click()
    await page.waitForTimeout(600) // 展开面板 + 定位
    const rail = page.locator('[data-testid="feedback-panel"]')
    const highlighted = rail.locator('[data-turn][class*="ring-accent"]')
    const n = await highlighted.count()
    const turnAttr = n ? await highlighted.first().getAttribute('data-turn') : null
    // 与点击的链接所在组比对（first link → 第一组）
    const firstGroup = await page.locator('[data-testid^="turn-group-"]').first().getAttribute('data-testid')
    console.log(`面板高亮=${n} data-turn=${turnAttr} 点击组=${firstGroup}`)
    if (n === 0) { console.log('✗ 面板没有高亮 TurnCard'); ok = false }
    else if (firstGroup !== `turn-group-${turnAttr}`) { console.log('✗ 高亮 Turn 与点击组不一致（AC-R3-06）'); ok = false }
  } else {
    console.log('（无查看链接：bundle 尚未到/落后 —— 不判失败，增强是异步的）')
  }
} else {
  console.log('旧任务形态：平铺渲染（AC-R3-04 兜底路径）')
  const evs = await page.locator('[data-testid="session-timeline"] > div > *').count()
  if (evs === 0) { console.log('✗ 平铺也空白'); ok = false }
}

if (errors.length) { console.log(`✗ 控制台错误 ${errors.length} 条:`); errors.slice(0, 5).forEach((e) => console.log('  ', e.slice(0, 200))); ok = false }
console.log(ok ? 'SMOKE OK' : 'SMOKE FAIL')
await browser.close()
process.exit(ok ? 0 : 1)
