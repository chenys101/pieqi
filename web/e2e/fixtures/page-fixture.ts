// 真机验收页：**只给 e2e 用**，不进生产构建。
//
// 为什么需要它：`web/src` 里没有"12 轮会话 + 待审批卡"这种形状的可直达页面。
// 若 e2e 直接打 `/sessions/e2e-long-12`，页面会去 store 里找任务，找不到就渲染空态 ——
// 于是"看起来测通了"，实际测的是一屏空壳。这属于最典型的静默失败。
//
// 本页把夹具（e2e/fixtures/api-payloads.mjs 的同一份数据，经**同一套适配器** adaptTask）
// 灌进 store，然后挂载**真实的生产组件**（SessionTimeline / ApprovalCard / FeedbackPanel）。
//
// ⚠️ 这里只能 import 与挂载，不能"简化样式"：一旦在这里放宽 class 或省略容器，
// 量出来的尺寸就不是生产尺寸，视觉基线也就失去意义。
// ⚠️ 走 adaptTask 而不是手搓 Task 对象：适配层就是生产路径（字段名、默认值、归一化），
// 手搓等于测了一套只存在于 e2e 里的数据结构。

import { h, defineComponent } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory, type Router } from 'vue-router'
import SessionTimeline from '@/features/session/components/SessionTimeline.vue'
import InterveneInput from '@/features/session/components/InterveneInput.vue'
import ApprovalCard from '@/features/approval/components/ApprovalCard.vue'
import FeedbackPanel from '@/features/feedback/components/FeedbackPanel.vue'
import DashboardPage from '@/pages/DashboardPage.vue'
import AgentsPage from '@/pages/AgentsPage.vue'
import SettingsPage from '@/pages/SettingsPage.vue'
import NewTaskPage from '@/pages/NewTaskPage.vue'
import SessionPage from '@/pages/SessionPage.vue'
import { useAgentStore } from '@/stores/agent'
import { getSettings } from '@/services/api/settings'
import { useSessionStore } from '@/stores/session'
import { useTaskStore } from '@/stores/task'
import { useAppStore } from '@/stores/app'
import { adaptTask } from '@/services/api/client'
import { intervene } from '@/services/api/tasks'
import type { TaskDto } from '@/types/api'
import type { ApprovalRequest } from '@/types/approval'

// @ts-expect-error —— .mjs 无类型声明；夹具本身由 mock-api 同源消费，形状由 DTO 守卫
import { tasks, LONG_TASK_ID } from '../fixtures/api-payloads.mjs'

const FIXTURE_TASKS = tasks as TaskDto[]
const LONG_ = FIXTURE_TASKS.find((t) => t.id === LONG_TASK_ID) as TaskDto

/**
 * 夹具 DTO → 生产领域模型（与 taskStore.loadTasks 走同一条适配）
 *
 * `opts.tasksDeferred`：**先不灌任务清单**，只把 `loaded` 置真后再由调用方在
 * 一个延迟后 `seedTasks()`。用它复现"设置先到、项目清单后到"的真实时序
 * （见 mock-api 的 `_delay` 与 vite.config 的 e2e 代理）。
 */
function seed(opts: { tasksDeferred?: boolean } = {}): void {
  const pinia = createPinia()
  setActivePinia(pinia)

  const taskStore = useTaskStore()
  // `loaded` 必须显式置 true：新建任务页的判空逻辑等它（区分"还没到"与"确实没有"，
  // 见 NewTaskPage 的 watch）。夹具是**同步**灌数据的，不像生产那样有网络往返，
  // 不设的话页面会一直停在"项目还没到"的观感上。
  taskStore.loaded = true
  if (!opts.tasksDeferred) taskStore.tasks = FIXTURE_TASKS.map(adaptTask)
  useSessionStore().syncFromTask(LONG_)

  // 顶部横幅（"尚未绑定飞书管理员账号…"）是全局状态，生产里由 auth/status 拉取后设置。
  // 夹具页不打接口，所以必须**显式清掉**：它是一条 2 行的橙条，会把下面所有元素整体下移
  // 12~36px —— 基线里带着它，任何"改 banner 文案"都会变成全线视觉 diff；
  // 反过来，夹具跑在等接口的状态下又会时有时无（实测出现过两次基线不一致）。
  useAppStore().banner = null
}

