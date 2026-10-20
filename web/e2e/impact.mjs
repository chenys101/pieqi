#!/usr/bin/env node
// 影响面定向回归：把 `git diff` 的改动路径映射成"该跑哪些用例"。
//
// 这一条是"重复"的解法：重复的根源不是截图本身，而是**每次改动都想全量验一遍**。
// 改了 approval 的一个按钮，没必要重跑会话时间线的三视口视觉回归。
//
// 立下的规矩（判错的方向要偏保守）：
//   · 无法归类 ⇒ **跑全量**。宁可多跑 30 秒，也不要"改到了却没人测"。
//   · 组件库（components/ui）、全局样式（styles/）⇒ 跑全量（影响面 = 所有页面）。
//   · 只改 .md / 测试本身 ⇒ 不跑（省时间且不会漏）。
//
// 用法：
//   node e2e/impact.mjs              # 相对 HEAD 的工作区改动
//   node e2e/impact.mjs --base=main  # 相对某个 ref
//   node e2e/impact.mjs --print      # 只看会跑什么，不执行
//
// 输出的是**可直接执行的命令**，不自己 spawn playwright ——
// 这样 CI / 本地 / 人手动都能复用同一决定，而且失败能原样重跑。

import { execSync, spawnSync } from 'node:child_process'
import { tmpdir } from 'node:os'
import { fileURLToPath } from 'node:url'
import { dirname, join, resolve } from 'node:path'

const HERE = dirname(fileURLToPath(import.meta.url))
const WEB_ROOT = resolve(HERE, '..')

const args = process.argv.slice(2)
const printOnly = args.includes('--print')
const baseArg = args.find((a) => a.startsWith('--base='))
const base = baseArg ? baseArg.slice('--base='.length) : 'HEAD'

/** 读改动路径（工作区 + 暂存 + 相对 base 的提交） */
function changedFiles() {
  const out = new Set()
  const run = (cmd) => {
    try {
      return execSync(cmd, { cwd: WEB_ROOT, encoding: 'utf-8', stdio: ['ignore', 'pipe', 'ignore'] })
        .split('\n')
        .map((l) => l.trim())
        .filter(Boolean)
    } catch {
      // 非 git 环境（如 tar 包交付）⇒ 返回空，调用方按"未知 ⇒ 全量"处理
      return []
    }
  }
  // 工作区（含未暂存）
  for (const f of run('git diff --name-only')) out.add(f)
  // 暂存区
  for (const f of run('git diff --cached --name-only')) out.add(f)
  // 相对 base（含已提交但未合入的改动）
  for (const f of run(`git diff --name-only ${base}...HEAD`)) out.add(f)
  return [...out]
}

/**
 * 三层全跑。作为常量而不是每处手写：新增 project 时只改这一行，
 * 不会出现"某几条规则漏了三层里的某一层"（那种漏检是静默的）。
 */
const ALL = ['--project=layout', '--project=visual', '--project=interaction']

/**
 * 映射表：**路径前缀 → 要跑的 project/test 过滤表达式**。
 *
 * 顺序有意义：先匹配到的赢。所以"范围大的"放后面（把全量放在兜底位）。
 */
