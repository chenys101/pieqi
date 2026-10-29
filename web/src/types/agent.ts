// Agent 模型（方案 §22）。
// 目录由后端 GET /api/agents 下发（可用性取决于服务端配置），
// 在线状态 / 活跃会话由 Task Store 实时派生。

export interface AgentInfo {
  /** agent 业务名（claude / qoder）——与 Task.agent 同源，也是创建任务时的取值 */
  id: string
  name: string
  transport: string
  capabilities: string[]
  /** 一句话说明（新任务页选择器展示；后端 /api/agents 下发） */
  description?: string
  /**
   * 该 agent 的传输是否具备图片入站通路（新建任务页据此显示加图入口）。
   * 由服务端按配置下发（后端 agent.AgentInfo.ImagePrompt），前端不自行推断。
   * true 只表示"能把图送出去"，不保证对端模型看得见图。
   */
  imagePrompt: boolean

  // ---- 结构化能力（2026-10-18 新增）----
  //
  // 为什么不再靠 capabilities 那串文案：三个 agent 的 capabilities 此前**完全相同**
  // （都是「流式输出 / 工具审批 / 续问 / 取消」），等于没给任何信息 ——
  // 用户在 Agents 页看到三张一模一样的卡，只是换了个名字。
  //
  // 下面这些是**判据**（服务端按配置与实测结论算出），前端据此渲染，不自己推断。

  /**
   * goal 能力形态：
   * - `tool`    模型可见的工具（create_goal/update_goal），ACP 实测可通 → dsh
   * - `command` 斜杠命令 + Stop hook（claude 的 `/goal <条件>`）
   * - 缺省      该通道不开放 goal → qoder
   *
   * ⚠️ 语义差异是产品级的：`tool` 是「目标（objective）」，`command` 是
   * 「完成条件（condition）」。展示时不能统一写成"支持 goal"。
   * 详见 docs/agents-goal-and-capabilities.md。
   */
  goalMode?: 'tool' | 'command'
  /** goal 状态在 pieqi 里能不能看见（qoder 为 false；claude 曾因桥丢事件为 false，已修） */
  goalStateVisible?: boolean
  /** 是否支持「本会话内同类操作免审」（claude 走 hook，选项里没有 approve_session） */
  sessionAlways?: boolean
  /** 是否会下发可选模型清单（清单本身按需拉，见 stores/agent 的 modelsByAgent） */
  modelSelectable?: boolean
  /**
   * 建任务前可展示的 ACP 模式（来自 GET /api/agents 的 modes）。
   * 空 = 新建页不显示 ModePicker；会话协商后以 Task.modes 为准。
   */
  modes?: import('./api').ModeOptionDto[]
  /** 冷启动是否明显慢（dsh 实测 >90s）—— 用来提醒"点了创建要等一会儿" */
  coldStartCold?: boolean
}

/** Agent 运行时统计（从任务列表派生） */
export interface AgentStats {
  agentId: string
  /**
   * **不要**把它理解成"进程在线"。它只是"有活跃会话"（running / waiting_input / pending）。
   *
   * 曾经的 UI 拿它显示 `🟢 Online` —— 那是个虚假信号：后端明确不做进程探活
   * （AvailableAgents 的注释：探活要 spawn 子进程，代价与副作用都不该花在渲染下拉框上）。
   * 现在改用 lastActiveAt 展示"最近活跃"，并把"不探活"明确写出来。
   */
  online: boolean
  activeSessions: number
  totalSessions: number
  /** 最近一次活跃时刻（ISO）；无任务时为空。比布尔值诚实，也能用来看"多久没动过" */
  lastActiveAt?: string
}
