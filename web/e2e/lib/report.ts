// 失败**结论**报告：把一次 e2e 运行的失败收敛成**一个文件 / 一段文本**。
//
// 这个模块要解决的问题只有一个：**"跑一次 e2e 要回读 N 份输出"**。
//
// 背景（实测，2026-10-19 的真实会话）：修一个输入框遮挡问题，用了 473 次 LLM 调用、
// 近 1 亿 token。其中 45.8% 花在"跑一次测试 → 回读输出 → 改一点 → 再跑一次"的重复段里。
// 重复的根源不是截图，而是**反馈的单位是一条命令的输出**：
//   · 一次跑几十个用例，失败信息散在几十个 `[layout] detail @ mobile` 块里；
//   · 每次重跑都要把整段输出重新读一遍（而模型每读一次都要为全量上下文付一次钱）；
//   · 于是"测试结果"成了上下文里最贵、复读率最高的东西。
//
// 本模块把"跑测试"这件事的产物改成**一次读完的结论**：
//   ① 每条失败只留**一行**：`scene@vp 断言 → 实测 / 期望 / 差多少`；
//   ② 按**失败签名**聚合：同一条断言在 6 个场景里挂，只写一行 ×6，而不是抄 6 遍；
//   ③ 首跑与重跑**自动对比**：`仍红 / 已修好 / 新挂红` 三个集合，直接回答
//      "我这次改动有没有接近目标"，而不是让人去比对两份输出。
//
// 落点固定在系统临时目录（与 playwright.config.ts 的 OUTPUT_DIR 同一判据：
// 一次性产物不进仓库），且**不随 retries 累积** —— 每次运行覆盖。

import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

export const REPORT_DIR = process.env.E2E_OUTPUT_DIR || join(tmpdir(), 'pieqi-e2e')

/**
 * 跨运行锚点（`last-failures.json`）的落点：**故意不放在 REPORT_DIR 里**。
 *
 * ⚠️ 踩过的坑（诊断花了 4 轮）：Playwright 在每次运行开始时会**清空 `outputDir`**
 * （`REPORT_DIR` 正是它的值，用来放 test-results 与失败截图）。锚点一旦放进去，
 * 就永远在 `onEnd` 读到之前被删掉 —— 表现是**每轮都显示"首次运行"**，
 * 而文件明明存在（删的是下一次运行前那一刻）。
 *
 * 判据：`outputDir` 属于"本轮产物"，锚点属于"跨轮状态"。两者的生命周期相反，
 * 不能放同一个目录。所以锚点单独占一个兄弟目录。
 */
export const STATE_DIR = join(REPORT_DIR, '..', 'pieqi-e2e-state')

/** `--reporter=json` 的产物位置：不能是 OUTPUT_DIR 本身（Playwright 会占用它） */
export const JSON_REPORT_PATH = join(REPORT_DIR, '_playwright-report.json')

/**
 * 人读 + 模型读的**结论**文件。
 *
 * 为什么是 `.md` 而不是 `.json`：它要被整段贴进上下文。JSON 的括号、转义与重复键
 * 会把真正有用的那几十个字稀释掉；而每次运行产出一份新的结论，读它比读日志便宜一个量级。
 */
export const SUMMARY_PATH = join(REPORT_DIR, 'SUMMARY.md')

/**
 * **上一轮失败的签名表**（跨运行保留）。这是"重跑对比"的锚：
 * 每次运行开始时读它，结束时写它。只有它能让报告说出"已修好 / 新挂了"。
 *
 * 为什么要在**上一次运行**里存，而不是在内存里：e2e 常常是"命令行跑一次、看结果、
 * 改代码、再跑一次"，两次之间是**不同进程**（甚至是不同的 agent 回合）。
 */
export const LAST_FAILURES_PATH = join(STATE_DIR, 'last-failures.json')

// ─── Playwright JSON reporter 的最小形状 ────────────────────────────────
// 只声明用到的字段：Playwright 的 JSON 结构很大，全量建模既没必要也容易过时。

interface PWError {
  message?: string
  stack?: string
  /**
   * ⚠️ 真正的出处在这里 —— `results[].error.location`，**不是** `test.location`
   * （实测：test 级只有 spec 标题，file/line 全无）。第一版取错位置，
   * 于是报告里每条失败的出处都成了 `detail @ mobile (375×667):0`。
   */
  location?: { file?: string; line?: number; column?: number }
}

