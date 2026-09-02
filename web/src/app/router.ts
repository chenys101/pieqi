// 路由（方案 §26/§53）：懒加载页面；/ → Dashboard；
// 兼容 V1 的 /session/<id> 旧链接（301 语义重定向到 /sessions/:id）。

import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'

const routes: RouteRecordRaw[] = [
  { path: '/', redirect: '/dashboard' },
  // V1 旧链接兼容：飞书历史消息里的 /session/<id>
  { path: '/session/:id', redirect: (to) => `/sessions/${to.params.id}` },
  { path: '/dashboard', component: () => import('@/pages/DashboardPage.vue') },
  { path: '/tasks', component: () => import('@/pages/TasksPage.vue') },
  // Task 详情与 Session 是同一页面（Task 即会话的载体）
  { path: '/tasks/:id', redirect: (to) => `/sessions/${to.params.id}` },
  { path: '/sessions/:id', component: () => import('@/pages/SessionPage.vue') },
  { path: '/agents', component: () => import('@/pages/AgentsPage.vue') },
  { path: '/approvals', component: () => import('@/pages/ApprovalsPage.vue') },
  { path: '/projects', component: () => import('@/pages/ProjectsPage.vue') },
  { path: '/settings', component: () => import('@/pages/SettingsPage.vue') },
  // 兜底：未知路径回首页
  { path: '/:pathMatch(.*)*', redirect: '/dashboard' },
]

export default createRouter({
  history: createWebHistory(),
  routes,
})
