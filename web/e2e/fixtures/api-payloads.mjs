// e2e 夹具数据的**唯一来源**：mock-api 服务与 Playwright 用例共用。
//
// 为什么必须共用：视觉回归的判据是"这一版和上一版画出来一样不一样"。
// 如果 mock 服务给的是一份数据、用例断言里引用的是另一份，那么任何一次
// "只改夹具"都会让两侧悄悄错开 —— diff 全红或更糟：全绿但测的不是同一屏。
//
// 形状全部对齐 web/src/types/api.ts 的 DTO（snake_case wire format）。
// 这里刻意**不引 TypeScript**：mock 服务是 `node xxx.mjs` 直接跑的，
// 不走 vite/tsc；DTO 的守卫交给 `npm run typecheck`（真实组件消费同一批字段）。

const T0 = '2026-01-01T08:00:00Z'

/** 相对当前时间的 ISO 串（列表会显示"x 分钟前"，固定时间会让视觉回归永远漂移） */
function ago(min) {
  return new Date(Date.now() - min * 60_000).toISOString()
}

/**
 * 一条 Turn 的事件。`turn` 会出现在前几条事件的 text 里 —— 夹具页
 * （e2e/fixtures/page-fixture.ts）靠它把事件映射回 Turn N。
 */
function turnEvents(taskId, turn, prompt) {
  const seqBase = (turn - 1) * 3 + 1
  const at = ago(600 - turn * 10)
  return [
    { seq: seqBase, type: 'user', text: `[turn:${turn}] ${prompt}`, at },
    { seq: seqBase + 1, type: 'thinking', text: `分析需求 ${turn}…`, at },
    { seq: seqBase + 2, type: 'text', text: `第 ${turn} 轮完成：${prompt}`, at },
  ]
}

/** 长会话：12 轮 —— 正是"挂载全部 Turn 会不会把 DOM 撑爆"的实测形状（对应 21,206 → 2,135 那条基线） */
export const LONG_TASK_ID = 'e2e-long-12'

/**
 * 夹具目录里的**默认执行 Agent** = dsh（默认值而非列表首项）。
 *
 * ⚠️ ESM 的 `export const` 与 `const` 一样有 TDZ：agentsResponse 里 `default:` 引用它时
 * 必须已经初始化 —— 所以这条声明放在文件**靠前**，不能挪到 agentsResponse 后面。
 *
 * 为什么要换掉原来的 `claude`：claude 的 `model_selectable: false`，模型清单恒为空，
 * 于是输入框里的模型选择器只剩「◈ Agent 默认 ▾」（约 105px），而用户实际看到的
 * 是**选了具体模型之后的长名字**（如「◈ DeepSeek-V4.1-Flash」约 158px）。
 * 宽触发器 + 加图按钮 + 发送按钮共处一行时，宽度预算完全不同 ——
 * 用 claude 当默认，等于把这一行最挤的形态排除在验收之外（2026-10-19 踩到）。
 * claude 保留在列表里：能力矩阵仍要三张卡。
 */
export const FIXTURE_DEFAULT_AGENT = 'dsh'

const LONG_TURNS = 12

const longEvents = []
for (let t = 1; t <= LONG_TURNS; t++) {
  longEvents.push(...turnEvents(LONG_TASK_ID, t, `把第 ${t} 块的交互补上`))
}

/**
 * 语义工具调用（goal / todo / plan）—— **插在最后一轮（Turn 12）内部**。
 *
 * 为什么夹具里要有：语义卡（ToolCard 的 goal/todo/plan 分支）此前零覆盖，
 * 而"能把这几个工具画成什么样"只有截图能看出来。没有它们，视觉基线里永远
 * 只有通用 🔧 卡，等于新功能没验证。
 *
 * ⚠️ 位置很关键：**必须落在已挂载的 Turn 里**，不能追加在末尾。
 *   时间线默认只挂最近 MOUNT_WINDOW 轮，追加到末尾 = 这些卡在折叠区里，
 *   断言能找到元素、截图却看不见 —— 那是一种"绿着但没验证"的假象。
 *   （第一版就是这么写的，截图里果然一个语义卡都没有。）
 *
 * ⚠️ 也不能带 `[turn:N]` 前缀：feedbackResponse() 靠那个前缀归 Turn，
 *   混进去会让 Turn 分组错乱。
 */