const RULES = [
  // 仓库元文件：不影响运行产物，不跑
  { match: [/^\.gitignore$/, /^CONTEXT\.md$/, /^AGENTS\.md$/, /^README\.md$/, /^\.editorconfig$/], targets: [], reason: '仓库元文件' },

  // 纯文档/说明：不跑任何用例
  { match: [/\.md$/, /^web\/e2e\/docs\//, /^docs\//], targets: [], reason: '仅文档改动' },

  // 构建/样式配置：Tailwind 内容扫描、vite 代理与入口切换都在这里，影响所有页面。
  // package.json 也归这里：依赖版本变化（Tailwind / Playwright / Vue 升级）会改变渲染结果，
  // 不跑视觉层就等于"升级了但没人看"。
  // 三层全跑：交互层依赖夹具与路由，配置/夹具变了它也必须跟着验。
  {
    match: [/^tailwind\.config\./, /^postcss\.config\./, /^vite\.config\./, /^playwright\.config\./, /^tsconfig/, /^package\.json$/, /^package-lock\.json$/],
    targets: ALL,
    reason: '构建/依赖/测试配置改动',
  },

  // 测试自身与夹具：三层共用同一份夹具数据，夹具改了三层都要重跑。
  { match: [/^e2e\/fixtures\//, /^e2e\/mock-api\.mjs$/], targets: ALL, reason: '夹具/后端改动影响三层' },
  { match: [/^e2e\//], targets: ALL, reason: 'e2e 支撑代码改动' },

  // 样式与设计 token：影响所有页面；视觉基线必跑（布局断言可以顺带跑）
  { match: [/^src\/styles\//, /^tailwind\.config\./, /^src\/components\/ui\//], targets: ALL, reason: '全局样式/组件库改动，影响面=所有页面' },

  // 按 feature 定向（交互层也带 -g：审批卡的点击路径与时间线的展开都在 interaction 里）
  {
    match: [/^src\/features\/approval\//],
    targets: ['--project=layout -g approval', '--project=visual -g approval', '--project=interaction -g 审批'],
    reason: '审批卡',
  },
  {
    match: [/^src\/features\/session\//, /^src\/features\/timeline\//],
    targets: ['--project=layout -g detail', '--project=visual -g detail', '--project=interaction'],
    reason: '会话时间线',
  },
  { match: [/^src\/features\/feedback\//], targets: ['--project=layout -g detail', '--project=visual -g detail'], reason: '变更反馈右栏（挂在会话详情里）' },

  // 适配层/类型/store：影响所有页面渲染，保守全量
  { match: [/^src\/services\//, /^src\/stores\//, /^src\/types\//, /^src\/app\//, /^src\/main\.ts$/], targets: ALL, reason: '数据层/路由/入口改动，影响所有页面' },

  // 未知目录：保守全量（见文件末尾的 ALL）
  { match: [/./], targets: ALL, reason: '未归类的改动，保守跑全量' },
]

function decide(files) {
  if (!files.length) {
    // 没有 git 信息（或确实没改动）：**不要**静默跳过一个证 —— 交给调用方决定
    return { targets: ['--project=layout'], reason: '未检测到改动（或非 git 环境）：只跑便宜的数值层', files }
  }
  const hits = []
  for (const f of files) {
    const rule = RULES.find((r) => r.match.some((m) => m.test(f)))
    if (rule) hits.push({ file: f, ...rule })
  }
  // 合并去重（保持顺序）
  const targets = [...new Set(hits.flatMap((h) => h.targets))]
  const reason = [...new Set(hits.map((h) => h.reason))].join('；')
  return { targets: targets.length ? targets : ['--project=layout'], reason, files }
}

const files = changedFiles()
const d = decide(files)

const cmd = ['npx playwright test', ...d.targets].join(' ')

console.log(`[impact] base=${base}  改动 ${files.length} 个文件`)
for (const f of files.slice(0, 20)) console.log(`  · ${f}`)
if (files.length > 20) console.log(`  … 其余 ${files.length - 20} 个`)
console.log(`[impact] 判据：${d.reason}`)
console.log(`[impact] 执行：${cmd}`)

/**
 * 从选中的 targets 里抽出"最近的那条 -g 表达式"与它所属的 project。
 * 形如 `--project=layout -g detail` → { project: '--project=layout', grep: 'detail' }。
 */
function locateGrep(targets) {
  for (const t of targets) {
    const m = t.match(/^(--project=\S+)\s+-g\s+(.+)$/)
    if (m) return { project: m[1], grep: m[2].trim() }
  }
  return null
}

if (printOnly) process.exit(0)

// 用 inherit：把子进程输出直接交给终端，失败码原样透传（不要在捕获里抹掉）
const [bin, ...rest] = cmd.split(' ')
const r = spawnSync(bin, rest, { cwd: WEB_ROOT, stdio: 'inherit', shell: process.platform === 'win32' })

/**
 * 跑完**立刻**把"回到全绿"的那条命令打出来（2026-10-19 复盘新增）。
 *
 * 那次修一个输入框遮挡，真正烧掉时间的不是找一个红，而是**每改一点就把同一组用例
 * 再整跑一遍**（`fixture.spec.ts` 被改了 18 次、`measure.spec.ts` 16 次，而每次重跑
 * 都要重新读一遍输出）。收敛循环应该是：
 *   ① 只跑**刚红的那几条**（`-g` 精确到用例名，几秒）；
 *   ② 绿了以后才跑**一次**完整的影响面集合；
 *   ③ 结论读 SUMMARY.md（含"已修好/仍红/新挂"），不读逐条输出。
 *
 * 这里把 ① 的命令直接打印出来，省掉"自己去拼 -g 表达式"——拼错的那次会白跑一轮。
 */
const g = locateGrep(d.targets)
if (g) {
  // 把表达式里的正则元字符转义：它是"按名字找用例"，不是拿它当正则用
  const literal = g.grep.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  console.log(
    `\n[impact] 失败时的定位命令（只跑匹配的那几条，**别**重跑全量）：\n` +
      `    npx playwright test ${g.project} -g "${literal}"`,
  )
}
const summaryFile = join(
  process.env.E2E_OUTPUT_DIR || join(process.env.TEMP || tmpdir(), 'pieqi-e2e'),
  'SUMMARY.md',
)
console.log(`[impact] 结论只看这里：${summaryFile}`)
process.exit(r.status ?? 1)
