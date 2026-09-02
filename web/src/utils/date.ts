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

/** 取时间戳（排序用；解析失败为 0） */
export function timestamp(iso?: string): number {
  if (!iso) return 0
  const v = new Date(iso).getTime()
  return Number.isNaN(v) ? 0 : v
}