// seq 从最后一轮的末条 + 1 开始（Turn 12 是第 12*3=36 条，seq 34/35/36）。
const SEM_SEQ0 = longEvents.length + 1
const semanticToolEvents = [
  {
    seq: SEM_SEQ0,
    type: 'tool_use',
    tool_name: 'create_goal',
    tool_use_id: 'tu-goal-1',
    input: { objective: '把 web/e2e 的两层测试在 Dashboard 上也跑起来' },
    at: ago(8),
  },
  {
    seq: SEM_SEQ0 + 1,
    type: 'tool_use',
    tool_name: 'todo_write',
    tool_use_id: 'tu-todo-1',
    input: {
      todos: [
        { content: '加 dashboard 场景', status: 'completed' },
        { content: '重拍基线', status: 'completed' },
        { content: '补真点击用例', status: 'pending' },
      ],
    },
    at: ago(7),
  },
  {
    seq: SEM_SEQ0 + 2,
    type: 'tool_use',
    tool_name: 'exit_plan_mode',
    tool_use_id: 'tu-plan-1',
    input: { plan: '第一步：把输入区改成自适应高度\n第二步：重拍基线' },
    at: ago(6),
  },
  // 对照组：一个**认不出**的工具，必须仍走通用 🔧 卡（防止语义表把一切都吞掉）
  {
    seq: SEM_SEQ0 + 3,
    type: 'tool_use',
    tool_name: 'Bash',
    tool_use_id: 'tu-bash-1',
    input: { command: 'npm run test:e2e' },
    at: ago(5),
  },
]
// 直接推进 longEvents（它们就属于 Turn 12 —— 最后一个 [turn:12] 之后，
// 下一条 user 事件之前，所以分组会归到 Turn 12 里，而不是末尾的折叠区）。
longEvents.push(...semanticToolEvents)

