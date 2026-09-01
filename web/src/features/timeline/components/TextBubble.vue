<script setup lang="ts">
// Agent 文本输出气泡
defineProps<{ text: string }>()
</script>

<template>
  <div class="event-enter max-w-full">
    <div class="whitespace-pre-wrap break-words rounded-xl border border-border/60 bg-surface px-3.5 py-2 text-sm leading-relaxed">
      {{ text }}
    </div>
  </div>
</template>