/** 延迟灌任务清单（配合 seed({ tasksDeferred: true })） */
function seedTasks(): void {
  useTaskStore().tasks = FIXTURE_TASKS.map(adaptTask)
}

function toApproval(t: TaskDto): ApprovalRequest {
  const d = t.current_decision!
  return {
    id: d.id,
    taskId: t.id,
    kind: d.kind ?? 'approval',
    tool: d.tool_name,
    risk: d.risk,
    summary: d.summary,
    options: d.options,
    createdAt: d.created_at,
  }
}

/**
 * 夹具里 `?project=` 的取值（**只用于夹具自己拼 URL 的场景**）。
 *
 * 生产侧的约定只有一条：链接是 `{ project: 路径 }`、由 vue-router 编一次码，
 * 页面从 `route.query.project` 拿回**原样路径**（见 `src/app/router.ts` 的
 * newTaskProjectOf）。夹具的 Shell 不经过路由 props，只能自己从 `location.search`
 * 取 —— 所以这里**显式解一次**，把"浏览器编码"这一层还原掉。
 *
 * ⚠️ 必须用 `location.search` 原样切、**不能**用 `URLSearchParams.get()`：
 * 后者会先解码一次，再交给 `decodeURIComponent` 就变成解两次
 * （`D%253A` → `D%3A` → `D:` —— 看上去对，但任何真含 `%` 的路径都会坏）。
 * 2026-10-24 实测踩过这一轮。
 */
function rawProjectParam(): string {
  const raw = location.search
  const i = raw.indexOf('project=')
  if (i < 0) return ''
  return raw.slice(i + 'project='.length).split('&')[0]
}

function projectParam(): string {
  const raw = rawProjectParam()
  if (!raw) return ''
  try {
    return decodeURIComponent(raw)
  } catch {
    return raw
  }
}

