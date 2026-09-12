// AC-R8-01 真机实测：输入条与正文左右边界在 1280 / 768 / 375 三档视口一致。
//
// ⚠️ 靶点是 **web（dev server / dist）**，不是 `docs/ui-design/prototype/index.html`。
// 原型那套 `_validate/_verify/_measure` 验的是原型自身，对 `web/src` 的改动**零覆盖** ——
// 拿它们给 R8 做出口条件会「验完等于没验」。本脚本是 web 侧的补充口径，不替代原型回归。
//
// 用法：node scripts/_measure-composer.mjs
//   依赖：本机 Chrome + 已起的后端（:3000，仅用于取一个真实 session id）+ web dev server（:5174）
import { chromium } from 'playwright'

const BASE = process.env.BASE ?? 'http://127.0.0.1:5174'
const API = process.env.API ?? 'http://127.0.0.1:3000'
const CHROME = process.env.CHROME ?? 'C:/Program Files/Google/Chrome/Application/chrome.exe'
const TOL = 1 // px：亚像素舍入容差

const VIEWPORTS = [
  { name: '1280', width: 1280, height: 900 },
  { name: '768', width: 768, height: 900 },
  { name: '375', width: 375, height: 812 },
]

/** 取一个真实任务 id（详情页要能渲染出时间线，不然对齐无从谈起） */
async function pickTaskId() {
  const res = await fetch(`${API}/api/tasks`)
  const data = await res.json()
  for (const p of data.projects ?? []) {
    const t = (p.tasks ?? []).find((x) => x.status === 'completed') ?? (p.tasks ?? [])[0]
    if (t?.id) return t.id
  }
  throw new Error('后端没有可用的任务 —— 详情页无法渲染')
}

/** 在页面里量一对元素：正文块 vs 输入条内层（Playwright 只收一个参数，故打包成对象） */
function measure({ refSel, targetSel }) {
  const ref = document.querySelector(refSel)
  const target = document.querySelector(targetSel)
  const r = (el) => {
    const b = el.getBoundingClientRect()
    return { left: +b.left.toFixed(2), right: +b.right.toFixed(2), w: Math.round(b.width) }
  }
  const ta = document.querySelector('[data-testid="composer-inner"] textarea')
  const send = document.querySelector('[data-testid="composer-inner"] button')
  const bar = document.querySelector('[data-testid="composer-bar"]')
  const after = send ? getComputedStyle(send, '::after') : null
  return {
    ref: ref ? r(ref) : null,
    target: target ? r(target) : null,
    // 输入条外条宽度：它与正文块宽度之差 = **修复前**的错位量
    // （旧写法把 px-3 直接挂在满宽外条上，没有 max-w-3xl 内层）
    bar: bar ? r(bar) : null,
    textarea: ta ? r(ta) : null,
    sendH: send ? Math.round(send.getBoundingClientRect().height) : 0,
    // 命中区 = 视觉高 + 伪元素上下外扩（AC-R8-04 允许视觉 36）
    hitH: send ? Math.round(send.getBoundingClientRect().height - parseFloat(after.top) - parseFloat(after.bottom)) : 0,
  }
}

const rows = []
let fail = 0

const browser = await chromium.launch({ executablePath: CHROME, headless: true })
try {
  const taskId = await pickTaskId()
  for (const vp of VIEWPORTS) {
    const page = await browser.newPage({ viewport: { width: vp.width, height: vp.height } })
    // 页面加载期会暴露 Vue 的 props/fallthrough 警告 —— 这类警告在单测里看不见，
    // 而它们正是"事件没落到真实元素上"的早期信号（斜杠补全的 keydown 依赖 fallthrough）。
    const noises = []
    page.on('console', (m) => {
      if (m.type() === 'error' || m.type() === 'warning') noises.push(`[${m.type()}] ${m.text()}`)
    })
    page.on('pageerror', (e) => noises.push(`[pageerror] ${e.message}`))

    for (const [label, path, refSel] of [
      ['详情页', `/sessions/${taskId}`, '[data-testid="session-timeline"] > div'],
      ['新建任务页', '/tasks/new', '[data-testid="page-content"]'],
    ]) {
      await page.goto(`${BASE}${path}`, { waitUntil: 'domcontentloaded' })
      await page.waitForSelector('[data-testid="composer-inner"] textarea', { timeout: 15000 })
      await page.waitForTimeout(500) // 等布局稳定（含右栏展开/收起）
      // 交互冒烟：只输入、不提交（不碰真实任务）
      const taSel = '[data-testid="composer-inner"] textarea'
      await page.fill(taSel, '/')
      await page.waitForTimeout(250)
      const menuShown = await page.locator('[class*="bottom-full"]').count()
      await page.fill(taSel, '')
      const m = await page.evaluate(measure, { refSel, targetSel: '[data-testid="composer-inner"]' })
      rows.push({ vp: vp.name, label, menu: menuShown, ...m })
    }
    if (noises.length) {
      fail++
      console.log(`⚠️ ${vp.name} 控制台噪声 ${noises.length} 条：`)
      for (const n of [...new Set(noises)].slice(0, 6)) console.log(`   ${n.slice(0, 200)}`)
    }
    await page.close()
  }
} finally {
  await browser.close()
}

console.log('视口   页面          正文左/右        输入条左/右      Δ左   Δ右  输入框宽  命中区  菜单  修复前错位')
for (const r of rows) {
  const dL = +(r.target.left - r.ref.left).toFixed(2)
  const dR = +(r.target.right - r.ref.right).toFixed(2)
  const ok = Math.abs(dL) <= TOL && Math.abs(dR) <= TOL
  if (!ok) fail++
  // 旧写法：px-3 md:px-4 挂在满宽外条上、没有 max-w-3xl 内层 → 输入框从 bar.left + 内边距起排
  const pad = r.vp === '375' ? 12 : 16
  const before = Math.round(r.ref.left - (r.bar.left + pad))
  console.log(
    `${r.vp.padEnd(6)} ${r.label.padEnd(12)} ${String(r.ref.left).padStart(7)}/${String(r.ref.right).padStart(7)}  ` +
      `${String(r.target.left).padStart(7)}/${String(r.target.right).padStart(7)}  ` +
      `${String(dL).padStart(5)} ${String(dR).padStart(5)}  ${String(r.textarea.w).padStart(7)}  ${String(r.hitH).padStart(5)}  ` +
      `${String(r.menu).padStart(4)}  ${String(before).padStart(9)}px  ${ok ? 'OK' : '✗ 错位'}`,
  )
}
console.log(`\n对齐 ${rows.length - fail}/${rows.length} 通过（容差 ${TOL}px）`)
console.log('「修复前错位」= 正文块左边界 −（外条左边界 + 内边距 px-3/md:px-4）。答案：**错位只发生在 >768**')
console.log('  —— ≤768 时 max-w-3xl(768) 不生效，正文块本就与输入条同线；>768 正文块居中而输入条满宽，两者分家。')
process.exit(fail ? 1 : 0)
