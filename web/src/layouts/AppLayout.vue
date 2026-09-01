<script setup lang="ts">
// 响应式布局切换（方案 §27）：≤768px 走 Mobile（TopBar + BottomNav），
// 其余走 Desktop（Sidebar + Main）。
import { useResponsive } from '@/composables/useResponsive'
import DesktopLayout from './DesktopLayout.vue'
import MobileLayout from './MobileLayout.vue'

const { isMobile } = useResponsive()
</script>

<template>
  <MobileLayout v-if="isMobile">
    <slot />
  </MobileLayout>
  <DesktopLayout v-else>
    <slot />
  </DesktopLayout>
</template>
