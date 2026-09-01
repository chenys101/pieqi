// Auth API：status 公开轮询；bind/unbind 仅内网（V2 UI 不做 bind 入口，仅提示）

import { request } from './client'
import type { AuthStatusDto } from '@/types/api'

export type AuthStatus = {
  bound: boolean
  debug: boolean
  openid?: string
  nickname?: string
}

export async function getAuthStatus(): Promise<AuthStatus> {
  const dto = await request<AuthStatusDto>('/auth/status', { quiet401: true })
  return { bound: dto.bound, debug: dto.debug, openid: dto.openid, nickname: dto.nickname }
}
