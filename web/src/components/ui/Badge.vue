<script setup lang="ts">
// 徽章：tone 映射语义色（状态语义由领域组件决定，此处只管视觉）
const props = withDefaults(defineProps<{ tone?: 'success' | 'warning' | 'error' | 'info' | 'neutral' }>(), {
  tone: 'neutral',
})

const toneClass = {
  success: 'bg-success/10 text-success border-success/30',
  warning: 'bg-warning/10 text-warning border-warning/30',
  error: 'bg-error/10 text-error border-error/30',
  info: 'bg-info/10 text-info border-info/30',
  neutral: 'bg-elevated text-muted border-border',
} as const
</script>

<template>
  <span
    class="inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs font-medium leading-4"
    :class="toneClass[props.tone]"
  >
    <slot />
  </span>
</template>
