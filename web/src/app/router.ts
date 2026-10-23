// 路由（方案 §26/§53）：懒加载页面；/ → Dashboard；
// 兼容 V1 的 /session/<id> 旧链接（301 语义重定向到 /sessions/:id）。

import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'

/**
 * 新建任务页的**预选项目**（`?project=`）。
 *
 * 取值口径只有一条：**链接里给的是原样路径**（SessionPage 的 newTaskHref 就是
 * `{ project: task.projectPath }`），vue-router 生成 href 时会自动 `encodeURIComponent`、
 * 解析时自动解码，所以这里拿到的 `route.query.project` **已经是路径本身**。
 *
 * ⚠️ 千万别在这里再 `decodeURIComponent` 一次。踩过（2026-10-24）：以为"链接要编码、
 * 这里要解"，于是两边各编解码一次 —— 绝大多数路径看不出问题（`D:` 解两次还是 `D:`），
 * 但 `route.query` 那头已经解过一次，再解就是把用户路径里的 `%` 当转义符：
 *   · 路径里真带 `%`（如 `C:\work\100%`、`C:\a%2Fb`）会被"解"成另一条路径，
 *   · 更糟的是 `C:\x\100&y` 这种，`URLSearchParams` 解出的是 `100`，后面的 `&y` 被吃掉。
 * 编码/解码只做一次，交给自己更清楚的那一层做（浏览器 ↔ vue-router）。
 */
export function newTaskProjectOf(raw: unknown): string {
  return typeof raw === 'string' ? raw.trim() : ''
}

const routes: RouteRecordRaw[] = [
  { path: '/', redirect: '/dashboard' },
  // V1 旧链接兼容：飞书历史消息里的 /session/<id>
  { path: '/session/:id', redirect: (to) => `/sessions/${to.params.id}` },
  { path: '/dashboard', component: () => import('@/pages/DashboardPage.vue') },
  // 任务浏览器页：**移动端专用**（SPEC §6.1）—— 桌面由侧栏任务树承接。
  // 桌面深链进来会落到窄屏版式：不是不能用，但那是一处"本不该出现的地方"，
  // 所以直接送回仪表盘。断点与 useResponsive 的 media query 保持一致（768px）。
  {
    path: '/tasks',
    component: () => import('@/pages/TasksPage.vue'),
    beforeEnter: () => (window.matchMedia('(max-width: 768px)').matches ? true : '/dashboard'),
  },
  // 新建任务页：复用详情页骨架，仅多项目选择（放 /tasks/:id 前避免被吞）
  //
  // `?project=<绝对路径>` = **从任务详情页带着当前任务的项目进来**（用户 2026-10-24 要求：
  // 详情页点「新建任务」应默认落在当前任务的项目里）。它只是个**预选建议**，
  // NewTaskPage 会按"用户已主动决定"对待（不再被默认目录 / 最近项目覆盖），
  // 用户仍可在页内改成别的项目。缺参时行为与从前完全一致。
  //
  // 取值约定见 newTaskProjectOf 的注释（`%` 开头的先按 encodeURIComponent 解一次）。
  {
    path: '/tasks/new',
    component: () => import('@/pages/NewTaskPage.vue'),
    props: (route) => ({ initialProjectPath: newTaskProjectOf(route.query.project) }),
  },
  // Task 详情与 Session 是同一页面（Task 即会话的载体）
  { path: '/tasks/:id', redirect: (to) => `/sessions/${to.params.id}` },
  { path: '/sessions/:id', component: () => import('@/pages/SessionPage.vue') },
  { path: '/agents', component: () => import('@/pages/AgentsPage.vue') },
  // 没有 /approvals：SPEC §2.1 已定「无独立审批中心」。
  // 旧链接（飞书历史消息 / 浏览器书签）仍要能落到一个有意义的地方，
  // 所以是**重定向到仪表盘**（待审批组就在那里），不是 404 ——
  // 删掉入口不等于把已经在路上的链接打断。
  { path: '/approvals', redirect: '/dashboard' },
  { path: '/settings', component: () => import('@/pages/SettingsPage.vue') },
  // 兜底：未知路径回首页
  { path: '/:pathMatch(.*)*', redirect: '/dashboard' },
]

export default createRouter({
  history: createWebHistory(),
  routes,
})
