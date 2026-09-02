// Agent 模型（方案 §22）。
// 当前后端无独立 Agent API：Agent 目录为静态注册，
// 在线状态 / 活跃会话由 Task Store 实时派生。

export interface AgentInfo {
  id: string
  name: string
  transport: string
  capabilities: string[]
}

/** Agent 运行时统计（从任务列表派生） */
export interface AgentStats {
  agentId: string
  online: boolean
  activeSessions: number
  totalSessions: number
}
