// 数值基线：仓库内提交的 JSON，记录**上一次验收通过的**数字。
//
// 为什么基线要进 git：没有基线时，"DOM 4,218 个节点"是一个孤立数字 ——
// 既不能判断好坏，也不能判断有没有回退。而放在仓库外（~/.pieqi、临时目录）的基线
// 会随机器消失，于是"和基线比"退化成"和上次的印象比"。
//
// 更新基线的唯一方式：**人确认这些数字确实是新的期望值**，然后跑
//   `npm run test:e2e:update-baseline`
// 把本次实测写回文件并提交。**不要在断言失败时随手 closeTo 放宽阈值** ——
// 那等于把回退固化成期望值。

import { readFileSync, writeFileSync, mkdirSync, existsSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import type { Metrics, ViewportName } from './measure'

const HERE = dirname(fileURLToPath(import.meta.url))
export const BASELINE_PATH = resolve(HERE, '../baseline/layout-baseline.json')

/** 一条基线条目：只保留会被断言的数字，其余不落盘（否则每次跑都产生 diff 噪音） */
export interface BaselineEntry {
  domNodes: number
  lcpMs: number | null
  cls: number
  hOverflow: number
  /** 关键元素尺寸（断言"不变"用）；不存在的元素记为 null */
  boxes: Record<string, { w: number; h: number } | null>
}

export type Baseline = Record<string, BaselineEntry>

export function loadBaseline(): Baseline | null {
  if (!existsSync(BASELINE_PATH)) return null
  return JSON.parse(readFileSync(BASELINE_PATH, 'utf-8')) as Baseline
}

export function toEntry(m: Metrics): BaselineEntry {
  const boxes: BaselineEntry['boxes'] = {}
  for (const [k, b] of Object.entries(m.boxes)) boxes[k] = b ? { w: b.w, h: b.h } : null
  return { domNodes: m.domNodes, lcpMs: m.lcpMs, cls: m.cls, hOverflow: m.hOverflow, boxes }
}

export function keyOf(scene: string, vp: ViewportName): string {
  return `${scene}@${vp}`
}

/**
 * 回写单个场景的基线（按 key 合并，而不是整文件覆盖）。
 *
 * 按 key 合并是必要的：`E2E_UPDATE_BASELINE=detail` 只跑了一个 project，
 * 整文件覆盖会把另一个 scene（approval）的基线抹掉 —— 下次它就变成"无基线"而静默跳过。
 */
export function writeBaseline(entries: Record<string, BaselineEntry>): void {
  const cur = loadBaseline() ?? {}
  const next = { ...cur, ...entries }
  // 键排序，保证 diff 稳定（对象键顺序变化也会产生整文件 diff）
  const sorted = Object.fromEntries(Object.keys(next).sort().map((k) => [k, next[k]]))
  mkdirSync(dirname(BASELINE_PATH), { recursive: true })
  writeFileSync(BASELINE_PATH, JSON.stringify(sorted, null, 2) + '\n', 'utf-8')
}
