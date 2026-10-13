<script setup lang="ts">
// 用户消息：气泡本体。
//
// 对齐与"下方的时:分"都由调用方 TimelineEventView 负责（那里是 flex-col +
// items-end，时间戳跟着气泡一起靠右）。这里只留气泡样式，**不要**自己写
// w-full / mx-auto 之类的外层定位 —— 两边都写会让气泡被挤成内容宽度、
// 时间戳跟着错位。
defineProps<{ text: string }>()
</script>

<template>
  <div class="max-w-[85%] whitespace-pre-wrap break-words rounded-xl rounded-br-sm bg-accent/15 px-3.5 py-2 text-sm" data-testid="user-message">
    {{ text }}
  </div>
</template>
