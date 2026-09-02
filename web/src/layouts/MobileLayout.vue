<script setup lang="ts">
// 移动布局：TopBar + 抽屉侧边栏。
// 侧边栏与 PC 使用同一组件（AppSidebar），仅默认收起、点汉堡展开。
import { useRoute } from 'vue-router'
import { useWebSocket } from '@/composables/useWebSocket'
import { useSessionStore } from '@/stores/session'
import { useAppStore } from '@/stores/app'
import AppSidebar from './AppSidebar.vue'

const route = useRoute()
const { connection, reconnect } = useWebSocket()
const sessionStore = useSessionStore()
const appStore = useAppStore()

/** 会话页隐藏 TopBar，全屏时间线（返回靠 SessionHeader） */
const isSession = () => route.path.startsWith('/sessions/')
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

    <!-- 抽屉：遮罩 + 共享侧边栏（与 PC 完全一致的结构） -->
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
          <!-- 侧边栏点击导航后自动收起（navigate 事件） -->
          <div class="h-full max-w-[80vw]" @click.stop>
            <AppSidebar @navigate="appStore.mobileNavOpen = false" />
          </div>
        </div>
      </Transition>
    </Teleport>
  </div>
</template>
