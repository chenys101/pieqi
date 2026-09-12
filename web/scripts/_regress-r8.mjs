// T4：R8 三档视觉回归基线（web 靶点 —— 靶点错配修正后取代原型 _measure.mjs 的角色）。
//
// 用法：
//   node scripts/_regress-r8.mjs           # 与 _baseline-r8.json 比对，漂移即失败
//   node scripts/_regress-r8.mjs --update  # 重新生成基线（仅在有意变更视觉后使用）
//
// 覆盖的 AC：R8-01 对齐 Δ=0、R8-03 焦点 ring 3px、R8-04 命中区 ≥44、R8-05 圆角 6px、
//            控件在位、控制台零 error/pageerror（R8-11 由静态 grep 另行断言）。
// 前提：vite dev server（:5174）+ pieqi 后端（:3000）。/tasks 是移动端专用页（>768 重定向）。
import { createRequire } from 'node:module'
import { readFileSync, writeFileSync, existsSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const require = createRequire(import.meta.url)
const { chromium } = require('playwright')
const CHROME = process.env.CHROME ?? 'C:/Program Files/Google/Chrome/Application/chrome.exe'
const BASE = 'http://127.0.0.1:5174'
const UPDATE = process.argv.includes('--update')
const HERE = dirname(fileURLToPath(import.meta.url))
const BASELINE = join(HERE, '_baseline-r8.json')

const VIEWPORTS = [
  { name: '1280', width: 1280, height: 800 },
  { name: '768', width: 768, height: 1024 },
  { name: '375', width: 375, height: 667 },
]

// 取一个真实任务 id（响应结构 = { projects: [{ tasks: [...] }] }）
let taskId = null
try {
  const res = await fetch('http://127.0.0.1:3000/api/tasks')
  const data = await res.json()
  taskId = data.projects?.[0]?.tasks?.[0]?.id ?? null
} catch { /* 后端不在则详情页缺测，其余照跑 */ }

// 每个路由在哪些视口测（tasks 仅 375 —— 桌面会被路由守卫重定向，测了是假样本）
const routes = [
  { key: 'detail', path: taskId ? `/sessions/${taskId}` : null, vps: ['1280', '768', '375'], wait: '[data-testid="composer-inner"] textarea' },
  { key: 'newtask', path: '/tasks/new', vps: ['1280', '768', '375'], wait: '[data-testid="composer-inner"] textarea' },
  { key: 'settings', path: '/settings', vps: ['1280', '768', '375'], wait: 'input' },
  { key: 'tasks', path: '/tasks', vps: ['375'], wait: 'input[type="search"]' },
].filter((r) => r.path)

// 在页面里量：对齐（composer 内层 vs 正文参照）、命中区、圆角、焦点 ring、控件计数。
// ⚠️ page.evaluate 只收一个参数 → 全部打包成对象。
// ⚠️ 控件带 transition-[border-color,box-shadow]：focus 后必须等过渡跑完（150ms）再读
//    boxShadow，否则读到的是过渡起点 0px —— 不是 ring 没生效。
async function measure() {
  const box = (el) => {
    if (!el) return null
    const r = el.getBoundingClientRect()
    return { l: +r.left.toFixed(2), r: +r.right.toFixed(2), w: Math.round(r.width) }
  }
  const ta = document.querySelector('[data-testid="composer-inner"] textarea')
  const inner = document.querySelector('[data-testid="composer-inner"]')
  const ref = document.querySelector('[data-testid="session-timeline"] > div') ?? document.querySelector('[data-testid="page-content"]')
  const send = document.querySelector('[data-testid="composer-inner"] button')
  // 命中区 = 视觉高 + ::after 上下外扩（AC-R8-04 允许视觉 36）
  let hit = 0
  if (send) {
    const a = getComputedStyle(send, '::after')
    const h = send.getBoundingClientRect().height
    hit = Math.round(h - parseFloat(a.top) - parseFloat(a.bottom) + (parseFloat(a.height) || 0))
    if (!Number.isFinite(hit) || hit <= 0) hit = Math.round(h)
  }
  // 焦点 ring（AC-R8-03）：聚焦后读 boxShadow，找 "3px"（Tailwind ring-[3px] 是 spread 3px）
  let ring3 = false
  const firstCtl = document.querySelector('input:not([type="checkbox"]):not([type="radio"]), textarea, select')
  if (firstCtl) {
    firstCtl.focus()
    await new Promise((r) => setTimeout(r, 250)) // 等 box-shadow 过渡（150ms）跑完
    ring3 = /3px/.test(getComputedStyle(firstCtl).boxShadow)
    firstCtl.blur()
  }
  const radius = ta ? getComputedStyle(ta).borderTopLeftRadius : null
  return {
    ref: box(ref),
    inner: box(inner),
    textarea: box(ta),
    hit,
    radius,
    ring3,
  }
}

const browser = await chromium.launch({ executablePath: CHROME, headless: true })
const results = {}
let fatal = 0

for (const route of routes) {
  for (const vpName of route.vps) {
    const vp = VIEWPORTS.find((v) => v.name === vpName)
    const page = await browser.newPage({
      viewport: { width: vp.width, height: vp.height },
      // /tasks 只在移动视口可达；其余路由双档都行 —— 统一按视口开页
    })
    const problems = []
    page.on('console', (m) => m.type() === 'error' && problems.push(m.text()))
    page.on('pageerror', (e) => problems.push(e.message))
    let m = null
    try {
      await page.goto(BASE + route.path, { waitUntil: 'domcontentloaded', timeout: 20000 })
      await page.locator(route.wait).first().waitFor({ state: 'attached', timeout: 20000 })
      await page.waitForTimeout(400) // 布局稳定
      m = await page.evaluate(measure)
    } catch (e) {
      problems.push(`等待失败: ${e.message.split('\n')[0]}`)
    }
    const key = `${route.key}@${vpName}`
    results[key] = {
      consoleErrors: problems,
      ref: m?.ref ?? null,
      inner: m?.inner ?? null,
      textarea: m?.textarea ?? null,
      hit: m?.hit ?? 0,
      radius: m?.radius ?? null,
      ring3: m?.ring3 ?? false,
    }
    await page.close()
  }
}
await browser.close()

// ---- 派生断言（不进基线，恒为硬条件）----
const hardFails = []
for (const [key, r] of Object.entries(results)) {
  if (r.consoleErrors.length) hardFails.push(`${key}: 控制台 ${r.consoleErrors.length} 条噪声`)
  if (r.ref && r.inner) {
    const dL = +(r.inner.l - r.ref.l).toFixed(2)
    const dR = +(r.inner.r - r.ref.r).toFixed(2)
    if (Math.abs(dL) > 0.5 || Math.abs(dR) > 0.5) hardFails.push(`${key}: 对齐 Δ=${dL}/${dR}`)
  }
  if (r.hit && r.hit < 44) hardFails.push(`${key}: 命中区 ${r.hit} < 44`)
  if (r.radius && r.radius !== '6px') hardFails.push(`${key}: 圆角 ${r.radius} ≠ 6px`)
  if (r.textarea && !r.ring3) hardFails.push(`${key}: 焦点 ring 未见 3px`)
}

// ---- 基线比对 ----
let drifts = []
if (UPDATE) {
  writeFileSync(BASELINE, JSON.stringify({ generatedAt: new Date().toISOString(), note: 'T4 R8 三档基线（web 靶点）', results }, null, 2) + '\n')
  console.log(`基线已写入 ${BASELINE}`)
} else {
  if (!existsSync(BASELINE)) {
    console.error('✗ 基线不存在 —— 先跑一次 --update 生成')
    process.exit(1)
  }
  const base = JSON.parse(readFileSync(BASELINE, 'utf8')).results
  for (const [key, r] of Object.entries(results)) {
    const b = base[key]
    if (!b) { drifts.push(`${key}: 基线缺此样本`); continue }
    const numEq = (a, c) => a == null || c == null ? a === c : Math.abs(a - c) <= 0.5
    const cmp = (name, a, c) => { if (!numEq(a, c)) drifts.push(`${key}.${name}: ${c} ≠ 基线 ${a}`) }
    cmp('ref.l', b.ref?.l, r.ref?.l); cmp('ref.r', b.ref?.r, r.ref?.r)
    cmp('inner.l', b.inner?.l, r.inner?.l); cmp('inner.r', b.inner?.r, r.inner?.r)
    cmp('textarea.w', b.textarea?.w, r.textarea?.w)
    cmp('hit', b.hit, r.hit)
    if (b.radius !== r.radius) drifts.push(`${key}.radius: ${r.radius} ≠ 基线 ${b.radius}`)
    if (b.ring3 !== r.ring3) drifts.push(`${key}.ring3: ${r.ring3} ≠ 基线 ${b.ring3}`)
  }
  for (const key of Object.keys(base)) {
    if (!results[key]) drifts.push(`${key}: 本次未产出（路由/视口丢了？）`)
  }
}

console.log(`样本 ${Object.keys(results).length} 个（${Object.keys(results).join(', ')}）`)
if (hardFails.length) {
  console.error(`✗ 硬断言失败 ${hardFails.length} 条:\n  - ${hardFails.join('\n  - ')}`)
  fatal += hardFails.length
}
if (drifts.length) {
  console.error(`✗ 基线漂移 ${drifts.length} 条:\n  - ${drifts.join('\n  - ')}`)
  fatal += drifts.length
}
if (!fatal) console.log(UPDATE ? '✅ 基线生成完成，硬断言全部通过' : '✅ 与基线一致，硬断言全部通过')
process.exit(fatal ? 1 : 0)
