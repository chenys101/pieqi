<script setup lang="ts">
// 用户消息：右对齐气泡。
//
// 外层不自己撑满：它现在被 TimelineEventView 放进一个 flex 行里（左边挂时间戳），
// 所以这里只负责气泡本身的样式，对齐由调用方那层决定 —— 两边都写 justify-end
// 会让气泡被挤成内容宽度、时间戳也跟着错位。
defineProps<{ text: string }>()
</script>

<template>
  <div class="max-w-[85%] whitespace-pre-wrap break-words rounded-xl rounded-br-sm bg-accent/15 px-3.5 py-2 text-sm">
    {{ text }}
  </div>
</template>
