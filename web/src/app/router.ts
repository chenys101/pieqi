// 路由（方案 §26/§53）：懒加载页面；/ → Dashboard；
// 兼容 V1 的 /session/<id> 旧链接（301 语义重定向到 /sessions/:id）。

import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'

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
  { path: '/tasks/new', component: () => import('@/pages/NewTaskPage.vue') },
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
