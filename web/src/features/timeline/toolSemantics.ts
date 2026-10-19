// 工具语义表：把"能认出是什么"的工具从通用 🔧 卡升级为带语义的卡片。
//
// ★ 判据（必须写死在这里，否则以后一定有人把它接到审批上）：
//
//   **本表只影响展示，绝不参与审批/免审判定。**
//
//   审批的判据是 internal/core/settings_store.go 的 `riskLevelKinds`（按 ACP ToolKind
//   分级），而 dsh 的 `create_goal` / `todo_write` 等**刻意留在 other（L2 强制人工）**
//   —— 见 internal/agent/toolkind_map.go 的注释：它们不是只读（job_kill 能杀后台任务、
//   create_goal 会改会话目标），但也不该冒充 edit/execute 去蹭免审。
//
//   所以 "能把这几个工具画得好看一点" 与 "它们该不该被自动放行" 是**两个问题**。
//   把本表当成放行依据，等于让"卡片画得好"变成"权限更宽"——那是最不该发生的耦合。
//
// 第二个判据：**认不出就不猜**。表里没有的工具一律走通用卡（原名 + 全部参数），
// 不做启发式归类 —— 猜错的代价是用户看到一张写着"计划"的卡片，实际是一次删除。

/** 工具语义类别。决定用哪张卡片渲染，仅此而已。 */
export type ToolSemantic = 'goal' | 'todo' | 'plan' | 'subagent' | 'jobs'

/**
 * 工具名 → 语义。键是**小写归一**后的名字（三家 agent 的命名风格不同：
 * dsh 全小写、claude 首字母大写、qoder 还会拼上参数）。
 *
 * 逐条说明为什么这一项值得单独一张卡：
 *   · goal     —— "这次任务的目标变了"比"某个工具被调了"重要得多，用户要一眼看到
 *   · todo     —— 计划清单，是"agent 打算怎么做"的唯一可见面
 *   · plan     —— 待批准的方案，与审批卡同等重要的决策点
 *   · subagent —— 有从属关系（谁派的任务），扁平渲染会丢掉层级
 *   · jobs     —— 后台任务（能杀、能读输出），与普通命令调用的生命周期不同
 */
export const TOOL_SEMANTICS: Record<string, ToolSemantic> = {
  // goal（dsh 的工具；qoder 同名工具在 ACP 下不可达，但表里保留 —— 名字就是名字）
  create_goal: 'goal',
  update_goal: 'goal',
  get_goal: 'goal',
  clear_goal: 'goal',
  // 计划清单
  todo_write: 'todo',
  todowrite: 'todo',
  // 方案（dsh 的 exit_plan_mode；claude 侧是 permissionMode）
  exit_plan_mode: 'plan',
  enter_plan_mode: 'plan',
  // 子代理
  subagent: 'subagent',
  task: 'subagent', // claude 的子代理工具就叫 Task
  list_agents: 'subagent',
  // 后台任务
  job_list: 'jobs',
  job_output: 'jobs',
  job_kill: 'jobs',
}

/**
 * 取工具语义。**认不出返回 undefined**（走通用卡），不猜。
 *
 * 名字归一：小写 + 取首词。取首词是因为 qoder 会把参数拼在名字后面
 * （`Read <path>`、`Subagent <desc>` 这种形态，见 internal/agent/toolkind_map.go）。
 */
export function semanticOf(toolName: string): ToolSemantic | undefined {
  if (!toolName) return undefined
  const head = toolName.trim().split(/\s+/)[0].toLowerCase()
  return TOOL_SEMANTICS[head]
}

/** 每类语义在卡片上的标题（人读）。 */
export const SEMANTIC_LABEL: Record<ToolSemantic, string> = {
  goal: '目标',
  todo: '计划清单',
  plan: '方案',
  subagent: '子代理',
  jobs: '后台任务',
}

/**
 * 从工具入参里取"最该展示的那一句"。
 *
 * 判据：**用户扫一眼就知道发生了什么**，而不是把整个 JSON 摊开。
 * 取不到就返回空串（卡片会退回显示全部参数）。
 */
export function headlineOf(semantic: ToolSemantic, input: unknown): string {
  const o = (input ?? {}) as Record<string, unknown>
  const str = (k: string): string => (typeof o[k] === 'string' ? (o[k] as string) : '')
  switch (semantic) {
    case 'goal':
      // create_goal(objective) / update_goal(action, objective)
      return str('objective') || str('action') || str('goal_id')
    case 'todo':
      // todo_write(todos: [{content, status}]) —— 说"几项、几项完成"
      return todosSummary(o.todos)
    case 'plan':
      // exit_plan_mode(plan) —— 取首行
      return (str('plan') || str('summary')).split('\n')[0]
    case 'subagent':
      // subagent(description) / Task(description)
      return str('description') || str('prompt') || str('name')
    case 'jobs':
      return str('job_id') || str('jobId') || str('id')
  }
}

/** 计划清单的摘要："5 项 · 2 完成"。取不到就返回空串（不编造）。 */
function todosSummary(raw: unknown): string {
  if (!Array.isArray(raw)) return ''
  const total = raw.length
  if (!total) return ''
  const done = raw.filter((t) => {
    const s = (t as Record<string, unknown>)?.status
    return s === 'completed' || s === 'done'
  }).length
  return `${total} 项 · ${done} 完成`
}
