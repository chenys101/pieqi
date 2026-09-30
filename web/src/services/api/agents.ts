// Agent API：可被任务选择的 agent 目录。
//
// 目录由**服务端配置**决定（qoder 有没有配、claude 走桥还是 print），前端不维护第二份。
// 拉取失败时由调用方（stores/agent）退回静态兜底，保证下拉框永远有可选项。

import { request } from './client'
import type { AgentsResponseDto } from '@/types/api'

/** GET /api/agents → 可选 agent 列表 + 服务端默认 agent */
export async function getAgents(): Promise<AgentsResponseDto> {
  return request<AgentsResponseDto>('/agents')
}