const Shell = defineComponent({
  setup() {
    /**
     * 当前是夹具的哪一屏。
     *
     * ⚠️ **必须在渲染函数里读**（不是 setup 里算一次）：点击详情页那枚入口会**就地**把
     * 路径换成生产形态的 `/tasks/new?project=…`，而 SPA 复用同一个 Shell 实例 ——
     * 在 setup 里读一次就会永远停在 session 那支：URL 已经是新建页、界面却还是详情页，
     * 而且因为 session 分支要 `route.params.id`，页面只剩一句「加载中…」。
     * 2026-10-24 实测踩过（点了"新建任务"看起来没反应）。
     */
    const currentMode = (): 'session' | 'approval' | 'dashboard' | 'agents' | 'settings' | 'newtask' | 'sessionpage' => {
      const raw = new URLSearchParams(location.search).get('mode')
      // 整页会话详情走独立路径 `/__e2e/detail/<id>/sessionpage`：路径本身就是模式判据
      if (location.pathname.endsWith('/sessionpage')) return 'sessionpage'
      // ⚠️ `/tasks/new` 这条必须排在 `/__e2e/detail` 的**查询参数判据之前**：
      // 点击详情页的入口后 SPA 复用同一个 Shell 实例，`?project=…` 旁边**没有** `mode=`
      // （生产链接就是这样的）。若先判 `mode`，`raw === null` 会一路掉到默认的 'session'，
      // 于是 URL 已是新建页、界面还停在会话壳（并立刻按"最近项目"重置了预选）。
      // 2026-10-24 实测踩过：点了入口，项目变成了 pieqi/web 而不是 pieqi。
      if (location.pathname === '/tasks/new') return 'newtask'
      // ⚠️ `raw` 可能是 **null**（直接打开 `/__e2e/detail`）。`null === 'session'` 不成立，
      // 于是默认分支必须显式兜住 —— 早先写的是 `mode == null || mode === 'session'`，
      // 改成"查询参数里的字符串"之后，不带参数的入口就掉进了"既不是 session 也不是别的"
      // 的空壳（375 宽实测：页面只剩底栏，一行错都没有）。
      if (raw === 'session') return 'session'
      if (raw === 'approval') return 'approval'
      if (raw === 'dashboard') return 'dashboard'
      if (raw === 'agents') return 'agents'
      if (raw === 'settings') return 'settings'
      if (raw === 'newtask') return 'newtask'
      if (raw === 'sessionpage') return 'sessionpage'
      return 'session'
    }
    const decisions = FIXTURE_TASKS.filter((t) => t.current_decision)

    /**
     * 模型清单：在 setup 里取一次 Pinia store，**在渲染函数里**把它翻译成 props。
     *
     * 为什么渲染函数里读（而不是 setup 里取一次固化）：beforeEnter 已经把清单拉好了，
     * 但 agents / settings 等场景的目录是挂载后才落地的；渲染函数每次执行时读 store，
     * 状态一变就跟着变，不会拍到"清单还没到"的中间态。
     */
    const agentStore = useAgentStore()
    const modelsLoadedForAgent = (a: string) => !!agentStore.modelsLoaded[a]

    return () => {
      // ⚠️ 根元素 `h-full` 而**不是** `h-dvh`：高度由 AppLayout（App.vue 里那层）的滚动容器给。
      // 自己占满整屏会把底栏挤到视口之外（底栏不参与滚动 ⇒ 输入区被它挡住），
      // 真实页面就是这么排的。
      //
      // ⚠️ 夹具**不再**自己包 AppLayout：App.vue 已经给每一个页面套了一层，
      // 再包一层就是两条底栏、主区被压两次（375 宽实测直接溢出）。
      // 早先夹具自搭壳时没有底栏，"最新消息会不会被底栏挡住"根本量不到 ——
      // 现在壳就是生产那个，底栏/侧栏的尺寸一并是真的。
      const m = currentMode()
      const agentOf = () => (m === 'newtask' ? agentStore.defaultAgent : LONG_.agent ?? agentStore.defaultAgent)
      return h('div', { class: 'flex h-full w-full min-w-0 overflow-hidden bg-canvas', 'data-e2e': 'shell' }, [
        h(
          'main',
          { class: 'flex min-h-0 min-w-0 flex-1 flex-col overflow-y-auto', 'data-e2e': 'main' },
          m === 'newtask'
            ? [
                // 新建任务页：项目选择器 + 执行 Agent + 输入条。
                //
                // 它用 useRouter()（创建成功后 router.push 去会话页），所以必须在
                // **有路由的环境**里挂 —— 夹具页的独立路由表正好提供这一点。
                // 依赖：taskStore.loaded（判空）、agentStore.defaultAgent（默认 agent）。
                // 前者在 seed() 里显式置 true，后者由 beforeEnter 拉目录后填充。
                //
                // `project` 查询参数 = 上游带来的预选项目（生产里由 SessionPage 的
                // 「在「x」新建任务」入口经 `/tasks/new?project=…` 给到，路由以 props 传入）。
                // 夹具必须**同名同义**地喂进去，否则"从详情页新建默认落当前项目"这条
                // 根本不会被验证到。
                h('div', { 'data-e2e': 'newtask' }, [
                  // 取值口径与生产一致（见 app/router.ts 的 newTaskProjectOf）：
                  // 页面最终拿到的是**原样路径**，这里只解掉"浏览器编码"那一层
                  //（夹具 Shell 不经过路由 props，只能自己从 location 取，见 projectParam）。
                  h(NewTaskPage, { initialProjectPath: projectParam() }),
                ]),
              ]
            : m === 'sessionpage'
            ? [
                // 任务 ID 用 LONG_：它已在 seed() 里经 syncFromTask 进过 store，
                // SessionPage 的"本地是否已有权威事件流"判据因此成立（不再拉一次详情）。
                //
                // **直接挂**（不经过 RouterView）：SessionPage 只需要 `route.params.id`，
                // 而夹具路由没有 `:id`。早先试过在 Shell 内部再套一层 RouterView 并把 id 当 prop 传，
                // 那会渲染**另一个**页面实例（SessionTimeline 又出现一次），页面结构就不是生产那份了。
                // 改用**路由这一层**提供 id：同一条 `/__e2e/detail` 再注册一条带 `:id` 的变体，
                // 由 beforeEach 把 `?mode=sessionpage` 改写成 `<路径>/<LONG_.id>/sessionpage`（见下面）。
                h('div', { class: 'h-full', 'data-e2e': 'sessionpage' }, [h(SessionPage)]),
              ]
            : m === 'settings'
            ? [
                // 设置页：四组全局偏好。它依赖 getSettings() —— 那个请求回来之前
                // 所有开关都是 disabled（代码里刻意的：不给"点了没反应"的控件）。
                // 所以夹具在 beforeEnter 里等它落地（见 route 的注释），否则截图会
                // 拍到一个"全部灰掉"的页面，看起来正常、实际一行设置都没验证到。
                h('div', { 'data-e2e': 'settings' }, [h(SettingsPage)]),
              ]
            : m === 'agents'
            ? [
                // Agents 页：能力矩阵的落点。
                //
                // 它依赖 agentStore.catalog，而目录是**异步**加载的（loadCatalog 打
                // GET /api/agents）。夹具页这里显式拉一次并等它落地，避免截到"还没加载"
                // 的空态 —— 那种截图看起来"页面正常"，实际什么都没验证。
                h('div', { 'data-e2e': 'agents' }, [h(AgentsPage)]),
              ]
            : m === 'dashboard'
            ? [
                // Dashboard 是首页，也是全产品唯一的"行动面"（SPEC §6.2）。
                // 挂真实页面而不是重搭一份：它的两个区块（待审批 / 本周概览）的选择性渲染
                // —— 有审批才出现待审批块、有历史才出现概览 —— 正是最需要被基线守住的东西。
                // 依赖：approvalStore.pending 由 taskStore.tasks 派生（夹具里带 decision
                // 的 waiting_input 任务即可），insight 由 computeWeeklyInsight 从同一份
                // tasks 算出 —— 所以这里不需要额外喂 store。
                h('div', { 'data-e2e': 'dashboard' }, [h(DashboardPage)]),
              ]
            : m === 'session'
            ? [
                // 必填 props 必须给真值：缺失只会是一条 Vue warning，
                // 但"警告被当成噪声忽略"恰恰会让夹具与生产行为悄悄分叉
                h(SessionTimeline, {
                  taskId: LONG_.id,
                  // 生产里由 SessionPage 经 useForceScroll 提供；e2e 场景不需要强制滚动
                  consumeForceScroll: () => false,
                }),
                // 干预输入区：生产里由 SessionPage 在时间线**下方**渲染（flex 列的最后一项）。
                // 不挂它，"主行动按钮在不在视口内"这条断言就没有被量的对象 ——
                // 少了它等于把最重要的一条验收静默跳过（探针第一次跑就是栽在这里）。
                h(InterveneInput, {
                  canCancel: false,
                  // waiting_input 允许发送（与 SessionPage 的 canSendPrompt || !!decision 同义）
                  canSend: true,
                  // ★ 模型清单必须**从 store 透传**，与 SessionPage 同一口径。
                  // 早先这里写死 `models: []` + `modelsLoaded: true` —— 那是「该 Agent 不提供
                  // 可选模型」的形态，不是用户看到的形态（用户选过模型后触发器带长名字，
                  // 宽度差 50px 以上）。用硬编码的空数组，任何"这一行放不放得下"
                  // 的回归都被夹具自己先抹掉了。见下方 beforeEnter 里的 loadModels。
                  //
                  // ⚠️ modelsOf 是 Pinia 的 getter（`modelsOf(state)` 返回取值函数），
                  // 必须在 setup 作用域里取一次缓存，**不能塞进模板字符串当响应式源**。
                  models: agentStore.modelsOf(agentOf()),
                  modelsLoaded: modelsLoadedForAgent(agentOf()),
                  currentModel: '',
                  /**
                   * ★ 必须接 `onSend`，否则"点发送"在夹具里**静默无效**。
                   *
                   * InterveneInput 的 onSend 只做 `emit('send', text)` —— 真正发请求的是
                   * 它的父组件（生产里是 SessionPage 的 onSendPrompt → api.intervene）。
                   * 夹具页不接这个事件，"点发送后断言请求已发出"就永远等不到请求，
                   * 而表现只是"超时"，看不出是夹具缺了一环。实测踩过。
                   *
                   * 这里复刻 SessionPage 的最小语义（发 append_prompt），不复制它的
                   * 全部逻辑（通知/清空/防重都在组件里）：
                   * 交互用例要验证的是"点击 → 请求"这条链，而不是 SessionPage 的编排。
                   */
                  onSend: (text: string) => {
                    void intervene(LONG_.id, { kind: 'append_prompt', text })
                  },
                }),
              ]
            : [
                h('div', { class: 'mx-auto w-full max-w-[840px] space-y-3 overflow-y-auto p-4', 'data-e2e': 'approval-list' },
                  decisions.map((t) => h(ApprovalCard, { approval: toApproval(t), key: t.id })),
                ),
              ],
        ),
        // 「变更反馈」右栏：生产里默认收起为 46px 贴边条（SPEC §5.2），这里挂同一组件。
        // mode="dock" 与 SessionPage 宽屏时一致；canRewind 取"任务非运行中"，
        // 与 SessionPage 的 canRewind 同判据（否则回退按钮在夹具里恒为 disabled，
        // 视觉基线会固化一个生产里不存在的状态）。
        // min-w-0 必须给：FeedbackPanel 收起态固定 46px 贴边条，不加 min-w-0 时
        // 它的 min-content 会把 flex 行撑宽（375 视口下实测 +11px 溢出）。
        h('aside', { class: 'hidden min-w-0 shrink-0 md:flex', 'data-e2e': 'feedback-slot' }, [
          h(FeedbackPanel, { taskId: LONG_.id, mode: 'dock', canRewind: true }),
        ]),
      ])
    }
  },
})

