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

const LONG_TURNS = 12

const longEvents = []
for (let t = 1; t <= LONG_TURNS; t++) {
  longEvents.push(...turnEvents(LONG_TASK_ID, t, `把第 ${t} 块的交互补上`))
}

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
  ...Array.from({ length: 40 }, (_, i) => ({
    id: `e2e-bulk-${i}`,
    source: 'web',
    agent: 'claude',
    project_id: 'pieqi-bulk',
    project_path: 'D:/code/go/pieqi-bulk',
    worktree_path: 'D:/code/go/pieqi-bulk',
    claude_session_id: `sess-bulk-${i}`,
    status: ['completed', 'running', 'pending', 'cancelled', 'failed'][i % 5],
    prompt: `批量任务 ${i}`,
    title: `批量任务 ${i}：用于列表滚动与窗口化验证`,
    events: [{ seq: 1, type: 'user', text: `批量任务 ${i}`, at: ago(200 + i) }],
    created_at: ago(200 + i),
    updated_at: ago(200 + i),
  })),
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

export const agentsResponse = {
  agents: [
    {
      name: 'claude',
      display_name: 'Claude Code',
      description: '本地 Claude Code（经 SDK 桥）',
      transport: 'sdk-bridge',
      capabilities: ['text', 'tool'],
      image_prompt: true,
    },
    { name: 'qoder', display_name: 'Qoder', description: 'qodercli --acp', transport: 'acp', capabilities: ['text'] },
  ],
  default: 'claude',
}

export const settingsResponse = {
  auto_approve_l0: true,
  auto_approve_l1: true,
  dnd_enabled: false,
  dnd_start: '22:00',
  dnd_end: '08:00',
  event_retention: 500,
}

export const authStatusResponse = { bound: false, debug: false }

export const botsResponse = { bots: [] }

export const larkRegStatusResponse = { registered: false }

export const tunnelStatusResponse = { active: false }
