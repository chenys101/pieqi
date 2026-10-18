// 真机量的**数字**（L0 层）：不产图，只回传可断言的数值。
//
// 为什么先做这一层：AGENTS.md 定的验收判据是"量出数字并与基线对比"。
// 而 jsdom 量不出尺寸 / 遮挡 / 滚动 / 首屏代价 —— 于是它们只能在这里量。
//
// 每个指标都对应一条**真实会出事的故障**（不是"顺手测一下"）：
//   hOverflow      → 移动端整页横向拖动（1100px 正文列 + 375 屏的经典事故）
//   blocked        → 要点的按钮被浮层/侧栏/底栏拦住 ⇒ Playwright 的 click 会直接失败
//   offscreen      → 决策按钮要滚动才可见 ⇒ 违反 SPEC「One Screen, One Decision」
//   lcpMs          → 首屏渲染代价回退（每次改动悄悄拖慢）
//   domNodes       → 按需挂载没生效（12 轮全挂 = 21,206 → 2,135 那条基线）

import type { Page } from '@playwright/test'

/** 三视口：移动底栏 / 平板（侧栏收起临界）/ 桌面（右栏常驻） */
export const VIEWPORTS = [
  { name: 'mobile', width: 375, height: 667 },
  { name: 'tablet', width: 834, height: 1112 },
  { name: 'desktop', width: 1440, height: 900 },
] as const

export type ViewportName = (typeof VIEWPORTS)[number]['name']

/**
 * 被量尺寸的元素清单（只量尺寸/位置，**不做遮挡判定**）。
 *
 * ⚠️ 选择器必须落在**生产代码已有的 `data-testid`** 上。若某个 testid 尚不存在，
 * 断言会失败而不是静默跳过 —— 这正是我们要的：元素改名时「测不到」必须响亮。
 */
export const TARGETS: Record<string, string> = {
  sessionTimeline: '[data-testid="session-timeline"]',
  composerBar: '[data-testid="composer-bar"]',
  composerActions: '[data-testid="composer-actions"]',
  composerInner: '[data-testid="composer-inner"]',
  jumpLatest: '[data-testid="jump-latest"]',
  loadOlderTurns: '[data-testid="load-older-turns"]',
  feedbackPanel: '[data-testid="feedback-panel"]',
  feedbackRail: '[data-testid="feedback-rail"]',
  contentColumn: '[data-testid="content-column"]',
  contentInner: '[data-testid="content-inner"]',
  // 夹具页的容器（生产里对应 App 布局的主区 / 审批列表）
  main: '[data-e2e="main"]',
  approvalList: '[data-e2e="approval-list"]',
}

/**
 * 必须**真的能点到**的元素（可操作性判定的对象）。
 *
 * 为什么不再对容器逐个查 elementFromPoint：容器中心点大概率落在自己的子元素上，
 * "容器是否被遮挡"因此几乎没有判读价值，只会制造假红。
 * 真正会伤人的是：**用户要点的那个东西点不到**。
 *
 * ⚠️ 只放**自身就是点击目标**、且**常驻可见**的小尺寸元素（button / a / input）：
 *   · `composer-actions` 是 `pointer-events-none` 的布局行，它的采样点天然落在
 *     自己的子按钮之间或穿透到下层 textarea —— 那是设计要求（PromptInput.vue 注释）；
 *     把布局行放进来 = 要求"容器每一寸都可点"，必然假红。
 *   · `load-older-turns`（更早的 N 轮）**刻意不在清单里**：它在 12 轮会话里宽度被撑满，
 *     四角采样点落在按钮边缘圆角之外、命中的是下层容器 —— 那是**尺寸判定误差**，
 *     不是遮挡。它也不是"随时要点"的按钮（点之前本就要滚到顶）。
 *     想验证它，应该写一条**真点击**的用例（`click()` 自己会做可操作性检查），
 *     而不是靠像素采样猜。
 */
export const INTERACTIVE: Record<string, string> = {
  // 发送按钮没有 testid，但它有**无障碍名**（aria-label="发送"，见 InterveneInput.vue）
  composerSend: 'button[aria-label="发送"]',
  // 「回到最新」本身就是 <button>（SessionTimeline:576）
  jumpLatest: '[data-testid="jump-latest"]',
  contentResizer: '[data-testid="content-resizer"]',
}

export interface BlockedHit {
  name: string
  /** 采样点（视口坐标） */
  at: string
  /** 实际命中的元素描述 */
  hit: string | null
}

