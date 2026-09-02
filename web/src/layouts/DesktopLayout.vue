<script setup lang="ts">
// 桌面布局：共享侧边栏（常驻）+ Main。
import AppSidebar from './AppSidebar.vue'
</script>

<template>
  <div class="flex h-dvh overflow-hidden bg-background text-text">
    <AppSidebar />
    <main class="min-w-0 flex-1 overflow-hidden">
      <slot />
    </main>
  </div>
</template>
