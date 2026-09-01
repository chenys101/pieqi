<script setup lang="ts">
// 统计卡片：数值 + 标签 + 语义色
defineProps<{ label: string; value: number | string; tone: 'success' | 'warning' | 'error' | 'info' | 'neutral' }>()

const toneText = {
  success: 'text-success',
  warning: 'text-warning',
  error: 'text-error',
  info: 'text-info',
  neutral: 'text-text',
} as const
</script>

<template>
  <div class="rounded-lg border border-border bg-surface px-4 py-3">
    <div class="text-xs text-muted">{{ label }}</div>
    <div class="mt-1 text-2xl font-semibold tabular-nums" :class="toneText[tone]">{{ value }}</div>
  </div>
</template>
