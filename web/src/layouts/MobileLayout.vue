<script setup lang="ts">
// 移动布局：TopBar + 左滑抽屉导航（替代底部 Tab，释放常驻底部空间）。
// 空间对比：底部 Tab 常驻 ~56px + 安全区；抽屉收起时占 0。
import { watch } from 'vue'
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
  { to: '/dashboard', label: '仪表盘', icon: 'M3 10.5 12 3l9 7.5M5 9.5V21h14V9.5' },
  { to: '/approvals', label: '审批', icon: 'M6 10V7a6 6 0 0 1 12 0v3M5 10h14v10H5z' },
  { to: '/agents', label: 'Agents', icon: 'M8 6a4 4 0 1 1 8 0v3H8zM4 9h16v11H4z' },
  { to: '/projects', label: '项目', icon: 'M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z' },
  { to: '/settings', label: '设置', icon: 'M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM19.4 15a1.7 1.7 0 0 0 .34 1.87l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.7 1.7 0 0 0-1.87-.34 1.7 1.7 0 0 0-1.03 1.56V21a2 2 0 1 1-4 0v-.09a1.7 1.7 0 0 0-1.11-1.56 1.7 1.7 0 0 0-1.87.34l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.7 1.7 0 0 0 .34-1.87 1.7 1.7 0 0 0-1.56-1.03H3a2 2 0 1 1 0-4h.09a1.7 1.7 0 0 0 1.56-1.11 1.7 1.7 0 0 0-.34-1.87l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.7 1.7 0 0 0 1.87.34h.09a1.7 1.7 0 0 0 1.03-1.56V3a2 2 0 1 1 4 0v.09a1.7 1.7 0 0 0 1.03 1.56 1.7 1.7 0 0 0 1.87-.34l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.7 1.7 0 0 0-.34 1.87v.09a1.7 1.7 0 0 0 1.56 1.03H21a2 2 0 1 1 0 4h-.09a1.7 1.7 0 0 0-1.56 1.03z' },
]

/** 会话页隐藏 TopBar，全屏时间线（返回靠 SessionHeader） */
const isSession = () => route.path.startsWith('/sessions/')

// 路由切换自动收起抽屉
watch(
  () => route.fullPath,
  () => (appStore.mobileNavOpen = false),
)
</script>

<template>
  <div class="flex h-dvh flex-col overflow-hidden bg-background text-text">
    <!-- TopBar：汉堡菜单 + 品牌 + 连接状态 -->
    <header v-if="!isSession()" class="flex shrink-0 items-center justify-between border-b border-border bg-surface px-3 py-2.5">
      <div class="flex items-center gap-2">
        <button
          class="rounded-md p-1.5 text-muted transition-colors hover:bg-elevated hover:text-text"
          aria-label="打开菜单"
          @click="appStore.mobileNavOpen = true"
        >
          <svg viewBox="0 0 24 24" class="h-5 w-5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
            <path d="M4 6h16M4 12h16M4 18h16" />
          </svg>
        </button>
        <span class="text-sm font-bold">🥧 Pieqi</span>
      </div>
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

    <!-- 抽屉导航：遮罩 + 左滑面板 -->
    <Teleport to="body">
      <Transition
        enter-active-class="transition-opacity duration-150"
        enter-from-class="opacity-0"
        leave-active-class="transition-opacity duration-100"
        leave-to-class="opacity-0"
      >
        <div
          v-if="appStore.mobileNavOpen"
          class="fixed inset-0 z-50 bg-black/60 backdrop-blur-sm"
          @click="appStore.mobileNavOpen = false"
        >
          <!-- 面板自身滑动动画；@click.stop 防止点面板内误关 -->
          <div class="event-enter flex h-full w-64 max-w-[80vw] flex-col border-r border-border bg-surface pb-3 pt-4" @click.stop>
            <div class="px-4 pb-3 text-base font-bold tracking-tight">🥧 Pieqi</div>

            <nav class="min-h-0 flex-1 space-y-0.5 overflow-y-auto px-2">
              <RouterLink
                v-for="item in nav"
                :key="item.to"
                :to="item.to"
                class="flex items-center gap-3 rounded-lg px-3 py-2.5 text-sm transition-colors"
                :class="route.path.startsWith(item.to) ? 'bg-elevated font-medium text-text' : 'text-muted hover:bg-elevated/60 hover:text-text'"
              >
                <svg viewBox="0 0 24 24" class="h-5 w-5 shrink-0" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <path :d="item.icon" />
                </svg>
                {{ item.label }}
                <span
                  v-if="item.to === '/approvals' && approvalStore.pending.length"
                  class="ml-auto rounded-full bg-warning/20 px-1.5 text-xs tabular-nums text-warning"
                >
                  {{ approvalStore.pending.length }}
                </span>
              </RouterLink>
            </nav>

            <div class="shrink-0 border-t border-border px-3 pt-3">
              <button
                class="w-full rounded-lg bg-accent px-3 py-2.5 text-sm font-medium text-white transition-opacity hover:opacity-90"
                @click="appStore.mobileNavOpen = false; appStore.newTaskOpen = true"
              >
                ＋ 新建任务
              </button>
            </div>
          </div>
        </div>
      </Transition>
    </Teleport>
  </div>
</template>