/**
 * 夹具路由表里共用的**数据就绪**逻辑（`/__e2e/detail` 与 `/tasks/new` 都用）。
 *
 * 为什么必须在挂载前跑：store 里先有数据，首次渲染就是"有数据"的状态，
 * 不会先闪一帧空态（那会让视觉基线随机多/少一个 0 状态断言）。
 */
async function primeStores(to: { query: Record<string, unknown> }): Promise<void> {
  const mode = to.query.mode as string | undefined
  // "设置先到、项目清单后到"这条时序（`?tasksDelay=<ms>`）：任务清单推迟灌，
  // 而 agent 目录 / 设置照常先落地 —— 用它复现生产里两请求乱序的那一侧。
  // 夹具是同步的（seed() 直接写 store），所以延迟只能用 setTimeout 表达；
  // 用例侧则用 mock 的 `_delay=/api/settings:<ms>` 造出"设置晚到"。
  const tasksDelay = Number(to.query.tasksDelay || 0)
  seed({ tasksDeferred: tasksDelay > 0 })
  if (tasksDelay > 0) setTimeout(seedTasks, tasksDelay)

  // agents 场景：目录是异步拉的（GET /api/agents），**必须在挂载前拉完**。
  // 否则会截到"暂无可用 Agent"的空态 —— 那种图看起来页面正常，
  // 实际一个能力矩阵都没验证到（典型的静默假绿）。
  // agents 场景要目录；newtask / sessionpage 也要（默认执行 Agent、以及会话的模型清单）。
  // ⚠️ 会话/审批场景**不能**用 loadCatalog(true)：那是 force 语义，会把 store 换一份新对象；
  // 这里只需要"有目录"，走默认的幂等分支。
  if (mode === 'agents' || mode === 'newtask') {
    await useAgentStore().loadCatalog(true)
  }
  // 会话场景：生产里是 **SessionPage** 在任务到达后 `loadModels(task.agent)`，
  // 再经 InterveneInput 把清单交给模型选择器。夹具页直接挂的是 InterveneInput，
  // 没有这步 ⇒ `models=[]` + `modelsLoaded=true`，触发器只剩 105px 的
  // 「Agent 默认」、框下还会多一行「该 Agent 不提供可选模型」的说明。
  // 那不是用户的形态：真实用户一旦选过模型，触发器是「◈ DeepSeek-V4.1-Flash」
  // （约 158px），而**宽度**恰恰是这一行最容易出事的地方。这里按生产同序补齐。
  if (mode == null || mode === 'session' || mode === 'approval' || mode === 'sessionpage') {
    const agentStore = useAgentStore()
    try {
      await agentStore.loadCatalog()
      await agentStore.loadModels(LONG_.agent || agentStore.defaultAgent)
    } catch {
      // 拉不到就保持空清单：组件会走它自己的降级分支（那是真实行为）
    }
  }
  // settings 场景同理：getSettings() 没回来之前开关全是 disabled。
  // 这里先把它预热进 HTTP 缓存不可行（要的是组件状态），所以改成"等它成功一次"：
  // 组件挂载时还会再发一次，但那时网络已经在途/已缓存，且我们的 ready 选择器
  // 等的是"开关可交互"而不是"页面出现"。
  if (mode === 'settings') {
    try {
      await getSettings()
    } catch {
      // 失败就让页面自己走它的降级路径（开关 disabled）——
      // 那是真实行为，用例会在断言里红，而不是被夹具吞掉
    }
  }
  // newtask / sessionpage：**不能**预先拉设置。
  //
  // 新建任务页读 `GET /api/settings` 的默认工作目录，会话页（预选项目那条路）不读。
  // 若在 beforeEnter 就替页面拉一次，mock 的 `_delay=/api/settings:<ms>` 会被
  // 白白消耗在"没人用的那次请求"上，页面自己那次反而瞬间返回 ——
  // 「设置晚到」的用例于是就测不到了（而且看起来还是绿的）。
  // 这两条时序由页面自己发请求，夹具不抢跑。
}
/**
 * 夹具页的**独立路由表**。
 *
 * 为什么不 `router.addRoute()` 到主路由上：
 *   · 主路由表里是 `/:pathMatch(.*)*` 兜底重定向到 /dashboard。任何时序上的不巧
 *     （注册晚一轮、模块实例不一致、HMR 重载）都会让 `/__e2e/detail` **静默**变成 dashboard。
 *     这条 bug 已在本仓库真实发生过，花了好几轮才定位。
 *   · 独立路由表 = 只认 `/__e2e/detail`，没有兜底可掉。掉不进去就渲染不出东西、
 *     ready 元素等不到、用例立刻红 —— **失败是响亮的**。
 *
 */
