// Agent Store（方案 §22）：Agent 目录 + 从任务派生的实时统计。
//
// 目录来源：**服务端** GET /api/agents（agent 可用性由服务端配置决定：qoder 配没配、
// claude 走桥还是 print）。前端不再维护第二份目录 —— 两份事实源必然漂移，表现就是
// 「配了 qoder 前端不显示」或「显示了但选中即失败」。静态 FALLBACK 只在请求失败时兜底。
//
// id 用的是 agent 业务名（claude / qoder），与 Task.agent / POST /api/tasks 的 agent 字段
// 同一套取值 —— 三处同名，下面按 agentId 过滤的统计才真正对得上。

import { defineStore } from 'pinia'
import type { AgentInfo, AgentStats } from '@/types/agent'
import { getAgents, getAgentModels } from '@/services/api/agents'
import type { AgentModelDto } from '@/types/api'
import { useTaskStore } from './task'
import { useSessionStore } from './session'

/** 兜底目录：请求失败时至少让选择器/列表有 Claude Code 可显示（服务端保证存在的那个）。 */
const FALLBACK: AgentInfo[] = [
  {
    id: 'claude',
    name: 'Claude Code',
    transport: 'SDK Bridge',
    capabilities: ['流式输出', '工具审批', '续问', '取消'],
  },
]

export const useAgentStore = defineStore('agent', {
  state: () => ({
    catalog: [...FALLBACK] as AgentInfo[],
    /** 服务端默认 agent（新任务页选择器初始选中）。空 = 用目录首位。 */
    defaultAgentId: '',
    /** 目录是否已从服务端成功加载（失败时保持兜底，不必反复重试） */
    loaded: false,
    /**
     * 各 agent 的可选模型清单（agentId → models）。**按需加载**：只有用户真正需要
     * 某个 agent 的清单（新任务页选中它、或在会话页续问）才去拉 —— 服务端取清单要起
     * 一次 agent 进程，预取整个目录会把这份代价乘以 agent 数。
     * 空数组 = 已加载过且该 agent 没有可选模型（据此隐藏下拉框，不再重复请求）。
     */
    modelsByAgent: {} as Record<string, AgentModelDto[]>,
    /** 每个 agent 的模型清单是否已尝试加载过（含"支持但为空"的结果） */
    modelsLoaded: {} as Record<string, boolean>,
    /** 正在拉取清单的 agent（下拉框显示加载中） */
    modelsLoading: {} as Record<string, boolean>,
  }),

  getters: {
    agents(state): AgentStats[] {
      const taskStore = useTaskStore()
      const sessionStore = useSessionStore()
      const online = sessionStore.connection === 'connected'
      return state.catalog.map((info) => {
        const own = taskStore.tasks.filter((t) => t.agent === info.id)
        return {
          agentId: info.id,
          online,
          activeSessions: own.filter((t) => t.status === 'running' || t.status === 'waiting_input' || t.status === 'pending').length,
          totalSessions: own.length,
        }
      })
    },
    /** 新任务页选择器默认选中的 agent（服务端默认 > 目录首位 > 兜底） */
    defaultAgent(state): string {
      return state.defaultAgentId || state.catalog[0]?.id || FALLBACK[0].id
    },
    /** 按 id 取目录项（选择器展示 transport / 说明用） */
    byId(state) {
      return (id: string): AgentInfo | undefined => state.catalog.find((a) => a.id === id)
    },
    /**
     * 某 agent 的可选模型（未加载过返回空数组）。
     * 空数组是"没有可选模型"的统一表示：未加载 / 加载中 / 该 agent 不支持 ——
     * 三种情形前端行为一致（不显示下拉框），故不额外区分。
     */
    modelsOf(state) {
      return (id: string): AgentModelDto[] => state.modelsByAgent[id] ?? []
    },
  },

  actions: {
    /** 拉取服务端 agent 目录（幂等：已成功加载过则跳过）。失败保持兜底目录。 */
    async loadCatalog(force = false) {
      if (this.loaded && !force) return
      try {
        const res = await getAgents()
        const agents = res.agents ?? []
        if (!agents.length) return
        this.catalog = agents.map((a) => ({
          id: a.name,
          name: a.display_name || a.name,
          transport: a.transport || '',
          capabilities: a.capabilities ?? [],
          description: a.description,
        }))
        this.defaultAgentId = res.default || agents[0].name
        this.loaded = true
      } catch {
        // 静默保持兜底：目录拉不到不该拦住「新建任务」这条主流程
      }
    },

    /**
     * 拉取某 agent 的可选模型清单（幂等：试过一次就不再试）。
     *
     * 失败**不写** modelsLoaded，下次选中时还能再试 —— 服务端探测失败可能只是
     * 一次冷启超时（agent 进程首次拉起要几秒），把它当永久结论会让下拉框一直空着。
     * 失败也不抛：选模型是**可选**能力，拿不到就退回"用 agent 默认"，
     * 不该挡住创建任务/续问这条主流程（同 loadCatalog 的取舍）。
     */
    async loadModels(agentId: string) {
      if (!agentId) return
      if (this.modelsLoaded[agentId] || this.modelsLoading[agentId]) return
      this.modelsLoading = { ...this.modelsLoading, [agentId]: true }
      try {
        const res = await getAgentModels(agentId)
        this.modelsByAgent = { ...this.modelsByAgent, [agentId]: res.models ?? [] }
        this.modelsLoaded = { ...this.modelsLoaded, [agentId]: true }
      } catch {
        // 保持未加载：下次再试
      } finally {
        this.modelsLoading = { ...this.modelsLoading, [agentId]: false }
      }
    },
  },
})
