<script setup lang="ts">
// 用户消息：右对齐气泡
defineProps<{ text: string }>()
</script>

<template>
  <div class="event-enter flex justify-end">
    <div class="max-w-[85%] whitespace-pre-wrap break-words rounded-xl rounded-br-sm bg-accent/15 px-3.5 py-2 text-sm">
      {{ text }}
    </div>
  </div>
</template>
