// 失败结论 reporter：把一次 `playwright test` 的**全部**失败收敛成一份 SUMMARY.md。
//
// 为什么不用 Playwright 自带的 html / list reporter：
//   · `list` 是把每个用例的结果平铺出来，失败的细节还要往上翻很多行；
//   · `html` 要开浏览器、要看图，且报告在 OUTPUT_DIR 里，与"一次读完的结论"相反。
//
// 这里复用了 Playwright 的 **json** reporter（它已经把所有断言/错误结构化好了），
// 只做两件它不做的事：
//   ① 按失败签名聚合 + 给出"实测 / 期望"一行结论；
//   ② 与**上一次运行**对比，直接说"已修好 / 仍红 / 新挂"。
//
// ⚠️ 顺序有意义：json 必须排在最后一个或与结论并列，因为它在 `onEnd` 里才拿到 stats；
// 我们的 `onEnd` 读的是 json 文件，所以启动前先删掉上一次的 json ——
// 否则一次崩溃/中断的运行会让下一轮读到旧结论（静默失败，见技能 silent-failure-checklist）。

import { existsSync, mkdirSync, readFileSync, unlinkSync, writeFileSync } from 'node:fs'
import type { FullResult, Reporter } from '@playwright/test/reporter'
import {
  JSON_REPORT_PATH,
  REPORT_DIR,
  SUMMARY_PATH,
  collectFailures,
  compare,
  loadLastFailures,
  renderSummary,
  rowOf,
  saveLastFailures,
  type RunSnapshot,
} from './lib/report'

export default class PieqiReporter implements Reporter {
  onBegin(): void {
    mkdirSync(REPORT_DIR, { recursive: true })
    // 上一轮的 json 必须清掉：本轮的 json 只在 onEnd 才写完，
    // 中途崩溃时残留的旧 json 会被当成"本轮结果"读进来。
    if (existsSync(JSON_REPORT_PATH)) {
      try {
        unlinkSync(JSON_REPORT_PATH)
      } catch {
        // 删不掉就算了：onEnd 会校验它存在与否
      }
    }
  }

  onEnd(result: FullResult): void {
    // 只有 JSON reporter 写完才有东西可读；没有就退化成"只有退出码"的结论
    if (!existsSync(JSON_REPORT_PATH)) {
      const msg = `# e2e ${result.status}\n\n没有拿到结构化结果（_playwright-report.json 不存在）——\n这通常意味着运行在产出结果前就退出了（编译错误 / 浏览器起不来 / 超时）。\n先看终端里的第一条错误，不要去看截图。\n`
      writeFileSync(SUMMARY_PATH, msg, 'utf-8')
      console.log(`\n[summary] 结果不可解析 → ${SUMMARY_PATH}\n${msg}`)
      return
    }

    let parsed: Parameters<typeof collectFailures>[0]
    try {
      parsed = JSON.parse(readFileSync(JSON_REPORT_PATH, 'utf-8'))
    } catch (e) {
      console.log(`[summary] 读取 JSON 报告失败：${String(e)}\n先看终端里的第一条错误，不要去看截图。`)
      return
    }

    const { failures, total, passed, unexpected } = collectFailures(parsed)
    const prev = loadLastFailures()
    /**
     * ⚠️ `lastFixed` 必须挂在 `now` 上，而不是只在写盘时算。
     *    渲染用的是 `now`，写盘用的是另一个对象 —— 第一版只在写盘那边带 `lastFixed`，
     *    于是"保持绿"这段永远判空（单测过是因为测试里手工给 `now` 塞了该字段）。
     *    教训：**渲染对象与持久化对象必须是同一个**。
     */
    const now: RunSnapshot = {
      at: new Date().toISOString(),
      keys: [...new Set(failures.map((f) => f.key))],
      rows: Object.fromEntries(failures.map((f) => [f.key, rowOf(f)])),
      lastFixed: prev?.lastFixed,
    }
    const cmp = prev ? compare(prev, now) : null
    // 本轮有"新修好"时覆盖沿用值；没有就保持上一轮的（见下方写盘注释）
    if (cmp?.fixed.length) {
      now.lastFixed = Object.fromEntries(cmp.fixed.map((k) => [k, prev?.rows[k] ?? k]))
    } else {
      now.lastFixed = prev?.lastFixed
    }
    // 诊断行：一眼看出"有没有读到上次的锚点"。这段代码只有跑测试时才执行，
    // 出问题时（比如锚点被别的进程清掉）必须有可观测信号，否则只能靠猜。
    console.log(
      `[summary] 锚点：${prev ? `上次失败 ${prev.keys.length} 条` : '无（首次运行）'}；` +
        `本次失败 ${now.keys.length} 条；` +
        (cmp ? `已修好 ${cmp.fixed.length} / 仍红 ${cmp.still.length} / 新挂 ${cmp.added.length}` : '无可比基线'),
    )

    // 出处只留 e2e/ 之后的部分：绝对路径对读的人毫无信息量，还长
    // ⚠️ 这里**必须**容忍 location 缺失：失败用例的 location 可能不全，
    //    而这段代码只有红的时候才执行（单测里专门钉过这一条）。
    for (const f of failures) {
      f.location = String(f.location ?? '').replace(/^.*\/e2e\//, 'e2e/')
    }
    const text = renderSummary(now, cmp, failures, { total, passed, unexpected }, prev?.rows)
    writeFileSync(SUMMARY_PATH, text, 'utf-8')

    /**
     * 更新对比锚点。两条判据都是实测校准的：
     *
     * ① **只有真的跑到了用例才更新** —— 一次 `-g` 写错的空运行不该清空锚点。
     * ② **全绿时保留"刚修好的那几条"作为 `lastFixed`，但不保留成"仍红"**：
     *    否则"红 → 绿 → 再跑一次"的第二次会显示"全部通过、什么都没变"，
     *    而人真正想知道的是"**上一轮修好的那几条，这一轮还绿着**"。
     *    第一版直接存空表，结果"已修好"只活一轮，第二步就看不到了。
     */
    if (total > 0) {
      // `now.lastFixed` 已在上面算好（新修好的覆盖、否则沿用上一轮），
      // 这里直接写盘 —— 渲染与持久化用**同一个对象**，不会再出现"文件里有、报告里没有"。
      saveLastFailures({ ...now, fixedCount: Object.keys(now.lastFixed ?? {}).length })
    }

    // 终端的最后一段就是结论：跑的人不必再去找文件
    console.log(`\n${text}\n[summary] ${SUMMARY_PATH}\n`)
  }
}
