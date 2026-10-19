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

/** 夹具 DTO → 生产领域模型（与 taskStore.loadTasks 走同一条适配） */
function seed(): void {
  const pinia = createPinia()
  setActivePinia(pinia)

  const taskStore = useTaskStore()
  taskStore.tasks = FIXTURE_TASKS.map(adaptTask)
  // `loaded` 必须显式置 true：新建任务页的判空逻辑等它（区分"还没到"与"确实没有"，
  // 见 NewTaskPage 的 watch）。夹具是**同步**灌数据的，不像生产那样有网络往返，
  // 不设的话页面会一直停在"项目还没到"的观感上。
  taskStore.loaded = true
  useSessionStore().syncFromTask(LONG_)

  // 顶部横幅（"尚未绑定飞书管理员账号…"）是全局状态，生产里由 auth/status 拉取后设置。
  // 夹具页不打接口，所以必须**显式清掉**：它是一条 2 行的橙条，会把下面所有元素整体下移
  // 12~36px —— 基线里带着它，任何"改 banner 文案"都会变成全线视觉 diff；
  // 反过来，夹具跑在等接口的状态下又会时有时无（实测出现过两次基线不一致）。
  useAppStore().banner = null
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

const Shell = defineComponent({
  setup() {
    const raw = new URLSearchParams(location.search).get('mode')
    const mode: 'session' | 'approval' | 'dashboard' | 'agents' | 'settings' | 'newtask' =
      raw === 'approval' ? 'approval'
      : raw === 'dashboard' ? 'dashboard'
      : raw === 'agents' ? 'agents'
      : raw === 'settings' ? 'settings'
      : raw === 'newtask' ? 'newtask'
      : 'session'
    const decisions = FIXTURE_TASKS.filter((t) => t.current_decision)

    return () =>
      // ⚠️ `h-dvh` 会与 AppLayout 里真实存在的移动端底栏（BottomNav）抢底部 49px：
      // 夹具页自己占满整屏，底栏被挤到视口之外且不参与滚动 ⇒ 输入区被它挡住。
      // 根元素用 `h-full`（由 AppLayout 的滚动容器给高度），真实页面就是这么排的。
      h('div', { class: 'flex h-full w-full min-w-0 overflow-hidden bg-canvas', 'data-e2e': 'shell' }, [
        h(
          'main',
          { class: 'flex min-h-0 min-w-0 flex-1 flex-col overflow-y-auto', 'data-e2e': 'main' },
          mode === 'newtask'
            ? [
                // 新建任务页：项目选择器 + 执行 Agent + 输入条。
                //
                // 它用 useRouter()（创建成功后 router.push 去会话页），所以必须在
                // **有路由的环境**里挂 —— 夹具页的独立路由表正好提供这一点。
                // 依赖：taskStore.loaded（判空）、agentStore.defaultAgent（默认 agent）。
                // 前者在 seed() 里显式置 true，后者由 beforeEnter 拉目录后填充。
                h('div', { 'data-e2e': 'newtask' }, [h(NewTaskPage)]),
              ]
            : mode === 'settings'
            ? [
                // 设置页：四组全局偏好。它依赖 getSettings() —— 那个请求回来之前
                // 所有开关都是 disabled（代码里刻意的：不给"点了没反应"的控件）。
                // 所以夹具在 beforeEnter 里等它落地（见 route 的注释），否则截图会
                // 拍到一个"全部灰掉"的页面，看起来正常、实际一行设置都没验证到。
                h('div', { 'data-e2e': 'settings' }, [h(SettingsPage)]),
              ]
            : mode === 'agents'
            ? [
                // Agents 页：能力矩阵的落点。
                //
                // 它依赖 agentStore.catalog，而目录是**异步**加载的（loadCatalog 打
                // GET /api/agents）。夹具页这里显式拉一次并等它落地，避免截到"还没加载"
                // 的空态 —— 那种截图看起来"页面正常"，实际什么都没验证。
                h('div', { 'data-e2e': 'agents' }, [h(AgentsPage)]),
              ]
            : mode === 'dashboard'
            ? [
                // Dashboard 是首页，也是全产品唯一的"行动面"（SPEC §6.2）。
                // 挂真实页面而不是重搭一份：它的两个区块（待审批 / 本周概览）的选择性渲染
                // —— 有审批才出现待审批块、有历史才出现概览 —— 正是最需要被基线守住的东西。
                // 依赖：approvalStore.pending 由 taskStore.tasks 派生（夹具里带 decision
                // 的 waiting_input 任务即可），insight 由 computeWeeklyInsight 从同一份
                // tasks 算出 —— 所以这里不需要额外喂 store。
                h('div', { 'data-e2e': 'dashboard' }, [h(DashboardPage)]),
              ]
            : mode === 'session'
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
                  models: [],
                  modelsLoaded: true,
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
  },
})

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
 * 形态就取 `app/router.ts` 的同构写法；组件仍然是那些生产组件。
 */
export const fixtureRouter = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/__e2e/detail',
      component: Shell,
      // beforeEnter 在组件挂载前跑：store 里先有数据，首次渲染就是"有数据"的状态，
      // 不会先闪一帧空态（那会让视觉基线随机多/少一个 0 状态断言）
      beforeEnter: async (to) => {
        seed()
        // agents 场景：目录是异步拉的（GET /api/agents），**必须在挂载前拉完**。
        // 否则会截到"暂无可用 Agent"的空态 —— 那种图看起来页面正常，
        // 实际一个能力矩阵都没验证到（典型的静默假绿）。
        // agents 场景要目录；newtask 也要（默认执行 Agent 来自目录的 default）。
        // 两者都在挂载前拉完，避免截到"还没加载"的状态。
        if (to.query.mode === 'agents' || to.query.mode === 'newtask') {
          await useAgentStore().loadCatalog(true)
        }
        // settings 场景同理：getSettings() 没回来之前开关全是 disabled。
        // 这里先把它预热进 HTTP 缓存不可行（要的是组件状态），所以改成"等它成功一次"：
        // 组件挂载时还会再发一次，但那时网络已经在途/已缓存，且我们的 ready 选择器
        // 等的是"开关可交互"而不是"页面出现"。
        if (to.query.mode === 'settings') {
          try {
            await getSettings()
          } catch {
            // 失败就让页面自己走它的降级路径（开关 disabled）——
            // 那是真实行为，用例会在断言里红，而不是被夹具吞掉
          }
        }
        return true
      },
    },
  ],
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
