// Tunnel API：status 公开可读；控制操作仅飞书移动端可用（后端 TunnelOpGate）

import { request } from './client'
import type { TunnelStatusDto, TunnelOpResultDto } from '@/types/api'

// 重新导出：组件按需引用 DTO 类型时统一从本模块取
export type { TunnelOpResultDto }

export type TunnelTTL = '15m' | '1h' | '4h'

export async function getTunnelStatus(): Promise<TunnelStatusDto> {
  return request<TunnelStatusDto>('/tunnel/status', { quiet401: true })
}

export async function startTunnel(ttl: TunnelTTL): Promise<TunnelOpResultDto> {
  return request<TunnelOpResultDto>('/tunnel/start', { method: 'POST', body: { ttl } })
}

export async function stopTunnel(): Promise<void> {
  await request('/tunnel/stop', { method: 'POST', body: {} })
}

export async function resetTunnelToken(): Promise<{ token: string }> {
  return request<{ token: string }>('/tunnel/reset', { method: 'POST', body: {} })
}

export async function renewTunnel(ttl: TunnelTTL): Promise<TunnelOpResultDto> {
  return request<TunnelOpResultDto>('/tunnel/renew', { method: 'POST', body: { ttl } })
}

/** QR 图片地址（后端渲染 PNG） */
export function tunnelQrUrl(text: string): string {
  const base = import.meta.env.VITE_API_BASE_URL || '/api'
  return `${base}/tunnel/qrcode?text=${encodeURIComponent(text)}`
}