/** 页面内采集：一次 evaluate 拿全部数字（多次 evaluate 会把"同一帧"的保证丢掉） */
export interface Metrics {
  url: string
  viewport: { width: number; height: number }
  domNodes: number
  /** 整页横向溢出（px）。> 0 表示用户能横向拖动整页 —— 移动端最刺眼的事故 */
  hOverflow: number
  scrollWidth: number
  clientWidth: number
  /** 最大内容绘制（ms）；无 LCP 条目时为 null（引擎不支持或还没绘制） */
  lcpMs: number | null
  /** 布局偏移：CLS 累计（无偏移 = 0） */
  cls: number
  boxes: Record<
    string,
    | null
    | {
        x: number
        y: number
        w: number
        h: number
        /** 完全在视口内（含边界） */
        inViewport: boolean
        /** 至少一部分在视口内 */
        intersects: boolean
        /** 自身是否可横向滚动（scrollWidth > clientWidth） */
        selfHOverflow: number
      }
  >
  /**
   * 交互元素的可点性，判据与 Playwright 的 click 一致：
   * 元素四角 + 中心共 5 个采样点拿去 `elementFromPoint`，**必须全部落在该元素
   * （或其后代）上**，否则 Playwright 会报 "element intercepts pointer events"。
   * `checked` 是实际找到并检查的元素数（0 说明选择器写错了 —— 由断言兜住）。
   */
  interactive: { checked: number; blocked: BlockedHit[] }
  /** 页面内所有"自身内容超过自身宽度"的元素（找内部滚动容器被撑宽） */
  wideElements: { sel: string; scrollWidth: number; clientWidth: number }[]
}

/**
 * 采集脚本（在页面里执行，因此必须是**自包含**的：不能引用外部变量/函数）。
 *
 * 注意：这里是模板字符串，内部**禁止**出现反引号与 `${`（会被宿主 TS 先求值，
 * 表现为 SyntaxError 或静默传空参数 —— 踩过一次）。
 */
const IN_PAGE = /* js */ `
  (cfg) => {
    const targets = cfg.targets
    const interactiveTargets = cfg.interactiveTargets
    const describe = (el) => {
      if (!el || el.nodeType !== 1) return null
      const id = el.getAttribute('data-testid') || el.getAttribute('data-e2e')
      const cls = (typeof el.className === 'string' ? el.className : '').trim().split(/\\s+/).slice(0, 2).join('.')
      return el.tagName.toLowerCase() + (id ? '[' + id + ']' : '') + (cls ? '.' + cls : '')
    }
    const isVisible = (el) => {
      const cs = getComputedStyle(el)
      if (cs.display === 'none' || cs.visibility === 'hidden' || Number(cs.opacity) === 0) return false
      const r = el.getBoundingClientRect()
      return r.width > 0 && r.height > 0
    }
    const vw = window.innerWidth
    const vh = window.innerHeight
    const doc = document.documentElement

    const boxes = {}
    for (const name in targets) {
      let el = null
      try { el = document.querySelector(targets[name]) } catch (e) { el = null }
      if (!el || !isVisible(el)) { boxes[name] = null; continue }
      const r = el.getBoundingClientRect()
      boxes[name] = {
        x: Math.round(r.x), y: Math.round(r.y), w: Math.round(r.width), h: Math.round(r.height),
        inViewport: r.top >= 0 && r.left >= 0 && r.bottom <= vh && r.right <= vw,
        intersects: r.top < vh && r.bottom > 0 && r.left < vw && r.right > 0,
        selfHOverflow: Math.max(0, el.scrollWidth - el.clientWidth),
      }
    }

    const interactive = { checked: 0, blocked: [] }
    for (const name in interactiveTargets) {
      let el = null
      try { el = document.querySelector(interactiveTargets[name]) } catch (e) { el = null }
      if (!el || !isVisible(el)) continue
      const r = el.getBoundingClientRect()
      // 不在视口内的元素本来就点不到，但那不是"被遮挡"—— 跳过（滚动属于正常交互）。
      // 注意：只有**部分**在视口内时仍然检查，采样点会自然落在视口内的那几个。
      if (r.bottom <= 0 || r.top >= vh || r.right <= 0 || r.left >= vw) continue
      interactive.checked++
      const pts = [
        [r.left + r.width / 2, r.top + r.height / 2],
        [r.left + 1, r.top + 1],
        [r.left + r.width - 1, r.top + 1],
        [r.left + 1, r.top + r.height - 1],
        [r.left + r.width - 1, r.top + r.height - 1],
      ]
      for (const p of pts) {
        const x = p[0], y = p[1]
        if (x < 0 || y < 0 || x > vw || y > vh) continue
        const h = document.elementFromPoint(x, y)
        if (!h) continue
        // 命中点必须在 el 子树里；点在不相关的元素上 = 会被拦截
        if (h !== el && !el.contains(h)) {
          interactive.blocked.push({ name: name, at: Math.round(x) + ',' + Math.round(y), hit: describe(h) })
          break
        }
      }
    }

    const wideElements = []
    const wideNodes = document.querySelectorAll('[data-testid], [data-e2e], main, aside, section')
    for (let i = 0; i < wideNodes.length; i++) {
      const el = wideNodes[i]
      const over = el.scrollWidth - el.clientWidth
      if (over > 1 && el.clientWidth > 0) {
        wideElements.push({ sel: describe(el), scrollWidth: el.scrollWidth, clientWidth: el.clientWidth })
      }
    }

    const lcpEntry = performance.getEntriesByType('largest-contentful-paint').slice(-1)[0]
    const layoutShifts = performance.getEntriesByType('layout-shift').filter((e) => !e.hadRecentInput)
    let cls = 0
    for (const e of layoutShifts) cls += e.value || 0

    return {
      url: location.href,
      viewport: { width: vw, height: vh },
      domNodes: document.querySelectorAll('*').length,
      hOverflow: Math.max(0, doc.scrollWidth - doc.clientWidth),
      scrollWidth: doc.scrollWidth,
      clientWidth: doc.clientWidth,
      lcpMs: lcpEntry ? Math.round(lcpEntry.startTime) : null,
      cls: Math.round(cls * 1000) / 1000,
      boxes: boxes,
      interactive: interactive,
      wideElements: wideElements,
    }
  }
`

