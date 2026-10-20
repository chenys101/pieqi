#!/usr/bin/env node
// 「先诊断环境，再跑验证」：一条命令跑完 health，绿了再跑被测的三层。
//
// 为什么要有它：2026-10-19 的真实事故里，最贵的一段消耗是**把环境故障当产品缺陷**——
// vite/mock 没起来、浏览器 spawn 被拒，表现都是"某个用例红了"，于是去看截图、去改源码。
// 判据：
//   · health 红 ⇒ **立刻停**，一句话说清是环境问题、不进入改代码循环；
//   · health 绿 ⇒ 再跑 layout/visual/interaction，结论看 SUMMARY.md。
//
// 用法：
//   npm run test:e2e:safe                        # 全部三层
//   npm run test:e2e:safe -- --project=layout    # 只跑某一层（参数原样透传）

import { spawnSync } from 'node:child_process'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const WEB_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const win = process.platform === 'win32'

function run(args) {
  // 用 shell 启动 npx：Windows 上 npx 是 .cmd，spawn 不带 shell 会 ENOENT
  return spawnSync('npx', ['playwright', 'test', ...args], {
    cwd: WEB_ROOT,
    stdio: 'inherit',
    shell: win,
  })
}

console.log('[safe] ① 环境分诊（--project=health）…')
const health = run(['--project=health'])
if (health.status !== 0) {
  console.error(
    '\n[safe] ✋ 环境分诊没过。**不要去改源码，也不要去看截图。**\n' +
      '     上面的报错已经说明是哪一环坏了（夹具后端 / dev server / 浏览器启动）。\n' +
      '     修好环境后重跑这一条命令即可 —— 环境红的时候，任何 layout/visual 的结论都不可信。',
  )
  process.exit(health.status ?? 1)
}

const passthrough = process.argv.slice(2)
const targets = passthrough.length
  ? passthrough
  : ['--project=layout', '--project=visual', '--project=interaction']

console.log(`\n[safe] ② 环境正常，跑验证：${targets.join(' ')}`)
const r = run(targets)

const summary = join(process.env.E2E_OUTPUT_DIR || join(tmpdir(), 'pieqi-e2e'), 'SUMMARY.md')
console.log(`\n[safe] 结论已写到 ${summary}（下一步只看它）。`)
process.exit(r.status ?? 1)