export const tasks = [
  // ① 长会话 + 待审批（approval 路径，进程存活）。放第一位是因为
  //    "仪表盘的待审批组"与"会话内联决策卡"是同一种卡的两种位置，一份夹具覆盖两屏。
  {
    id: LONG_TASK_ID,
    source: 'web',
    agent: 'claude',
    project_id: 'pieqi',
    project_path: 'D:/code/go/pieqi',
    worktree_path: 'D:/code/go/pieqi',
    claude_session_id: 'sess-e2e-1',
    status: 'waiting_input',
    prompt: '把时间线的 12 轮交互都补上',
    title: '补全会话时间线交互',
    events: longEvents,
    current_decision: {
      id: 'dec-l2',
      kind: 'approval',
      tool_name: 'Bash',
      risk: 'L2',
      summary: 'pnpm test -- --run\n(执行本仓库全部单元测试)',
      options: ['approve', 'approve_session', 'deny'],
      created_at: ago(3),
    },
    created_at: ago(720),
    updated_at: ago(3),
    started_at: ago(720),
    diff_stat: { files: 6, additions: 128, deletions: 31, captured_at: ago(3) },
    usage: { used: 42_000, size: 200_000, cost_usd: 0.42, has_cost: true, at: ago(3) },
    interventions: [],
  },

  // ② 高风险待审批（L3）：卡片要走"内联二次确认"，且按风险降序应排在 L2 之前
  {
    id: 'e2e-l3',
    source: 'lark',
    agent: 'claude',
    project_id: 'pieqi',
    project_path: 'D:/code/go/pieqi',
    worktree_path: 'D:/code/go/pieqi',
    claude_session_id: 'sess-e2e-2',
    status: 'waiting_input',
    prompt: '清理构建产物',
    title: '清理过期构建缓存',
    events: [
      { seq: 1, type: 'user', text: '清理过期构建缓存', at: ago(40) },
      { seq: 2, type: 'text', text: '需要删除 build/ 下的过期产物。', at: ago(39) },
    ],
    current_decision: {
      id: 'dec-l3',
      kind: 'approval',
      tool_name: 'Bash',
      risk: 'L3',
      summary: 'rm -rf build/release/*',
      options: ['approve', 'deny'],
      created_at: ago(12),
    },
    created_at: ago(40),
    updated_at: ago(12),
    interventions: [],
  },

  // ③ 空事件的任务：空态是最高频的 AI 漏项，必须在视觉基线里
  {
    id: 'e2e-empty',
    source: 'web',
    agent: 'claude',
    project_id: 'pieqi-web',
    project_path: 'D:/code/go/pieqi/web',
    worktree_path: 'D:/code/go/pieqi/web',
    claude_session_id: 'sess-e2e-3',
    status: 'pending',
    prompt: '',
    title: '',
    events: [],
    created_at: ago(2),
    updated_at: ago(2),
  },

  // ④ 超长标题 + 失败态：截断、溢出与"误判成已完成"的高发点
  {
    id: 'e2e-long-title',
    source: 'web',
    agent: 'claude',
    project_id: 'pieqi',
    project_path: 'D:/code/go/pieqi',
    worktree_path: 'D:/code/go/pieqi',
    claude_session_id: 'sess-e2e-4',
    status: 'failed',
    prompt: '为一个长度远超常规的任务标题做溢出验证',
    title:
      '修复在超长标题下侧栏任务行溢出并挤压时间戳、以及移动端底栏被遮挡导致主行动按钮不可见的问题（这是一条刻意超长的标题）',
    events: [{ seq: 1, type: 'user', text: '为超长标题做溢出验证', at: ago(90) }],
    error: 'agent 进程退出码 1',
    created_at: ago(90),
    updated_at: ago(88),
  },

  // ⑤ 大量任务（列表窗口化的形状：1000+ 才是真实量级，先给 40 条）
  ...Array.from({ length: 40 }, (_, i) => {
    const status = ['completed', 'running', 'pending', 'cancelled', 'failed'][i % 5]
    // ⚠️ completed 任务必须带 finished_at（以及 started_at / diff_stat），否则
    //   computeWeeklyInsight 会把它们**全部跳过**（判据是 `status==='completed' &&
    //   finishedAt`，见 features/dashboard/insight.ts:97）。缺了它，Dashboard 的
    //   "本周概览"会显示「完成任务 0」，而夹具里明明有 8 个 completed ——
    //   夹具与产品的判断口径分叉，是"基线守住了一个假状态"的典型来源。
    const createdMin = 200 + i
    return {
      id: `e2e-bulk-${i}`,
      source: 'web',
      agent: 'claude',
      project_id: 'pieqi-bulk',
      project_path: 'D:/code/go/pieqi-bulk',
      worktree_path: 'D:/code/go/pieqi-bulk',
      claude_session_id: `sess-bulk-${i}`,
      status,
      prompt: `批量任务 ${i}`,
      title: `批量任务 ${i}：用于列表滚动与窗口化验证`,
      events: [{ seq: 1, type: 'user', text: `批量任务 ${i}`, at: ago(createdMin) }],
      created_at: ago(createdMin),
      updated_at: ago(createdMin),
      ...(status === 'completed'
        ? {
            // 完成时间落在**本周窗口内**（< 6 天），这样 completedCount > 0。
            started_at: ago(createdMin + 3),
            finished_at: ago(createdMin - 2),
            diff_stat: { files: 2, additions: 30, deletions: 4, captured_at: ago(createdMin - 2) },
            // 一次通过率的分母含 completed；一半给干预、一半不给，让比率不是 100%/0%
            interventions:
              i % 10 === 0
                ? [{ id: `iv-${i}`, task_id: `e2e-bulk-${i}`, kind: 'decision', choice: 'approve', source: 'web', created_at: ago(createdMin + 1) }]
                : [],
          }
        : {}),
    }
  }),
]

