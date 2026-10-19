#!/usr/bin/env node
// `npm run test:e2e:online` 的实现：线上复量（发版动作，不是日常动作）。
//
// 与其余 e2e 脚本的差别，也是它需要单独一个文件的原因：
//
// ① **必须显式给 ONLINE_BASE_URL**。默认值是 127.0.0.1:3000（本机），但这里
//    **刻意要求显式设置** —— 理由是"线上复量"这个动作指向哪个实例，本身就是
//    决策的一部分。悄悄用一个默认值，会让"我以为测的是线上"变成一件没人复核的事。
//
// ② ONLINE_ONLY=1 跳过夹具层的 webServer（mock 后端 + dev server）。
//    判据：跑线上用例**不该需要**夹具服务，而不是"少等几秒"。
//
// ③ 退出码直接透传：线上复量失败必须是响亮的（发版脚本据此决定是否告警）。
//
// 用法：
//   npm run test:e2e:online -- --url=http://192.168.1.10:3000
//   ONLINE_BASE_URL=http://127.0.0.1:3000 npm run test:e2e:online

import { spawn } from 'node:child_process'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const WEB_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..')

const args = process.argv.slice(2)
const urlArg = args.find((a) => a.startsWith('--url='))
const baseURL = urlArg ? urlArg.slice('--url='.length) : process.env.ONLINE_BASE_URL

if (!baseURL) {
  console.error(
    '[online] 必须显式指定被测实例：\n' +
      '    npm run test:e2e:online -- --url=http://127.0.0.1:3000\n' +
      '    或  $env:ONLINE_BASE_URL="http://127.0.0.1:3000"; npm run test:e2e:online\n\n' +
      '  刻意不给默认值：线上复量指向哪个实例是**决策**，不该被默认值悄悄决定。',
  )
  process.exit(2)
}

// 只读提醒：本层用 route 拦截所有非 GET，但**前提是不去点敏感按钮**。
// 这里再打一行，是因为跑的人往往是刚部署完、注意力在别处。
console.log(`[online] 目标实例：${baseURL}（只读；所有非 GET 请求会被拦截）`)

const win = process.platform === 'win32'
const cmd = win ? 'cmd.exe' : 'npx'
const spawnArgs = win
  ? ['/d', '/s', '/c', 'npx', 'playwright', 'test', '--project=online', ...args.filter((a) => !a.startsWith('--url='))]
  : ['playwright', 'test', '--project=online', ...args.filter((a) => !a.startsWith('--url='))]

const child = spawn(cmd, spawnArgs, {
  cwd: WEB_ROOT,
  stdio: 'inherit',
  env: { ...process.env, ONLINE_BASE_URL: baseURL, ONLINE_ONLY: '1' },
})
child.on('exit', (code) => process.exit(code ?? 1))
child.on('error', (e) => {
  console.error(`[online] 启动失败: ${e.message}`)
  process.exit(1)
})
