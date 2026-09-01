<script setup lang="ts">
// 桌面布局（方案 §27）：Sidebar + Main。
// Sidebar：品牌 / 导航 / 连接指示 / 新建任务。
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

const nav = [
  { to: '/dashboard', label: '仪表盘' },
  { to: '/tasks', label: '任务' },
  { to: '/approvals', label: '审批' },
  { to: '/agents', label: 'Agents' },
  { to: '/projects', label: '项目' },
  { to: '/settings', label: '设置' },
]

/** 会话页隐藏侧栏，获得全宽时间线（与 SessionHeader 的返回导航配合） */
const isSession = () => route.path.startsWith('/sessions/')
</script>

<template>
  <div class="flex h-dvh overflow-hidden bg-background text-text">
    <aside v-if="!isSession()" class="flex w-56 shrink-0 flex-col border-r border-border bg-surface">
      <div class="flex items-center gap-2 px-4 py-4">
        <span class="text-base font-bold tracking-tight">🥧 Pieqi</span>
      </div>

      <nav class="flex-1 space-y-0.5 px-2">
        <RouterLink
          v-for="item in nav"
          :key="item.to"
          :to="item.to"
          class="flex items-center justify-between rounded-lg px-3 py-2 text-sm transition-colors"
          :class="route.path.startsWith(item.to) ? 'bg-elevated font-medium text-text' : 'text-muted hover:bg-elevated/60 hover:text-text'"
        >
          {{ item.label }}
          <span
            v-if="item.to === '/approvals' && approvalStore.pending.length"
            class="rounded-full bg-warning/20 px-1.5 text-xs tabular-nums text-warning"
          >
            {{ approvalStore.pending.length }}
          </span>
        </RouterLink>
      </nav>

      <div class="space-y-2 border-t border-border px-3 py-3">
        <button
          class="w-full rounded-lg bg-accent px-3 py-2 text-sm font-medium text-white transition-opacity hover:opacity-90"
          @click="appStore.newTaskOpen = true"
        >
          ＋ 新建任务
        </button>
        <!-- 连接指示（方案 §37）：状态色 + 文案，点击手动重连 -->
        <button
          class="flex w-full items-center gap-2 text-xs"
          :class="connection === 'connected' ? 'text-success' : connection === 'initial' ? 'text-muted' : 'text-warning'"
          title="点击重连"
          @click="reconnect()"
        >
          <span class="status-breathe h-1.5 w-1.5 rounded-full bg-current" />
          {{ sessionStore.connectionLabel }}
        </button>
      </div>
    </aside>

    <main class="min-w-0 flex-1 overflow-hidden">
      <slot />
    </main>
  </div>
</template>