/** 轻量视图（列表）：**不含 events**，带 event_count */
export function toSummary(t) {
  const { events, current_decision, ...rest } = t
  const s = { ...rest, event_count: events?.length ?? 0 }
  if (current_decision) s.current_decision = current_decision
  return s
}

/** GET /api/tasks 响应：按 project_path 分组 */
export function tasksResponse() {
  const byProject = new Map()
  for (const t of tasks) {
    const key = t.project_path
    if (!byProject.has(key)) {
      byProject.set(key, {
        project_id: t.project_id,
        project_path: t.project_path,
        counts: { pending: 0, running: 0, waiting_input: 0, completed: 0, failed: 0, cancelled: 0 },
        tasks: [],
      })
    }
    const g = byProject.get(key)
    g.counts[t.status] = (g.counts[t.status] ?? 0) + 1
    g.tasks.push(toSummary(t))
  }
  return { projects: [...byProject.values()] }
}

/** 反馈总览（FeedbackPanel）：按夹具里的事件里的 `[turn:N]` 反推 Turn */
export function feedbackResponse(taskId) {
  const t = tasks.find((x) => x.id === taskId)
  const turns = []
  if (t) {
    for (const e of t.events ?? []) {
      const m = typeof e.text === 'string' ? /^\[turn:(\d+)\] (.*)$/.exec(e.text) : null
      if (!m) continue
      const turn = Number(m[1])
      turns.push({
        turn,
        start_event_seq: e.seq,
        user_prompt: m[2],
        summary: { files: turn % 3, additions: turn * 4, deletions: turn % 2, modifies: turn % 3 },
      })
    }
  }
  const total = turns.reduce(
    (a, x) => ({
      files: a.files + x.summary.files,
      additions: a.additions + x.summary.additions,
      deletions: a.deletions + x.summary.deletions,
    }),
    { files: 0, additions: 0, deletions: 0 },
  )
  return {
    task_id: taskId,
    turns,
    cumulative: total,
    changes: [],
    checkpoints: [],
    baseline: { head_sha: 'deadbeef', captured_at: T0 },
    preview: { state: 'idle' },
  }
}

/**
 * Agent 目录夹具。
 *
 * ★ 三个 agent 的**结构化能力必须能看出差异** —— 这正是 2026-10-18 那批改动要守的：
 * 此前三张卡的能力行完全相同，用户看不出该选谁。夹具按**实测结论**填
 * （见 docs/agents-goal-and-capabilities.md），不是随手写的：
 *   · claude：goal 是 `command`（/goal <条件> + Stop hook），无同类免审（走 hook）
 *   · qoder ：goal **不可用**（ACP 只开 4 个命令、goal 走 SDK control request）
 *   · dsh   ：goal 是 `tool`（create_goal 实测可通），冷启动 >90s
 * 夹具若把这些抹平，截图就会退化成"三张一样的卡"，测试也就守不住差异了。
 */