export const fixtureRouter = createRouter({
  history: createWebHistory(),
  routes: [
    /**
     * **整页会话详情**的专用入口：`/__e2e/detail/<id>/sessionpage`。
     *
     * 必须是**独立的路由记录**而不是"在 Shell 里再套一层 RouterView"：
     * 后者会渲染出第二个 Shell（时间线、头、输入条都出现两遍），量出来的结构已经不是生产那份。
     * `?mode=sessionpage` 由 beforeEach 改写成这条路径（见上）。
     * id 走路由参数 ⇒ SessionPage 的 `route.params.id` 拿到真值，与生产 /sessions/:id 同构。
     */
    {
      path: '/__e2e/detail/:id/sessionpage',
      component: Shell,
      beforeEnter: async (to) => {
        await primeStores(to)
        return true
      },
    },
    {
      path: '/__e2e/detail',
      component: Shell,
      // beforeEnter 在组件挂载前跑：store 里先有数据，首次渲染就是"有数据"的状态，
      // 不会先闪一帧空态（那会让视觉基线随机多/少一个 0 状态断言）
      beforeEnter: async (to) => {
        await primeStores(to)
        return true
      },
    },
    /**
     * 「新建任务」的落地页：与 `src/app/router.ts` 的 `/tasks/new` **同一形状**
     * （同组件、同 `project` 查询参数 → `initialProjectPath`）。
     *
     * 为什么夹具不能省掉这一条：Task 1 的判据是"**点详情页那枚入口，新建页的项目
     * 已经是当前任务的**"。入口是 `<RouterLink :to="/tasks/new?project=…">`，
     * 没有这条路由，点击会停在原地（夹具路由表**没有兜底**，这正是它的设计），
     * 表现是"点了没反应"—— 与生产里路由漏注册的故障长得一模一样，会把人带偏。
     * 有了它，"点 → 导航 → 新页面的项目预选"这条链才在夹具里是真的。
     */
    {
      path: '/tasks/new',
      component: Shell,
      beforeEnter: async (to) => {
        // 换页**不重新 seed**：任务/agent 都已在首个路由里就绪，再 seed 一次会把
        // "从详情页带过来的上下文"冲掉（也会让测「设置晚到」的那条用例多一次请求）。
        // 但**首次直接打开**它（用例直达 `/tasks/new?project=…`，不经详情页）时必须
        // 先把 store 备好 —— 判据是"还没初始化过"，而不是"从哪一页来"。
        if (!useTaskStore().loaded) await primeStores(to)
        return true
      },
    },
    // 详情页的"删除后回仪表盘"靠它落到一个能渲染的页面
    { path: '/dashboard', component: Shell, beforeEnter: () => true },
  ],
})