interface PWResult {
  status: string
  duration: number
  retry: number
  error?: PWError
  errors?: PWError[]
}

interface PWTest {
  status: string
  /**
   * ⚠️ **可选**：真实产物的 `tests[]` 里**没有** title（标题在 `spec.title`）。
   * 写成必填会让"取错字段"这件事在类型层面被掩盖 —— 第一版就是这么错的。
   */
  title?: string
  projectName?: string
  location?: { file?: string; line?: number }
  results: PWResult[]
  /** `annotations` 里放的是 testInfo.annotations：我们用 `failure` 带结构化截图路径 */
  annotations?: { type: string; description?: string }[]
  expectedStatus: string
}

interface PWSpec {
  title: string
  ok: boolean
  tags?: string[]
  /** 有的版本/spec 级失败会带 location；缺了也不影响（测试用例级优先） */
  location?: { file?: string; line?: number }
  tests: PWTest[]
}

interface PWSuite {
  title: string
  file?: string
  suites?: PWSuite[]
  specs?: PWSpec[]
}

interface PWReport {
  stats: { expected: number; unexpected: number; flaky: number; skipped: number; duration: number }
  suites: PWSuite[]
  errors: PWError[]
}

// ─── 单条失败 ────────────────────────────────────────────────────────────

export interface Failure {
  /** 聚合键：**去掉场景/视口**后的失败签名。同一条断言在不同场景挂 = 同一个 key */
  key: string
  /** 展示名：`detail @ mobile (375×667)` */
  where: string
  project: string
  /** 断言点：`标题 → 第一条有信息量的错误行` */
  assertion: string
  /** 这一条失败的**唯一出处**（文件:行） */
  location: string
  attachments: { name: string; path?: string }[]
}

/**
 * 去掉 ANSI 颜色码。
 *
 * ⚠️ 这是必须的一步：Playwright 的 json reporter 把**带色码**的错误原文写了进去
 * （`Expected: <= \u001b[32m0\u001b[39m`）。不去掉的话，正则锚在行首的 `^Expected:`
 * 仍然能命中，但生成出来的结论文件会带一堆 `[32m` —— 贴进上下文又贵又难读。
 */
