#!/usr/bin/env node
// 视觉旁路（L3）：把截图交给**独立的视觉模型**，只把**一句结论**带回来。
//
// 解决的问题：截图一旦进主模型上下文，就是几千 token，且会随会话轮次反复计费。
// 旁路的做法是"图出主上下文"——图在本脚本里被消费，主上下文里只出现它的结论。
//
// 判据：**只在 `toHaveScreenshot` 失败、且需要人判断"这是设计变更还是 bug"时**才用。
// 平时的"变了没有"由机器 diff 判，不需要任何模型。
//
// 前提（刻意不自动安装）：
//   · 环境变量 VISION_API_KEY / VISION_BASE_URL / VISION_MODEL（OpenAI 兼容 /chat/completions）
//   · 未配置时**显式退出码 2 并打印原因** —— 不静默跳过（静默跳过 = 以为验过了）
//
// 用法：
//   node e2e/vision.mjs path/to/actual.png "检查布局：是否有元素重叠/被裁切/错位"
//   node e2e/vision.mjs --batch e2e/.artifacts   # 目录里所有 *-actual.png

import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, resolve, extname } from 'node:path'

const apiKey = process.env.VISION_API_KEY
const baseUrl = (process.env.VISION_BASE_URL || 'https://api.openai.com/v1').replace(/\/+$/, '')
const model = process.env.VISION_MODEL || 'gpt-4o-mini'

if (!apiKey) {
  console.error(
    '[vision] 未配置 VISION_API_KEY —— 视觉旁路不可用。\n' +
      '  请设置 VISION_API_KEY / VISION_BASE_URL / VISION_MODEL（OpenAI 兼容的 chat/completions）。\n' +
      '  刻意不自动跳过：跳过会让你以为图已经看过了。',
  )
  process.exit(2)
}

const args = process.argv.slice(2)
if (!args.length) {
  console.error('用法: node e2e/vision.mjs <图片路径> ["关注点"]   |   node e2e/vision.mjs --batch <目录>')
  process.exit(2)
}

/** 收集要分析的图 */
function collect() {
  if (args[0] === '--batch') {
    const dir = resolve(args[1] || 'e2e/.artifacts')
    const out = []
    const walk = (d) => {
      for (const e of readdirSync(d)) {
        const p = join(d, e)
        const s = statSync(p)
        if (s.isDirectory()) walk(p)
        // 只看失败产物：Playwright 的 actual 图命名以 -actual.png 结尾
        else if (/-actual\.png$/i.test(e)) out.push(p)
      }
    }
    walk(dir)
    return out.map((p) => ({ file: p, ask: DEFAULT_ASK }))
  }
  return [{ file: resolve(args[0]), ask: args[1] || DEFAULT_ASK }]
}

/**
 * 默认关注点：**给的是故障清单**，不是"描述这张图"。
 * 让模型描述图 = 把图变成长篇文字，token 又回来了；给清单 = 它只需要回答"有没有"。
 */
const DEFAULT_ASK =
  '这是 Web UI 的截图。只回答下面的问题，每项一行，用「是/否 + 位置 + 像素估计」回答，不要描述图片内容：\n' +
  '1. 是否有元素相互重叠或被遮挡？\n' +
  '2. 是否有文字被裁切、溢出容器或出现横向滚动条？\n' +
  '3. 是否有明显对齐错位（同一行元素基线不齐 / 间距不一致）？\n' +
  '4. 首屏主行动按钮是否完整可见？\n' +
  '5. 是否有整块空白（数据没渲染出来）？\n' +
  '最后一行输出：结论 OK 或 结论 PROBLEM(简述)。'

const MIME = { '.png': 'image/png', '.jpg': 'image/jpeg', '.jpeg': 'image/jpeg', '.webp': 'image/webp' }

async function ask(file, question) {
  const ext = extname(file).toLowerCase()
  const mime = MIME[ext]
  if (!mime) throw new Error(`不支持的图片格式: ${file}`)
  const b64 = readFileSync(file).toString('base64')

  const res = await fetch(`${baseUrl}/chat/completions`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${apiKey}` },
    body: JSON.stringify({
      model,
      // 低温：这是判读任务，不要发挥
      temperature: 0,
      max_tokens: 400,
      messages: [
        {
          role: 'user',
          content: [
            { type: 'text', text: question },
            { type: 'image_url', image_url: { url: `data:${mime};base64,${b64}`, detail: 'high' } },
          ],
        },
      ],
    }),
  })

  if (!res.ok) {
    const body = await res.text()
    throw new Error(`视觉模型调用失败 ${res.status}: ${body.slice(0, 300)}`)
  }
  const json = await res.json()
  const text = json?.choices?.[0]?.message?.content
  if (!text) throw new Error(`视觉模型返回为空: ${JSON.stringify(json).slice(0, 300)}`)
  return String(text).trim()
}

const items = collect()
if (!items.length) {
  console.error('[vision] 没有找到 *-actual.png（说明没有视觉用例失败，或还没跑过）')
  process.exit(0)
}

let bad = 0
for (const it of items) {
  try {
    const answer = await ask(it.file, it.ask)
    // ★ 只把结论打出来（这是要进主上下文的那一句），图本身留在本地
    console.log(`\n[vision] ${it.file}\n${answer}`)
    if (/PROBLEM/i.test(answer)) bad++
  } catch (e) {
    console.error(`[vision] ${it.file}: ${e.message}`)
    bad++
  }
}

console.log(`\n[vision] ${items.length} 张，其中有问题的 ${bad} 张`)
process.exit(bad ? 1 : 0)
