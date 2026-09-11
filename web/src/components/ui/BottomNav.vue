<script setup lang="ts">
// 移动端底栏（SPEC §6 / §2.1 / D5）。
//
// **判据：底栏每一项都必须是顶层目的地（页面切换）。**
// 不允许某一项触发弹层 —— 同类外观必须给出同类行为（iOS HIG / Material 一致）。
// 原型曾设想第 5 项「项目」+ 底部 Sheet，正因违反这条被否决（SPEC §6.1 记录）。
// 因此「任务」指向**任务浏览器页**（`/tasks`），项目手风琴就在那一页里，
// 不在这里弹出来。
//
// 底栏是**替代**抽屉，不是补充：侧栏在移动端被隐藏，任务浏览能力改由
// 「任务」这一项承接（SPEC §6：先把内容移出，再谈隐藏控件）。
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { useApprovalStore } from '@/stores/approval'
import { useWebSocket } from '@/composables/useWebSocket'

const route = useRoute()
const approvalStore = useApprovalStore()
const { connection } = useWebSocket()

/**
 * 连接异常。
 *
 * SPEC §2.1 规定「连接状态只允许一处」—— 桌面上那处是侧栏底部
 * 「设置 ● 连接正常」。移动端侧栏被隐藏，那一处随之消失，
 * 所以这里挂一个点**不构成第二处**，它就是移动端的唯一一处。
 * 不挂的话，移动端会在断线时完全静默：数据停在那儿，用户看不出是"没动"
 * 还是"断了"—— 这是移除 TopBar 连带丢掉的功能信号，必须补回。
 *
 * `initial` 不算异常（首帧还没连上），否则每次启动都闪一下红点。
 */
const connectionDown = computed(() => connection.value !== 'connected' && connection.value !== 'initial')

/** stroke 图标（与原型 sprite 同源，避免 emoji 在不同系统上字形不一） */
const ITEMS = [
  {
    to: '/dashboard',
    label: '首页',
    d: 'M3 9.5 12 3l9 6.5V20a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1z M9.5 21v-7h5v7',
    /** 高亮归属：审批已在仪表盘里（无独立审批中心），所以只需一个路径 */
    match: ['/dashboard'],
  },
  {
    to: '/tasks',
    label: '任务',
    d: 'M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01',
    /** 任务浏览器页 + 会话详情：都是"任务"这条线上（/projects 已随旧设计删除） */
    match: ['/tasks', '/sessions'],
  },
  {
    to: '/settings',
    label: '设置',
    d: 'M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6z M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.6a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z',
    match: ['/settings'],
  },
] as const

/**
 * 高亮归属：取「最具体的前缀」。
 *
 * 不用 `startsWith` 直接比对一个 to —— 因为一个目的地可能被多条路径指向
 * （如「任务」要同时覆盖 `/tasks` 与会话详情 `/sessions/*`），
 * 而这些路径彼此不是前缀关系，必须显式列出归属。
 */
const activeTo = computed(() => {
  const p = route.path
  let best = ''
  let bestLen = 0
  for (const it of ITEMS) {
    for (const m of it.match) {
      if (p === m || p.startsWith(m + '/')) {
        if (m.length > bestLen) {
          bestLen = m.length
          best = it.to
        }
      }
    }
  }
  return best
})
</script>

<template>
  <nav
    class="flex shrink-0 items-stretch border-t border-border bg-surface"
    style="padding-bottom: env(safe-area-inset-bottom)"
    aria-label="主导航"
  >
    <RouterLink
      v-for="it in ITEMS"
      :key="it.to"
      :to="it.to"
      class="relative flex flex-1 flex-col items-center gap-1 py-2 text-[10.5px] font-medium transition-colors"
      :class="activeTo === it.to ? 'text-accent' : 'text-text-tertiary'"
      :aria-current="activeTo === it.to ? 'page' : undefined"
    >
      <svg
        viewBox="0 0 24 24"
        class="h-5 w-5"
        fill="none"
        stroke="currentColor"
        stroke-width="1.8"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
      >
        <path :d="it.d" />
      </svg>
      {{ it.label }}

      <!-- 角标：点（不是数字）—— 底栏图标上塞计数会挤，而"有事 / 有异常"
           这个二值信号用点已经足够。⚠️ 它不是导航高亮：不改变 active 归属，
           只回答"那一页里有东西在等你"。 -->
      <span
        v-if="it.to === '/dashboard' && approvalStore.pending.length"
        class="absolute right-1/2 top-1 -mr-3 h-1.5 w-1.5 rounded-full bg-warning"
        aria-hidden="true"
      />
      <span
        v-if="it.to === '/settings' && connectionDown"
        class="absolute right-1/2 top-1 -mr-3 h-1.5 w-1.5 rounded-full bg-error"
        aria-hidden="true"
      />
    </RouterLink>
  </nav>
</template>