function stripAnsi(s: string): string {
  // eslint-disable-next-line no-control-regex
  return s.replace(/\u001b\[[0-9;]*m/g, '')
}

/** 从 Playwright 的错误文本里挑出**信息量最大的那一行**。
 *
 * 取值顺序是有讲究的（实测的失败文本长这样）：
 *
 *   Error: 滚动到最底后，动作行仍盖住文字区 41px：… scrollPaddingBottom=NaNpx —— 用户看不到被盖住的那 1~2 行
 *   Expected: <= 0
 *   Received: 41
 *   Call log:
 *     - expect.toBeLessThanOrEqual with timeout 5000ms
 *
 * 第一行是**我们手写的中文诊断**（带实测值），`Expected/Received` 两行只是把同一个
 * 事实拆成两半。所以顺序必须是：
 *   ① `Error:` 开头的诊断行 —— 优先；
 *   ② 中文/自定义消息行（含"期望/实测/超"等，且不含 `to be` 这类 Playwright 套话）；
 *   ③ `Expected/Received` 合并成一行（我们没写 message 时才会轮到它）；
 *   ④ 兜底第一行。
 */
function firstMeaningfulLine(err?: PWError): string {
  if (!err?.message) return '(无错误信息)'
  const lines = stripAnsi(err.message)
    .split('\n')
    // Playwright 的 message 后面跟着 `Call log:` / `Error Context:` / `  - waiting for ...` / 堆栈。
    .map((l) => l.trim())
    .filter(Boolean)
  if (!lines.length) return '(空错误信息)'

  // ① 我们手写的诊断（`Error: 中文...`，通常带实测数字）
  const custom = lines.find((l) => /^Error:\s*\S/.test(l) && /[\u4e00-\u9fa5]/.test(l))
  if (custom) return custom.replace(/^Error:\s*/, '').slice(0, 400)

  // ② 带实测/期望语义的中文行（没有 `Error:` 前缀时）
  const zh = lines.find((l) => /[\u4e00-\u9fa5]/.test(l) && /期望|实测|预期|盖住|拦截|超出|不在/.test(l))
  if (zh) return zh.slice(0, 400)

  // ③ 自动断言：把 Expected / Received 合成一行，比取其中任一行信息量大
  const exp = lines.find((l) => /^Expected:/.test(l))
  const rec = lines.find((l) => /^Received:/.test(l))
  if (exp || rec) {
    return `${exp ?? 'Expected: ?'} / ${rec ?? 'Received: ?'}`.slice(0, 400)
  }

  // ④ 兜底
  return lines[0].slice(0, 400)
}

/**
 * 失败签名。
 *
 * ⚠️ 只用**第一行**参与签名（`firstMeaningfulLine`），不要把整段错误都抹成 `<N>`：
 * 一条失败消息常带多行数字（"盖住 41px：可见文字高 66px，行高 22px"），
 * 若把所有带数字的行都拼进签名，**同一次失败会产生多个签名**，
 * 于是「已修好 / 仍红 / 新挂」三个集合全部失真（实测踩过，由单测钉住）。
 */
function signatureOf(project: string | undefined, title: string | undefined, errText: string | undefined): string {
  // ⚠️ 这三个参数**都可能缺失**：Playwright 的 JSON 在"运行级错误 / 超时 / 夹具崩溃"
  //    这类失败上不会给完整用例结构。它只有**红的时候**才发生，所以必须防御 ——
  //    实测：`title` 为空时这里 `undefined.replace` 崩掉，整份结论文件都没生成。
  //    表现比"没有 reporter"更糟：人会以为报告系统坏了，转头去看截图。
  const t = String(title ?? '(未命名用例)')
  // 场景/视口从标题里剥掉：同一条断言在 6 个视口挂，应当只有**一个**签名。
  const sceneOfTitle = t.replace(/\s*@\s*\w+\s*\(.*\)\s*$/, '').trim()
  const normalized = String(errText ?? '')
    .replace(/\d+(\.\d+)?px/g, '<N>px')
    .replace(/-?\d+(\.\d+)?/g, '<N>')
    .replace(/\s+/g, ' ')
    .trim()
    .slice(0, 200)
  return `${String(project ?? '(未知 project)')} | ${sceneOfTitle} | ${normalized}`
}

function walk(suites: PWSuite[] | undefined, out: PWSpec[], parent = ''): void {
  for (const s of suites ?? []) {
    const next = parent ? `${parent} › ${s.title}` : s.title
    if (s.suites) walk(s.suites, out, next)
    for (const spec of s.specs ?? []) out.push(spec)
  }
}

/** 把 JSON reporter 的结果收敛成失败清单（去重、排序、抹掉易变部分） */
export function collectFailures(report: PWReport): { failures: Failure[]; total: number; passed: number; unexpected: number } {
  const specs: PWSpec[] = []
  walk(report.suites, specs)

  const failures: Failure[] = []
  let total = 0
  let passed = 0

  for (const spec of specs) {
    for (const t of spec.tests) {
      total++
      // `status` 是这次运行的最终状态：expected = 过（含重试后过），unexpected = 失败
      if (t.status === 'expected') {
        passed++
        continue
      }
      if (t.status === 'skipped') continue
      const last = t.results[t.results.length - 1]
      const err = last?.errors?.[0] ?? last?.error
      const errText = firstMeaningfulLine(err)
      /**
       * ⚠️ 字段来源是**实测校准**过的，别按直觉改（第一版全取错了位置）：
       *
       *   · 标题：带视口的用例名在 **`spec.title`**（`detail @ mobile (375×667)`），
       *     而 `test.title` 在 json 里**根本不存在** —— 于是"出现 3 次：、、"。
       *   · 出处：在 **`results[].error.location`**，`test.location` 同样不存在 ——
       *     于是每条出处都成 `<spec.title>:0`。
       *   · 这两条只在**失败**时才会暴露（通过时不走这里），所以必须由单测钉住。
       */
      const title = spec.title ?? t.title ?? '(未命名用例)'
      const file = err?.location?.file ?? t.location?.file ?? spec.location?.file ?? spec.title ?? '(未知位置)'
      const line = err?.location?.line ?? t.location?.line ?? spec.location?.line ?? 0
      failures.push({
        key: signatureOf(t.projectName, title, errText),
        where: title,
        project: t.projectName ?? '?',
        assertion: errText,
        location: `${String(file).replace(/\\/g, '/')}:${line}`,
        attachments: [],
      })
    }
  }

  return { failures, total, passed, unexpected: report.stats.unexpected }
}

// ─── 跨运行对比 ──────────────────────────────────────────────────────────

export interface RunSnapshot {
  at: string
  keys: string[]
  /** key → 一行展示（重跑时用它显示"这条以前是什么"） */
  rows: Record<string, string>
  /**
   * 上一轮**刚修好**的失败（key → 行文字）。
   *
   * 为什么需要它：红→绿的第一次能靠 `compare` 说出"已修好"，
   * 但紧接着的**下一轮**（再跑一次确认没回归）就只剩"全绿"，`compare` 无从对比。
   * 把"刚修好"留在这里，就能说"这 N 条保持绿"——而这正是迭代收尾时唯一想确认的事。
   */
  lastFixed?: Record<string, string>
  fixedCount?: number
}

export function loadLastFailures(): RunSnapshot | null {
  if (!existsSync(LAST_FAILURES_PATH)) return null
  try {
    return JSON.parse(readFileSync(LAST_FAILURES_PATH, 'utf-8')) as RunSnapshot
  } catch {
    // 坏文件不该让测试报告也挂掉：当作"没有上一轮"
    return null
  }
}

export function saveLastFailures(snapshot: RunSnapshot): void {
  mkdirSync(STATE_DIR, { recursive: true })
  writeFileSync(LAST_FAILURES_PATH, JSON.stringify(snapshot, null, 2), 'utf-8')
}

export interface Comparison {
  fixed: string[]
  still: string[]
  added: string[]
}

export function compare(prev: RunSnapshot | null, now: RunSnapshot): Comparison {
  const before = new Set(prev?.keys ?? [])
  const after = new Set(now.keys)
  return {
    fixed: [...before].filter((k) => !after.has(k)),
    still: [...after].filter((k) => before.has(k)),
    added: [...after].filter((k) => !before.has(k)),
  }
}

// ─── 结论文本 ────────────────────────────────────────────────────────────

/** 把 key 还原成一行可读的字（`project | 断言 | 错误` 里的断言段） */
function humanizeRow(row: string): string {
  const parts = row.split(' | ')
  return parts.length >= 2 ? parts[1] : row
}

/**
 * 生成结论文本。**这就是"跑完测试后唯一要读的东西"**。
 *
 * 写法上的两条刻意取舍：
 *   · 先给**汇总一行**（过了多少 / 红了几条 / 和上次比怎样），因为多数时候看完这行就该收工；
 *   · 失败按签名聚合，给出**数量**而不是逐条铺开 —— 6 个场景同一条断言坏，是人一眼就懂的事，
 *     抄 6 遍只会把"真正新出现的那一条"淹掉。
 */
export function renderSummary(
  now: RunSnapshot,
  cmp: Comparison | null,
  failures: Failure[],
  totals: { total: number; passed: number; unexpected: number },
  /** 上一次运行的行内容（全绿时用它显示"已修好的那几条长什么样"） */
  prevRows?: Record<string, string>,
): string {
  const lines: string[] = []
  const failedKeys = now.keys.filter((k) => !cmp || !cmp.fixed.includes(k))
  const status = failures.length ? 'FAIL' : 'PASS'
  lines.push(
    `# e2e ${status}  ${totals.passed}/${totals.total} 通过 | ${new Set(failedKeys).size} 条失败签名 / ${failures.length} 个用例` +
      (cmp ? ` | 已修好 ${cmp.fixed.length} / 仍红 ${cmp.still.length} / 新挂 ${cmp.added.length}` : ''),
  )

  if (!failures.length) {
    lines.push('')
    lines.push('全部通过。**不需要看截图**，也不需要再跑一次。')
    // ★ 全绿时也要报"这一轮修好了什么"：迭代循环最想知道的就是这句
    //   （"刚才那次改动到底有没有用"）。第一版在这里直接 return，
    //   于是**修好之后恰恰看不到"已修好"**——最该出现的结论反而缺失。
    //
    // ⚠️ 已修好的 key 在 `now.rows` 里**查不到**（它这次已经不红了），
    //    所以要从 `prev.rows` 取那一行的文字。第一版写成 `now.rows[k]`，
    //    结果修好之后打印的是裸 key（`layout | xxx |`）。
    if (cmp?.fixed.length && prevRows) {
      lines.push('')
      lines.push(`## 与上一次运行相比：✅ 已修好 ${cmp.fixed.length} 条`)
      for (const k of cmp.fixed) lines.push(`- ${humanizeRow(prevRows[k] ?? k)}`)
    }
    // 绿→再绿：上一轮修好的那几条**保持绿**（跨过"绿"的那一轮也说得出来）
    if (!cmp?.fixed.length && now.lastFixed && Object.keys(now.lastFixed).length) {
      lines.push('')
      lines.push(`## 保持绿：上一轮修好的 ${Object.keys(now.lastFixed).length} 条仍然全绿 ✅`)
      for (const row of Object.values(now.lastFixed)) lines.push(`- ${humanizeRow(row)}`)
    }
    return lines.join('\n') + '\n'
  }

  // ① 本轮的失败，按签名聚合
  const byKey = new Map<string, Failure[]>()
  for (const f of failures) {
    const list = byKey.get(f.key) ?? []
    list.push(f)
    byKey.set(f.key, list)
  }

  const isNew = (k: string) => !!cmp && cmp.added.includes(k)
  const ordered = [...byKey.entries()].sort((a, b) => Number(isNew(b[0])) - Number(isNew(a[0])))

  lines.push('')
  lines.push('## 失败（按签名聚合）')
  for (const [key, list] of ordered) {
    const tag = isNew(key) ? '**[新挂]** ' : ''
    const where = list.map((f) => f.where).join('、')
    const locs = [...new Set(list.map((f) => f.location))]
    lines.push(`- ${tag}${list[0].assertion}`)
    lines.push(`  - 出现 ${list.length} 次：${where.slice(0, 300)}${where.length > 300 ? ' …' : ''}`)
    lines.push(`  - 出处 ${locs.slice(0, 3).join(' ')}${locs.length > 3 ? ` …(+${locs.length - 3})` : ''}`)
  }

  // ② 与上次的差分：这才是"改动有没有接近目标"的答案
  if (cmp) {
    lines.push('')
    lines.push('## 与上一次运行相比')
    if (cmp.fixed.length) {
      lines.push(`- ✅ 已修好（上次红、这次绿）：${cmp.fixed.map(keyToRow(now)).join('；')}`)
    }
    if (cmp.still.length) {
      lines.push(`- ⏳ 仍红（${cmp.still.length}）：${cmp.still.slice(0, 6).map(keyToRow(now)).join('；')}`)
    }
    if (cmp.added.length) {
      lines.push(`- ❗ 新挂（${cmp.added.length}）：${cmp.added.slice(0, 6).map(keyToRow(now)).join('；')}`)
    }
    if (!cmp.fixed.length && !cmp.added.length && cmp.still.length) {
      lines.push('- ⚠️ 本轮结果与上一轮**完全一样**：再改一版之前，先确认你改的那处真的被这些断言覆盖到。')
    }
  }

  // ③ 什么时候才该看图
  lines.push('')
  lines.push('## 什么时候才需要截图')
  lines.push(
    '上面每一条都已经是「实测 / 期望」的数字结论。只有在**数字看着对、但画面确实不对**时' +
      '（纯视觉问题，例如颜色/圆角/图标错位），才去看 `test-results/**/test-failed-1.png`，' +
      '并且**只看一张**。',
  )
  return lines.join('\n') + '\n'
}

function keyToRow(now: RunSnapshot) {
  return (k: string) => humanizeRow(now.rows[k] ?? k)
}

/** 供 reporter 使用：把一条 failure 变成快照里的一行 */
export function rowOf(f: Failure): string {
  return `${f.project} | ${f.assertion} | ${f.location}`
}