/**
 * `?project=<绝对路径>` 归一化为 `<path>?project=<encodeURIComponent(路径)>`
 * —— 与生产 `src/app/router.ts` 的 `/tasks/new` 完全一致（那边也是路由用
 * `encodeURIComponent` 生成链接、再用 `route.query.project` 原样传给组件）。
 *
 * 为什么夹具要自己归一化：夹具 Shell 的 div 包在 RouterView 外面，路由级 props
 * 传不到它；于是 `initialProjectPath` 由 Shell 从 `location` 读 `project` 查询参数
 * （见 newtask 分支）。若不归一化，直接手动打开
 * `/tasks/new?project=D:\code\go\pieqi` 时浏览器只会把 `\` 当字符，
 * Shell 读到的值与页面的 `route.query` 形态就分叉了 —— 夹具于是不再 equals 生产。
 *
 * ⚠️ 必须用 `fixtureRouter.beforeEach(...)` 注册，不能写成 `createRouter({ beforeEach })`：
 * 后者不是 RouterOptions 的字段，运行期会被静默忽略、类型检查才报错。它被忽略时
 * `/__e2e/detail?mode=sessionpage` 不会改写成带 id 的路径，SessionPage 拿不到
 * `route.params.id`，页面停在"任务不存在"—— 而且**一条错都不报**。
 */
