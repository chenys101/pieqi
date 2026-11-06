<script setup lang="ts">
// 桌面布局：共享侧边栏（常驻）+ Main。
// 侧栏收起后 aside 为 w-0 + pointer-events-none，收起按钮跟着消失 ——
// 展开入口挂在一条 40px 窄轨上（不盖住详情页返回箭头），仪表盘/设置同样可用。
import AppSidebar from './AppSidebar.vue'
import { useSidebarStore } from '@/stores/sidebar'

const sidebar = useSidebarStore()
</script>

<template>
  <div class="flex h-dvh overflow-hidden bg-background text-text">
    <AppSidebar />
    <div
      v-if="sidebar.collapsed"
      class="flex w-10 shrink-0 flex-col items-center border-r border-border bg-surface pt-3"
      data-testid="sidebar-expand-rail"
    >
      <button
        type="button"
        class="grid h-7 w-7 place-items-center rounded-md text-muted transition-colors hover:bg-elevated hover:text-text"
        title="展开侧栏"
        aria-label="展开侧栏"
        data-testid="sidebar-expand"
        @click="sidebar.expand()"
      >
        <svg viewBox="0 0 24 24" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M9 6l6 6-6 6" />
          <path d="M20 4v16" />
        </svg>
      </button>
    </div>
    <main class="min-w-0 flex-1 overflow-hidden">
      <slot />
    </main>
  </div>
</template>
