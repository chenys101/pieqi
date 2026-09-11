// Settings API：全局偏好（跨任务 / 跨项目 / 跨 agent）。
//
// 只有两项"属于全局"的自动化开关与一项数据上限：
//   ① 审批自动放行：**只有 L0 / L1 可配**，L2 / L3 在后端根本不接受字段
//      （能把自己配进坑里的选项，不要做成选项）。
//   ④ 事件保留上限。
// 免打扰时段是审批自动放行的另一面（它管"要不要打断你"），故同属 ①。

import { request, requestBlob } from './client'

export interface AppSettings {
  /** L0 只读探测自动放行 */
  auto_approve_l0: boolean
  /** L1 写入自动放行 */
  auto_approve_l1: boolean
  dnd_enabled: boolean
  /** "HH:MM" */
  dnd_start: string
  /** "HH:MM" */
  dnd_end: string
  /** 事件保留上限；0 = 全部保留 */
  event_retention: number
}

export async function getSettings(): Promise<AppSettings> {
  return request<AppSettings>('/settings', { quiet401: true })
}

/** 补丁语义：只传要改的字段（服务端同样按"未传 = 不改"处理） */
export async function patchSettings(patch: Partial<AppSettings>): Promise<AppSettings> {
  return request<AppSettings>('/settings', { method: 'PATCH', body: patch })
}

/**
 * 导出诊断日志（最近 7 天，zip）。
 *
 * 走 Blob 而不是 `<a href>`：导出接口要带鉴权头，而浏览器原生下载
 * 不会携带它 —— 直接用链接只会拿到 401 却看起来像"下载成功"。
 * 返回实际文件名（由服务端 Content-Disposition 给出）。
 */
export async function downloadDiagnostics(): Promise<string> {
  const { blob, filename } = await requestBlob('/diagnostics/export')
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
  return filename
}
