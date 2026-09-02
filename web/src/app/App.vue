<script setup lang="ts">
// 应用根组件：一次性 providers 接线 + 全局横幅 / Toast / 新建任务弹窗。
import { setupProviders } from './providers'
import AppLayout from '@/layouts/AppLayout.vue'
import ToastHost from '@/components/ui/ToastHost.vue'
import { NewTaskDialog } from '@/features/task'
import { useAppStore } from '@/stores/app'

setupProviders()

const appStore = useAppStore()

const bannerClass = {
  warning: 'border-warning/40 bg-warning/10 text-warning',
  error: 'border-error/40 bg-error/10 text-error',
  info: 'border-info/40 bg-info/10 text-info',
} as const
</script>

<template>
  <AppLayout>
    <!-- 全局横幅：debug 模式 / 未绑定（方案 §35） -->
    <div
      v-if="appStore.banner"
      class="shrink-0 border-b px-4 py-1.5 text-center text-xs"
      :class="bannerClass[appStore.banner.tone]"
    >
      {{ appStore.banner.message }}
    </div>
    <RouterView />
  </AppLayout>

  <NewTaskDialog :open="appStore.newTaskOpen" @close="appStore.newTaskOpen = false" />
  <ToastHost />
</template>
