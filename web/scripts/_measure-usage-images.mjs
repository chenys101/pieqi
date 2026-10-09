// 用量角标 + 图片入口的**真机**验证（尺寸/可见性/遮挡 —— jsdom 量不出来的那些）。
//
// 为什么必须真机：单测（UsageBadge.spec.ts / composer.spec.ts）只能验"渲染了没有、
// 文本对不对"，而这两个 feature 的关键风险恰恰都是**几何**的：
//   - 用量角标挤进移动端头部那一行 → 标题会不会被压没？
//   - 图片缩略图条在输入框上方 → 会不会盖住文字 / 把输入框顶出可视区？
// 这些只有真实浏览器 + getBoundingClientRect 能给答案（见 AGENTS.md 的 UI 验收纪律）。
//
// 用法：
//   $env:BASE="http://127.0.0.1:3101"; $env:API="http://127.0.0.1:3101"; `
//   $env:TASK="<task-id>"; node scripts/_measure-usage-images.mjs
// CHROME 可用环境变量覆盖。
import { chromium } from 'playwright'

const BASE = process.env.BASE ?? 'http://127.0.0.1:3101'
const API = process.env.API ?? BASE
const CHROME = process.env.CHROME ?? 'C:/Program Files/Google/Chrome/Application/chrome.exe'
const TASK = process.env.TASK ?? ''

const VIEWPORTS = [
  { name: 'mobile', width: 390, height: 844 },
  { name: 'tablet', width: 768, height: 900 },
  { name: 'desktop', width: 1280, height: 900 },
]

/** 取一个有事件的任务（详情页要能渲染，否则量不到输入框） */
async function pickTaskId() {
  if (TASK) return TASK
  const res = await fetch(`${API}/api/tasks`)
  const data = await res.json()
  for (const p of data.projects ?? []) {
    const t = (p.tasks ?? [])[0]
    if (t?.id) return t.id
  }
  throw new Error('后端没有可用任务')
}

/**
 * 页面内探针：量两件事的**几何**。
 *
 * 返回原始像素数而不是"OK/不 OK"：判据留在 Node 侧，探针只负责读数 ——
 * 把阈值写进页面会让"为什么没过"变成一个黑盒。
 */
function probe() {
  const rect = (el) => {
    if (!el) return null
    const b = el.getBoundingClientRect()
    return {
      x: +b.x.toFixed(1),
      y: +b.y.toFixed(1),
      w: Math.round(b.width),
      h: Math.round(b.height),
      top: +b.top.toFixed(1),
      bottom: +b.bottom.toFixed(1),
      left: +b.left.toFixed(1),
      right: +b.right.toFixed(1),
    }
  }
  const vis = (el) => {
    if (!el) return false
    const b = el.getBoundingClientRect()
    const s = getComputedStyle(el)
    return b.width > 0 && b.height > 0 && s.visibility !== 'hidden' && s.display !== 'none'
  }

  // --- 用量角标 ---
  const badge = document.querySelector('[role="progressbar"]')
  const badgeHost = badge?.parentElement ?? null
  const title = document.querySelector('header h1')
  const fill = badge?.firstElementChild ?? null

  // --- 图片入口 ---
  const addBtn = document.querySelector('[data-testid="composer-add-image"]')
  const composerInner = document.querySelector('[data-testid="composer-inner"]')
  const textarea = document.querySelector('[data-testid="composer-inner"] textarea')
  const actionsRow = document.querySelector('[data-testid="composer-actions"]')

  return {
    usage: {
      present: !!badge,
      visible: vis(badge),
      rect: rect(badge),
      hostRect: rect(badgeHost),
      // 进度条填充宽度：比例是否真的体现在视觉上（而非只有文本）
      fillW: fill ? Math.round(fill.getBoundingClientRect().width) : 0,
      text: badgeHost?.textContent?.trim() ?? '',
    },
    title: { rect: rect(title), text: title?.textContent?.trim() ?? '' },
    images: {
      addPresent: !!addBtn,
      addVisible: vis(addBtn),
      addRect: rect(addBtn),
      inputAccept: document.querySelector('[data-testid="composer-image-input"]')?.accept ?? '',
      // 缩略图条（有图时才存在）
      stripPresent: !!document.querySelector('[data-testid="composer-images"]'),
    },
    composer: { innerRect: rect(composerInner), textareaRect: rect(textarea), actionsRect: rect(actionsRow) },
    viewport: { w: window.innerWidth, h: window.innerHeight },
    // 白屏判定：SPA 挂载失败的典型表现是 #app 空 + 无时间线
    appChildren: document.getElementById('app')?.children.length ?? 0,
    bodyTextLen: (document.body.innerText ?? '').length,
  }
}

const taskId = await pickTaskId()
const browser = await chromium.launch({ executablePath: CHROME, headless: true })
const rows = []
const problems = []