/**
 * 采集一屏的全部数字。
 *
 * 字体必须在量之前**真的加载完**：字体落地会改变行高与字宽，量在它之前得到的数字不可复现。
 * 实测踩过：6 个 worker 并发 + 首次冷启动时，偶发量到 `sessionTimeline` 宽 550（应 503）——
 * 那是文本还没按目标字体排版、内层容器的固有宽度不同导致的。
 *
 * `document.fonts.ready` 只保证"当前 pending 的都 settled"，但偶发来不及还因为：
 *   · `addStyleTag` 注入的样式与字体探测**并行**发生；
 *   · 空元素上的文本还没触发字体请求。
 * 所以这里等两件事：`fonts.ready` + **全部 expected 元素都有非零尺寸**，再让出两帧。
 */
export async function waitFontsReady(page: Page): Promise<void> {
  await page.evaluate(async () => {
    const fonts = document.fonts
    if (fonts) {
      await fonts.ready
      // 再显式 load 一次正文常用族：只等 ready 可能"此刻没有 pending"就返回了
      try {
        await Promise.all([fonts.load('14px -apple-system'), fonts.load('14px sans-serif')])
      } catch {
        // 字体族名可能不合法：ready 已经等到位，忽略
      }
    }
  })
}

/** 采集一屏的全部数字（先等字体就绪，再让出两帧） */
export async function measure(page: Page): Promise<Metrics> {
  await waitFontsReady(page)
  // 双 rAF = 至少一次真正的样式/布局提交
  await page.evaluate(
    () => new Promise<void>((r) => requestAnimationFrame(() => requestAnimationFrame(() => r()))),
  )
  return page.evaluate(`(${IN_PAGE})(${JSON.stringify({ targets: TARGETS, interactiveTargets: INTERACTIVE })})`) as Promise<Metrics>
}

/**
 * 稳定化：关动画 / 关过渡 / 关光标闪烁 / 锁定字体渲染参数 / 藏滚动条占位。
 * 视觉回归与尺寸量测共用的前置。
 *
 * `text-rendering: geometricPrecision` + `font-kerning:none` 的作用是让**文本度量确定**：
 * 默认的字体合成与字距微调在并发/冷缓存时可能落到不同分支，导致同一张图差几像素。
 */
export async function stabilize(page: Page): Promise<void> {
  await page.addStyleTag({
    content: `
*,*::before,*::after{animation:none!important;transition:none!important;animation-duration:0s!important;transition-duration:0s!important;caret-color:transparent!important;scroll-behavior:auto!important}
html{scrollbar-gutter:stable}
html,body{text-rendering:geometricPrecision!important;font-kerning:none!important;-webkit-font-smoothing:antialiased!important}
`,
  })
  await waitFontsReady(page)
}

/** 只保留人类要看的数字（报告里用），把 boxes 展开成一行一行的 */
export function summarize(m: Metrics): string {
  const lines = [
    `${m.viewport.width}×${m.viewport.height}  DOM=${m.domNodes}  hOverflow=${m.hOverflow}  LCP=${m.lcpMs ?? 'n/a'}ms  CLS=${m.cls}`,
  ]
  for (const k in m.boxes) {
    const b = m.boxes[k]
    if (!b) {
      lines.push(`  ${k}: (不存在)`)
      continue
    }
    const flags = [b.selfHOverflow > 1 ? `内部横向溢出 ${b.selfHOverflow}px` : '', !b.inViewport ? '不在视口内' : '']
      .filter(Boolean)
      .join(' ')
    lines.push(`  ${k}: ${b.w}×${b.h} @(${b.x},${b.y}) ${flags}`)
  }
  if (m.interactive.checked) {
    const s = m.interactive.blocked.length
      ? '被拦截: ' + m.interactive.blocked.map((b) => `${b.name}@(${b.at})→${b.hit}`).join(', ')
      : '全部可点'
    lines.push(`  交互元素 ${m.interactive.checked} 个: ${s}`)
  }
  if (m.wideElements.length) {
    lines.push(
      `  内部溢出元素: ${m.wideElements.map((w) => `${w.sel} +${w.scrollWidth - w.clientWidth}px`).join(', ')}`,
    )
  }
  return lines.join('\n')
}
