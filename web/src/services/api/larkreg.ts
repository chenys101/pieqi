// LarkReg API：飞书渠道扫码接入 / 手动配置（仅内网，外网 403）

import { request, ApiError } from './client'
import type { LarkRegStatusDto, LarkRegConfigDto } from '@/types/api'

export interface LarkStatus {
  /** ok=false 且 status=403 → 外网不可配置 */
  status: number
  registered: boolean
  appId: string
}

export async function getLarkStatus(): Promise<LarkStatus> {
  try {
    const dto = await request<LarkRegStatusDto>('/larkreg/status', { quiet401: true })
    return { status: 200, registered: !!dto.registered, appId: dto.app_id ?? '' }
  } catch (err) {
    if (err instanceof ApiError) {
      return { status: err.status, registered: false, appId: '' }
    }
    throw err
  }
}

/** POST /api/larkreg/start：返回扫码 URL */
export async function startLarkReg(): Promise<string> {
  const r = await request<{ qr_url: string }>('/larkreg/start', { method: 'POST', body: {} })
  return r.qr_url
}

export type LarkPollResult =
  | { state: 'pending' }
  | { state: 'done'; appId: string; hint?: string }
  | { state: 'error'; message: string }

/** GET /api/larkreg/poll：202 等待 / 200 完成 / 其他失败 */
export async function pollLarkReg(): Promise<LarkPollResult> {
  try {
    const r = await request<{ app_id?: string; hint?: string }>('/larkreg/poll', { quiet401: true })
    return { state: 'done', appId: r.app_id ?? '', hint: r.hint }
  } catch (err) {
    if (err instanceof ApiError) {
      if (err.status === 202) return { state: 'pending' }
      return { state: 'error', message: err.message }
    }
    throw err
  }
}

/** GET /api/larkreg/config：secret 不回显，只回 secret_set */
export async function getLarkConfig(): Promise<LarkRegConfigDto> {
  return request<LarkRegConfigDto>('/larkreg/config', { quiet401: true })
}

export interface LarkConfigInput {
  appId: string
  appSecret: string
  verifyToken: string
  encryptKey: string
  eventMode: 'longconn' | 'webhook'
}

/** POST /api/larkreg/config：保存后热应用 */
export async function saveLarkConfig(input: LarkConfigInput): Promise<string> {
  const r = await request<{ message?: string }>('/larkreg/config', {
    method: 'POST',
    body: {
      app_id: input.appId,
      app_secret: input.appSecret,
      verify_token: input.verifyToken,
      encrypt_key: input.encryptKey,
      event_mode: input.eventMode,
    },
  })
  return r.message ?? '已保存'
}
