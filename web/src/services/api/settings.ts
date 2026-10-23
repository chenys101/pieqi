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
  /**
   * 新建任务的缺省工作目录（绝对路径）。
   *
   * 空串 = 未配置：此时新建任务必须显式选/填一个路径。
   * 出厂缺省是服务端的 `<数据根>/workspace`（main.go 注入），所以它通常不会为空 ——
   * 只有用户显式清空过后才是空串。前端**不自己造默认值**：路径必须是服务端上真实
   * 存在的目录，前端猜一个（例如 '~'）只会在提交时换来一句 400。
   */
  default_project_path: string
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
