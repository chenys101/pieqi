<script setup lang="ts">
// 移动布局（方案 §27）：TopBar + Content + BottomNav。
// BottomNav：Home / Tasks / Approvals / Agents / More（设置+项目入口）。
import { ref } from 'vue'
import { useRoute } from 'vue-router'
import { useWebSocket } from '@/composables/useWebSocket'
import { useSessionStore } from '@/stores/session'
import { useAppStore } from '@/stores/app'
import { useApprovalStore } from '@/stores/approval'

const route = useRoute()
const { connection, reconnect } = useWebSocket()
const sessionStore = useSessionStore()
const appStore = useAppStore()
const approvalStore = useApprovalStore()
const moreOpen = ref(false)

const tabs = [
  { to: '/dashboard', label: '首页', icon: 'M3 10.5 12 3l9 7.5M5 9.5V21h14V9.5' },
  { to: '/tasks', label: '任务', icon: 'M4 6h16M4 12h16M4 18h10' },
  { to: '/approvals', label: '审批', icon: 'M6 10V7a6 6 0 0 1 12 0v3M5 10h14v10H5z' },
  { to: '/agents', label: 'Agents', icon: 'M8 6a4 4 0 1 1 8 0v3H8zM4 9h16v11H4z' },
]

/** 会话页隐藏 TopBar/BottomNav，全屏时间线 */
const isSession = () => route.path.startsWith('/sessions/')
</script>

<template>
  <div class="flex h-dvh flex-col overflow-hidden bg-background text-text">
    <!-- TopBar：品牌 + 连接状态 -->
    <header v-if="!isSession()" class="flex shrink-0 items-center justify-between border-b border-border bg-surface px-4 py-2.5">
      <span class="text-sm font-bold">🥧 Pieqi</span>
      <button
        class="flex items-center gap-1.5 text-xs"
        :class="connection === 'connected' ? 'text-success' : connection === 'initial' ? 'text-muted' : 'text-warning'"
        @click="reconnect()"
      >
        <span class="status-breathe h-1.5 w-1.5 rounded-full bg-current" />
        {{ sessionStore.connectionLabel }}
      </button>
    </header>

    <main class="min-h-0 flex-1 overflow-hidden">
      <slot />
    </main>

    <template v-if="!isSession()">
      <!-- More 面板：设置 / 项目（BottomNav 放不下的一级入口） -->
      <Transition
        enter-active-class="event-enter"
        leave-active-class="transition-opacity duration-150"
        leave-to-class="opacity-0"
      >
        <div v-if="moreOpen" class="absolute inset-x-0 bottom-16 z-40 mx-3 rounded-xl border border-border bg-elevated p-2 shadow-xl">
          <button class="w-full rounded-lg px-3 py-2.5 text-left text-sm hover:bg-surface" @click="moreOpen = false; $router.push('/projects')">
            📁 项目
          </button>
          <button class="w-full rounded-lg px-3 py-2.5 text-left text-sm hover:bg-surface" @click="moreOpen = false; $router.push('/settings')">
            ⚙ 设置
          </button>
          <button
            class="w-full rounded-lg px-3 py-2.5 text-left text-sm text-accent hover:bg-surface"
            @click="moreOpen = false; appStore.newTaskOpen = true"
          >
            ＋ 新建任务
          </button>
        </div>
      </Transition>

      <!-- BottomNav：5 入口（方案 §27） -->
      <nav class="relative flex shrink-0 items-stretch border-t border-border bg-surface pb-[env(safe-area-inset-bottom)]">
        <RouterLink
          v-for="tab in tabs"
          :key="tab.to"
          :to="tab.to"
          class="relative flex flex-1 flex-col items-center gap-0.5 py-2 text-xs"
          :class="route.path.startsWith(tab.to) ? 'text-accent' : 'text-muted'"
        >
          <svg viewBox="0 0 24 24" class="h-5 w-5" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path :d="tab.icon" />
          </svg>
          {{ tab.label }}
          <span
            v-if="tab.to === '/approvals' && approvalStore.pending.length"
            class="absolute right-1/2 top-1 translate-x-4 rounded-full bg-warning px-1.5 text-[10px] tabular-nums text-background"
          >
            {{ approvalStore.pending.length }}
          </span>
        </RouterLink>
        <button
          class="flex flex-1 flex-col items-center gap-0.5 py-2 text-xs"
          :class="moreOpen ? 'text-accent' : 'text-muted'"
          @click="moreOpen = !moreOpen"
        >
          <svg viewBox="0 0 24 24" class="h-5 w-5" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true">
            <circle cx="5" cy="12" r="1.6" /><circle cx="12" cy="12" r="1.6" /><circle cx="19" cy="12" r="1.6" />
          </svg>
          更多
        </button>
      </nav>
    </template>
  </div>
</template>
