// LarkReg API：飞书渠道扫码接入 / 手动配置（**仅内网**，外网 403）
//
// 内网判定就靠这里的 403：`GET /larkreg/status` 也挂在 BindOpGate 后面，
// 外网一律 403 —— 所以它既是"接入状态"查询，也是"能不能操作"的探针。

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

export interface LarkStartResult {
  qrUrl: string
  /** 二维码有效期（秒）。SDK 默认 600；到点即失效，唯一出路是重新生成。 */
  expireIn: number
}

/**
 * POST /api/larkreg/start：拿授权链接（渲染成二维码）。
 *
 * `sysPrompt` 是预设提示词（D2：跟随 bot 记录）。它在**扫码之前**就交给服务端，
 * poll 成功后随新建的机器人一起落盘 —— 而不是扫完再补一次请求
 * （那会出现"码已扫、提示词还没写进去"的中间态）。
 */
export async function startLarkReg(sysPrompt = ''): Promise<LarkStartResult> {
  const r = await request<{ qr_url?: string; expire_in?: number; status?: string }>(
    '/larkreg/start',
    { method: 'POST', body: { sys_prompt: sysPrompt } },
  )
  // 202 = 链接还没出现（服务端内部最多等 3s）。这不该发生，但真发生时
  // 要显式失败，否则弹层会停在一个空白二维码上。
  if (!r.qr_url) throw new ApiError('二维码链接生成超时，请重试', 202)
  return { qrUrl: r.qr_url, expireIn: r.expire_in && r.expire_in > 0 ? r.expire_in : 600 }
}

export type LarkPollResult =
  | { state: 'pending' }
  | { state: 'done'; appId: string; botId: string; hint?: string }
  | { state: 'error'; message: string }

/** GET /api/larkreg/poll：202 等待 / 200 完成 / 其他失败 */
export async function pollLarkReg(): Promise<LarkPollResult> {
  try {
    const r = await request<{ app_id?: string; bot_id?: string; hint?: string }>('/larkreg/poll', {
      quiet401: true,
    })
    return { state: 'done', appId: r.app_id ?? '', botId: r.bot_id ?? '', hint: r.hint }
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
  /** 预设提示词：服务端在落 bot 记录时写入（手动配置也是"创建一台机器人"） */
  sysPrompt?: string
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
      sys_prompt: input.sysPrompt ?? '',
    },
  })
  return r.message ?? '已保存'
}
