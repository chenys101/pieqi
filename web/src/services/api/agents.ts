// Agent API：可被任务选择的 agent 目录。
//
// 目录由**服务端配置**决定（qoder 有没有配、claude 走桥还是 print），前端不维护第二份。
// 拉取失败时由调用方（stores/agent）退回静态兜底，保证下拉框永远有可选项。

import { request } from './client'
import type { AgentsResponseDto, AgentModelsResponseDto } from '@/types/api'

/** GET /api/agents → 可选 agent 列表 + 服务端默认 agent */
export async function getAgents(): Promise<AgentsResponseDto> {
  return request<AgentsResponseDto>('/agents')
}

/**
 * GET /api/agents/:name/models → 该 agent 当前可选的模型清单。
 *
 * ⚠️ 这个请求在服务端要**起一次 agent 进程**（清单只有建会话时才拿得到），
 * 首次可能耗时数秒（服务端有 10 分钟缓存）。所以只在用户真正选中某 agent 时调用，
 * 不要预取整个目录 —— 那会把代价乘以 agent 数。
 *
 * 200 + 空 models 表示"这个 agent 不支持选模型"，不是错误；调用方隐藏下拉框即可。
 * 探测失败会抛（服务端 502），由调用方决定是提示还是静默 —— 选模型是可选能力，
 * 拿不到不该拦住创建任务/续问这条主流程。
 */
export async function getAgentModels(agent: string): Promise<AgentModelsResponseDto> {
  return request<AgentModelsResponseDto>(`/agents/${encodeURIComponent(agent)}/models`)
}
