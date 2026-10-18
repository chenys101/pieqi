#!/usr/bin/env node
// `npm run test:e2e:update-baseline` 的实现。
//
// 为什么不用 `cross-env` 或 `E2E_UPDATE_BASELINE=1 playwright ...`：
//   · 后者在 Windows 的 cmd/PowerShell 下不生效（npm scripts 在 Windows 走 cmd，不认 `VAR=x cmd`），
//     于是"以为更新了基线、其实跑的是比对"——最糟的失败模式：一次回退被当成基线差异反复刷红，
//     或者反过来，基线悄悄写进了回退后的数字；
//   · 引入 cross-env 只为一行环境变量，多一个依赖不值。
// 在 Node 里设 process.env 再 spawn，两个平台行为一致。
//
// ⚠️ 这一步是**人确认过数字之后**才做的事。断言失败时不要跑它来"消红"。

import { spawn } from 'node:child_process'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const WEB_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..')

/**
 * ⚠️ Windows 上**不能** `spawn(shell: 'npx.cmd ...')`：
 * npm 的 bin 是个 .cmd 垫片，走 shell 时环境变量里的 `$xxx` 会被 cmd 当变量展开成空串。
 * 这个坑一旦踩到，表现是"命令跑完了、没报错、基线文件却没更新"——
 * 正是本项目反复强调的**静默失败**。所以统一用 `npx`/`npx.cmd` 作为**直接子进程**，
 * 不经 shell，环境变量原样透传。
 */
const win = process.platform === 'win32'
const cmd = win ? 'cmd.exe' : 'npx'
const args = win
  ? ['/d', '/s', '/c', 'npx', 'playwright', 'test', '--project=layout']
  : ['playwright', 'test', '--project=layout']

const child = spawn(cmd, args, {
  cwd: WEB_ROOT,
  stdio: 'inherit',
  env: { ...process.env, E2E_UPDATE_BASELINE: '1' },
})
child.on('exit', (code) => process.exit(code ?? 1))
child.on('error', (e) => {
  console.error(`[update-baseline] 启动失败: ${e.message}`)
  process.exit(1)
})