export const agentsResponse = {
  agents: [
    {
      name: 'claude',
      display_name: 'Claude Code',
      description: 'Anthropic 官方 Agent，经 SDK 常驻桥驱动',
      transport: 'SDK Bridge',
      capabilities: ['流式输出', '工具审批', '续问', '取消'],
      image_prompt: true,
      goal_mode: 'command',
      goal_state_visible: true,
      session_always: false,
      model_selectable: false,
      cold_start_cold: false,
    },
    {
      name: 'qoder',
      display_name: 'Qoder CLI',
      description: 'Qoder 官方 CLI（ACP 协议），可选 Qwen3.8-Flash',
      transport: 'ACP',
      capabilities: ['流式输出', '工具审批', '续问', '取消'],
      image_prompt: true,
      goal_state_visible: false,
      session_always: true,
      model_selectable: true,
      cold_start_cold: false,
    },
    {
      name: 'dsh',
      display_name: 'DeepSeek Harness',
      description: 'DeepSeek 官方 agent harness（dsh --profile acp）',
      transport: 'ACP',
      capabilities: ['流式输出', '工具审批', '续问', '取消'],
      image_prompt: true,
      goal_mode: 'tool',
      goal_state_visible: true,
      session_always: true,
      model_selectable: true,
      cold_start_cold: true,
    },
  ],
  default: FIXTURE_DEFAULT_AGENT,
}

/**
 * 模型清单（GET /api/agents/:name/models）。**带分组与长名字**，因为选择器的
 * 触发器与弹层宽度都由它决定；空清单或短名字量不出真实压力。
 */
export const agentModelsResponse = {
  dsh: {
    agent: 'dsh',
    models: [
      { value: 'deepseek-official', name: 'DeepSeek-V4.1-Flash', description: '走官方 API，日常最快', group: 'DeepSeek' },
      { value: 'deepseek-v4-pro', name: 'DeepSeek-V4-Pro', description: '推理更强、更慢', group: 'DeepSeek' },
      { value: 'workbuddy/glm-5.3-flash', name: 'GLM-5.3-Flash', description: '', group: '智谱' },
      { value: 'qwen/qwen3.8-flash', name: 'Qwen3.8-Flash', description: '免费额度', group: '通义' },
    ],
  },
  qoder: {
    agent: 'qoder',
    models: [{ value: 'Qwen3.8-Flash', name: 'Qwen3.8-Flash', description: '免费模型', group: '' }],
  },
  // claude 目录里是 `model_selectable: false`，但夹具**照样给清单**：
  // 会话场景直接挂 InterveneInput，父组件（SessionPage）不在，上一轮选过的模型
  // 不会自己回来 —— 只有让用户能在选择器里选一个，才能渲染出真实形态
  // （「◈ DeepSeek-V4.1-Flash」约 158px 的触发器，与加图按钮、发送按钮同一行）。
  // 那一行是不是放得下，正是本仓库 2026-10-19 那次回归的判据。
  claude: {
    agent: 'claude',
    models: [
      { value: 'sonnet', name: 'Claude Sonnet 4.5', description: '默认，均衡', group: 'Anthropic' },
      { value: 'opus', name: 'Claude Opus 4.5', description: '最强，最慢', group: 'Anthropic' },
      { value: 'haiku', name: 'Claude Haiku 4.5', description: '最快', group: 'Anthropic' },
    ],
  },
}

export const settingsResponse = {
  auto_approve_l0: true,
  auto_approve_l1: true,
  dnd_enabled: false,
  dnd_start: '22:00',
  dnd_end: '08:00',
  event_retention: 500,
}

/**
 * `bound: true` —— 刻意"已绑定"。
 *
 * 判据：`store/app.ts` 的 loadBootContext 在 `!bound` 时会**异步**置一条两行橙条横幅
 * （"系统尚未绑定飞书管理员账号…"），它会把下方所有元素整体下移 12~36px。
 * 夹具页虽然在 seed() 里清空过 banner，但 loadBootContext 是**之后**才返回的，
 * 于是横幅又回来了 —— 视觉基线里就带着一条与业务无关、且随接口时序时有时无的条。
 * 让 mock 回 bound:true 是从源头消除（而不是在夹具里跟时序赛跑）。
 *
 * 若将来要专门测"未绑定横幅"，应当单独一条用例显式覆盖它，而不是让它污染所有基线。
 */
export const authStatusResponse = { bound: true, debug: false }

export const botsResponse = { bots: [] }

export const larkRegStatusResponse = { registered: false }

export const tunnelStatusResponse = { active: false }