fixtureRouter.beforeEach((to) => {
  const raw = location.search
  // `/__fixture?to=<路径>` 服务端 302 到 `<路径>`（见 mock-api 的同名分支）。
  // 为什么要有它：夹具路由表**故意没有** `/tasks/new` 这样的"真实页面路径"
  // （见上面的说明），可"从详情页点入口 → 落到新建页"这条链必须**真的导航**才测得出来。
  // mock 的 `to=` 查询参数会被浏览器 url 编码成一个值，起点是 `/` —— 在这里还原成
  // 夹具页要的 `mode=` 查询参数。`to` 里可能带 `&`，所以整段取出、不再按 & 拆。
  if (location.pathname === '/__fixture') {
    const i = raw.indexOf('to=')
    if (i >= 0) return `/__e2e/detail${raw.slice(i + 2)}`
    return '/__e2e/detail'
  }
  // 直接打开（或 e2e 入口的兜底）根路径：落到夹具页
  if (location.pathname === '/') return `/__e2e/detail${raw}`
  // `?mode=sessionpage` → 带 id 的独立路由（见路由表里那条的说明），
  // 这样 SessionPage 的 `route.params.id` 才是真值。
  // ⚠️ 正在去那条路由的路上（pathname 已经是 `/__e2e/detail/<id>/sessionpage`）时**不要**再改写，
  // 否则每一轮都重定向一次、永远到不了（实测：页面停在"任务不存在"，请求打到了
  // 一个不存在的 id）。
  const onSessionPage = /^\/__e2e\/detail\/[^/]+\/sessionpage$/.test(location.pathname)
  if (!onSessionPage && location.pathname === '/__e2e/detail' && /(^|&)mode=sessionpage(&|$)/.test(raw.slice(1))) {
    // 去掉 mode=sessionpage 这个 URLSearchParams（保留其余参数，如 project）
    const rest = new URLSearchParams(raw)
    rest.delete('mode')
    const qs = rest.toString()
    return `/__e2e/detail/${LONG_.id}/sessionpage${qs ? `?${qs}` : ''}`
  }
  const params = new URLSearchParams(raw)
  if (!params.has('project')) return true
  // ⚠️ 必须保留其余查询参数（例如 `mode=newtask`）。早先这里用字符串拼出
  // `${to.path}?project=…`，会把 `mode` 一起丢掉 —— 直接打开
  // `/__e2e/detail?mode=newtask&project=…` 会被改写成缺失 mode 的地址，
  // 页面掉回默认的 session 模式（表现为「来自当前任务」徽标不出现）。
  // URLSearchParams 负责一次解码 + 一次编码，正好等于生产路由的规范化口径。
  const v = params.get('project') ?? ''
  params.set('project', v)
  const qs = params.toString()
  // 已经是规范形态 → 放行，避免无限重定向
  if (raw.slice(1) === qs) return true
  return `${to.path}?${qs}`
})

/**
 * 留给"主路由表"的兼容入口。`src/main.ts` 应当直接安装 `fixtureRouter`；
 * 这个函数只在需要把夹具页挂进既有 app 时使用（当前没有调用方，保留是为了不让
 * 接口"只存在于某一份 main.ts 里"）。
 */
export function installFixtureRoutes(router: Router): void {
  if (!import.meta.env.DEV) return
  for (const r of fixtureRouter.getRoutes()) {
    router.addRoute(r)
  }
}
