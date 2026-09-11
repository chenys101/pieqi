// Bots API：IM 机器人绑定记录（复数，D1 定案）。
//
// 鉴权分层（见 internal/api/router.go）：
//   - GET  ：与 /api 同套鉴权 —— 手机 / PWA 在外网也要能看列表；
//   - 写   ：BindOpGate（仅内网）—— 绑定是高风险操作。
// 因此读写失败的原因不同：外网下**读**能成功、**写**会 403。

import { request } from './client'
import type { BotDto, BotsResponseDto, BotRoleDto } from '@/types/api'

// 组件按需引用 DTO 类型时统一从本模块取
export type { BotDto, BotRoleDto }

/** GET /api/bots：列表（按创建时间升序）+ 管理员 id */
export async function listBots(): Promise<BotsResponseDto> {
  return request<BotsResponseDto>('/bots', { quiet401: true })
}

/** POST /api/bots：非设备流创建（手动配置 / 其他渠道） */
export async function createBot(input: {
  channel: string
  name?: string
  sysPrompt?: string
  appId?: string
}): Promise<BotDto> {
  const r = await request<{ bot: BotDto }>('/bots', {
    method: 'POST',
    body: {
      channel: input.channel,
      name: input.name,
      sys_prompt: input.sysPrompt,
      app_id: input.appId,
    },
  })
  return r.bot
}

/** PATCH /api/bots/:id：补丁语义 —— 只传要改的字段 */
export async function updateBot(
  id: string,
  patch: { name?: string; sysPrompt?: string; role?: BotRoleDto },
): Promise<BotDto> {
  const body: Record<string, unknown> = {}
  if (patch.name !== undefined) body.name = patch.name
  if (patch.sysPrompt !== undefined) body.sys_prompt = patch.sysPrompt
  if (patch.role !== undefined) body.role = patch.role
  const r = await request<{ bot: BotDto }>(`/bots/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    body,
  })
  return r.bot
}

/** DELETE /api/bots/:id：解绑（同时重建运行中的实例） */
export async function deleteBot(id: string): Promise<void> {
  await request(`/bots/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

/** 渠道标签（渠道是机器人的**属性**，写在名字前半截 —— 不是分组） */
export function channelLabel(ch: BotDto['channel']): string {
  switch (ch) {
    case 'lark':
      return '飞书'
    case 'wecom':
      return '企业微信'
    case 'wechat':
      return '微信'
    default:
      return ch
  }
}
