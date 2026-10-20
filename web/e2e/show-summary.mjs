#!/usr/bin/env node
// 打印上一次 e2e 运行的**结论**（SUMMARY.md），不需要知道它落在哪。
//
// 为什么要有这个脚本：结论文件在系统临时目录里（一次性产物不进仓库），
// 人记不住路径，于是每次都要去拼 `$env:TEMP\pieqi-e2e\SUMMARY.md` ——
// 拼错一次（比如忘了 TEMP 与 E2E_OUTPUT_DIR 的两种情况）就白读一轮。
//
// 用法：
//   npm run test:e2e:summary
//   npm run test:e2e:summary -- --last    # 同上（保留的别名，语义一样）
//
// 没有跑过任何 e2e 时，给出一句明确的"先跑一次"，而不是报 ENOENT。

import { existsSync, readFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const dir = process.env.E2E_OUTPUT_DIR || join(tmpdir(), 'pieqi-e2e')
const file = join(dir, 'SUMMARY.md')

if (!existsSync(file)) {
  console.log(
    `[summary] 还没有结论文件：${file}\n` +
      '先跑一次（例如 `npm run test:e2e:layout`），跑完的结论会写在这里。\n' +
      '⚠️ 环境相关的红先跑 `npx playwright test --project=health` 分诊，不要直接改代码。',
  )
  process.exit(0)
}

process.stdout.write(readFileSync(file, 'utf-8'))