try {
  for (const vp of VIEWPORTS) {
    const page = await browser.newPage({ viewport: { width: vp.width, height: vp.height } })
    const noises = []
    page.on('console', (m) => {
      if (m.type() === 'error') noises.push(m.text())
    })
    page.on('pageerror', (e) => noises.push(`[pageerror] ${e.message}`))

    await page.goto(`${BASE}/sessions/${taskId}`, { waitUntil: 'domcontentloaded' })
    await page.waitForSelector('[data-testid="composer-inner"] textarea', { timeout: 20000 })
    // 等能力探测与首屏布局稳定（能力是异步 HTTP，早量会拿到"还没出现"的假阴性）
    await page.waitForTimeout(1500)

    const m = await page.evaluate(probe)
    rows.push({ vp: vp.name, ...m })

    // --- 判据 ---
    if (m.appChildren === 0 || m.bodyTextLen < 40) {
      problems.push(`${vp.name}: 疑似白屏（#app 子节点 ${m.appChildren}，正文 ${m.bodyTextLen} 字）`)
    }
    if (m.usage.present && !m.usage.visible) {
      problems.push(`${vp.name}: 用量角标在 DOM 里但不可见（尺寸 0 或被隐藏）`)
    }
    // 角标与标题不得重叠：这是"挤进头部那一行"的直接风险
    if (m.usage.present && m.title.rect && m.usage.hostRect) {
      const overlap = m.title.rect.right > m.usage.hostRect.left + 1
      // 标题是 flex-1 truncate，正常会在角标之前被截断；重叠说明布局坏了
      if (overlap && m.title.rect.right > m.usage.hostRect.left + 2) {
        problems.push(
          `${vp.name}: 标题右边界(${m.title.rect.right}) 侵入用量角标左边界(${m.usage.hostRect.left})`,
        )
      }
    }
    // 加图按钮必须与发送按钮同一行（#lead 行），且不越出输入框
    if (m.images.addVisible && m.images.addRect && m.composer.innerRect) {
      const outside =
        m.images.addRect.left < m.composer.innerRect.left - 1 ||
        m.images.addRect.right > m.composer.innerRect.right + 1 ||
        m.images.addRect.bottom > m.composer.innerRect.bottom + 1
      if (outside) {
        problems.push(
          `${vp.name}: 加图按钮越出输入框（btn ${JSON.stringify(m.images.addRect)} vs inner ${JSON.stringify(m.composer.innerRect)}）`,
        )
      }
    }
    // 输入框必须留在可视区内（图片条若把框顶下去，这里会报）。
    //
    // ⚠️ 判据用 **textarea**（可交互元素）的底边，不是 composer-inner 的。
    // inner 的底边包含 `pb-[4.375rem]`(70px) 那块**专门用来放动作按钮的空白**，
    // 它天然会伸到可视区外（动作按钮是绝对定位的，不占流）。拿 inner 判会得到
    // 一个恒假的失败 —— 2026-10-09 就是这么先误判成"我的改动顶掉了输入框"的。
    if (m.composer.textareaRect && m.composer.textareaRect.bottom > m.viewport.h + 0.5) {
      problems.push(
        `${vp.name}: 输入框底边(${m.composer.textareaRect.bottom}) 超出视口高(${m.viewport.h}) —— 输入区被裁`,
      )
    }
    if (noises.length) problems.push(`${vp.name}: 控制台 error ${noises.length} 条 —— ${noises[0].slice(0, 160)}`)

    await page.close()
  }
} finally {
  await browser.close()
}

console.log('===== 用量角标 / 图片入口 真机实测 =====')
console.log(`任务: ${taskId}`)
console.log('')
console.log('视口     用量角标 可见 尺寸(w×h)  填充宽  标题宽  加图按钮 位置(x,y)      输入框底边 视口高')
for (const r of rows) {
  const u = r.usage
  console.log(
    `${r.vp.padEnd(8)} ${String(u.present).padEnd(8)} ${String(u.visible).padEnd(4)} ` +
      `${String(u.rect ? `${u.rect.w}×${u.rect.h}` : '-').padEnd(10)} ` +
      `${String(u.fillW).padStart(5)}  ${String(r.title.rect ? r.title.rect.w : 0).padStart(6)}  ` +
      `${String(r.images.addVisible).padEnd(8)} ` +
      `${String(r.images.addRect ? `${r.images.addRect.x},${r.images.addRect.y}` : '-').padEnd(14)} ` +
      `${String(r.composer.innerRect ? r.composer.innerRect.bottom : '-').padStart(10)} ${String(r.viewport.h).padStart(6)}`,
  )
}
console.log('')
console.log(`用量角标文字样本: ${rows.map((r) => JSON.stringify(r.usage.text)).join(' | ')}`)
console.log(`加图按钮 accept: ${rows[0]?.images.inputAccept || '(未渲染)'}`)
console.log('')
if (problems.length) {
  console.log(`❌ 发现 ${problems.length} 个问题：`)
  for (const p of problems) console.log(`   - ${p}`)
} else {
  console.log('✅ 无问题：无白屏、无控制台 error、角标/加图按钮均在框内且不与标题重叠')
}
process.exit(problems.length ? 1 : 0)
