// 格式化工具：标题截断 / 路径归一 / 状态文案

import type { TaskStatus } from '@/types/task'

/**
 * 标题智能截断：优先取首个断句短句；否则在词边界截断；
 * 中文长句无空格则硬切。LLM 生成的 title 存在时不走此函数。
 */
export function truncateTitle(s: string | null | undefined, max = 15): string {
  const str = String(s ?? '')
    .replace(/\s+/g, ' ')
    .trim()
  if (str.length <= max) return str
  // 1) 首个断句（。！？.!?；;）且长度可控 → 作为短标题
  const m = str.match(/^[^。！？.!?；;]*[。！？.!?；;]/)
  if (m && m[0].trim().length <= max + 6) return m[0].trim() + '…'
  // 2) max 内最近的空格边界；无空格则硬切
  const sp = str.lastIndexOf(' ', max)
  return str.slice(0, sp >= 1 ? sp : max) + '…'
}

/**
 * 项目分组 key：统一斜杠 / 去重复斜杠 / 去尾斜杠 / 小写
 * （Windows 路径大小写不敏感，历史数据混用 / 与 \）。
 */
export function groupKey(p: string | undefined): string {
  return String(p ?? '')
    .replace(/\\/g, '/')
    .replace(/\/{2,}/g, '/')
    .replace(/\/+$/, '')
    .toLowerCase()
}

/** 状态中文文案 */
export const STATUS_LABELS: Record<TaskStatus, string> = {
  pending: '待运行',
  running: '运行中',
  waiting_input: '需决策',
  completed: '已完成',
  failed: '失败',
  cancelled: '已取消',
}

/** 状态 → 语义色（方案 §29） */
export const STATUS_TONES: Record<TaskStatus, 'success' | 'warning' | 'error' | 'info' | 'neutral'> = {
  pending: 'info',
  running: 'success',
  waiting_input: 'warning',
  completed: 'neutral',
  failed: 'error',
  cancelled: 'neutral',
}

/** 短 id：#前 8 位 */
export function shortId(id: string): string {
  return (id ?? '').slice(0, 8)
}

/** 工具 input 健壮格式化：JSON 字符串/对象 → key: value 行 */
export function formatToolInput(input: unknown): { key: string; value: string }[] {
  if (!input) return []
  let obj: unknown = input
  if (typeof input === 'string') {
    try {
      obj = JSON.parse(input)
    } catch {
      return [{ key: '', value: input }]
    }
  }
  if (typeof obj !== 'object' || obj === null) {
    return [{ key: '', value: String(obj) }]
  }
  return Object.entries(obj as Record<string, unknown>).map(([k, v]) => {
    // value 截断到 200 字符，避免超长参数刷屏
    let val = typeof v === 'string' ? v : JSON.stringify(v)
    if (val.length > 200) val = val.slice(0, 200) + '...'
    return { key: k, value: val }
  })
}
