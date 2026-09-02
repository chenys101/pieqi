<script setup lang="ts">
// 空状态占位
defineProps<{ title: string; hint?: string }>()
</script>

<template>
  <div class="flex flex-col items-center justify-center gap-1 py-12 text-center">
    <div class="text-sm font-medium text-muted">{{ title }}</div>
    <div v-if="hint" class="text-xs text-muted/70">{{ hint }}</div>
    <slot />
  </div>
</template>
