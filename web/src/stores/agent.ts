// Agent Store（方案 §22）：Agent 目录 + 从任务派生的实时统计。
// 当前后端无独立 Agent API，Watchdog 判断逻辑未来放后端（方案 §45）。

import { defineStore } from 'pinia'
import type { AgentInfo, AgentStats } from '@/types/agent'
import { useTaskStore } from './task'
import { useSessionStore } from './session'

/** 静态目录：后端 AgentManager 当前仅接 Claude Code（ACP Bridge） */
const CATALOG: AgentInfo[] = [
  {
    id: 'claude-code',
    name: 'Claude Code',
    transport: 'ACP SDK Bridge',
    capabilities: ['Streaming', 'Permission', 'Resume', 'Cancel'],
  },
]

export const useAgentStore = defineStore('agent', {
  getters: {
    agents(): AgentStats[] {
      const taskStore = useTaskStore()
      const sessionStore = useSessionStore()
      const online = sessionStore.connection === 'connected'
      return CATALOG.map((info) => {
        const own = taskStore.tasks.filter((t) => t.agent === info.id)
        return {
          agentId: info.id,
          online,
          activeSessions: own.filter((t) => t.status === 'running' || t.status === 'waiting_input' || t.status === 'pending').length,
          totalSessions: own.length,
        }
      })
    },
    catalog(): readonly AgentInfo[] {
      return CATALOG
    },
  },
})
