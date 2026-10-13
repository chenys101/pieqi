// 日期/时间工具

/** 相对时间：x 秒/分/小时/天前（用于任务列表紧凑展示） */
export function timeAgo(iso?: string): string {
  if (!iso) return ''
  const t = new Date(iso).getTime()
  if (Number.isNaN(t)) return ''
  const s = Math.floor((Date.now() - t) / 1000)
  if (s < 0) return '刚刚'
  if (s < 60) return `${s}s`
  if (s < 3600) return `${Math.floor(s / 60)}m`
  if (s < 86400) return `${Math.floor(s / 3600)}h`
  return `${Math.floor(s / 86400)}d`
}

/** 运行时长：从 startedAt 到现在（或 finishedAt） */
export function duration(startedAt?: string, endedAt?: string): string {
  if (!startedAt) return ''
  const start = new Date(startedAt).getTime()
  const end = endedAt ? new Date(endedAt).getTime() : Date.now()
  if (Number.isNaN(start) || end <= start) return ''
  const s = Math.floor((end - start) / 1000)
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ${s % 60}s`
  const h = Math.floor(m / 60)
  return `${h}h ${m % 60}m`
}

/** 本地化时间（Settings / 详情页用） */
export function formatDateTime(iso?: string): string {
  if (!iso) return ''
  const t = new Date(iso)
  if (Number.isNaN(t.getTime())) return ''
  return t.toLocaleString()
}

/**
 * 时:分（24 小时制，零填充）—— 时间线上每轮"提示词 / 结论"的时间标记。
 *
 * 为什么固定 24 小时制而不是 toLocaleTimeString()：后者按浏览器区域设置输出，
 * 同一份数据在不同机器上会显示成 "4:08 PM" 或 "16:08"，而这是**日志性质**的
 * 信息、要和后端时间/其他人截图对得上。固定格式 = 可比。
 *
 * 为什么不显示日期：同一会话内的轮次几乎都在同一天，日期会挤占本就很窄的
 * 移动端头部；跨天的情况由任务详情头部的时间兜底。
 */
export function formatClock(iso?: string): string {
  if (!iso) return ''
  const t = new Date(iso)
  if (Number.isNaN(t.getTime())) return ''
  const hh = String(t.getHours()).padStart(2, '0')
  const mm = String(t.getMinutes()).padStart(2, '0')
  return `${hh}:${mm}`
}

/** 取时间戳（排序用；解析失败为 0） */
export function timestamp(iso?: string): number {
  if (!iso) return 0
  const v = new Date(iso).getTime()
  return Number.isNaN(v) ? 0 : v
}
