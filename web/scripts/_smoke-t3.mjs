// T3 冒烟：迁移过的页面逐个打开，收控制台 error/pageerror（零容忍）。
// 靶点 = web dev server 本身（不是原型 HTML —— 靶点错配教训）。
import { createRequire } from 'node:module'
const require = createRequire(import.meta.url)
const { chromium } = require('playwright')

const BASE = 'http://127.0.0.1:5174'
// 本机 Chrome（playwright 自带浏览器未安装；与 _measure-composer.mjs 同款启动）
const CHROME = process.env.CHROME ?? 'C:/Program Files/Google/Chrome/Application/chrome.exe'
const routes = [
  { name: '设置页', path: '/settings', expect: ['input', 'select'] },
  // /tasks 是移动端专用页（>768px 路由守卫重定向到仪表盘）→ 必须用移动视口
  { name: '任务列表', path: '/tasks', expect: ['input[type="search"]', 'select'], mobile: true },
  { name: '新建任务', path: '/tasks/new', expect: ['select'] },
]

// 取一个真实任务 id（响应结构 = { projects: [{ tasks: [...] }] }），把详情页也过一遍
let sessionId = null
try {
  const res = await fetch('http://127.0.0.1:3000/api/tasks')
  const data = await res.json()
  sessionId = data.projects?.[0]?.tasks?.[0]?.id ?? null
} catch { /* 后端不在也不阻塞冒烟 */ }
if (sessionId) routes.push({ name: '任务详情', path: `/sessions/${sessionId}`, expect: ['textarea'] })

const browser = await chromium.launch({ executablePath: CHROME, headless: true })
let fail = 0
for (const r of routes) {
  const page = await browser.newPage(
    r.mobile ? { viewport: { width: 375, height: 667 } } : undefined,
  )
  const problems = []
  page.on('console', (m) => m.type() === 'error' && problems.push(`console: ${m.text()}`))
  page.on('pageerror', (e) => problems.push(`pageerror: ${e.message}`))
  try {
    await page.goto(BASE + r.path, { waitUntil: 'domcontentloaded', timeout: 15000 })
    for (const sel of r.expect) {
      // /api/tasks 是 5MB 级响应，TasksPage 数据就绪才渲染工具栏 → 耐心等首个目标选择器
      await page.locator(sel).first().waitFor({ state: 'attached', timeout: 20000 })
    }
  } catch (e) {
    // 失败时记录页面处于哪个分支，便于定位是数据没到还是真缺控件
    const states = {
      spinner: await page.locator('svg.animate-spin').count(),
      errorBox: await page.locator('text=加载失败').count(),
      empty: await page.locator('text=还没有任务').count(),
    }
    problems.push(`等待 ${e.message.split('\n')[0]}；分支=${JSON.stringify(states)}`)
  }
  const ok = problems.length === 0
  if (!ok) fail++
  console.log(`${ok ? 'OK ' : '✗  '} ${r.name} ${r.path}${ok ? '' : ' → ' + problems.join(' | ')}`)
  await page.close()
}
await browser.close()
console.log(fail === 0 ? `\n冒烟 ${routes.length}/${routes.length} 通过` : `\n冒烟失败 ${fail}/${routes.length}`)
process.exit(fail === 0 ? 0 : 1)
