<template>
  <!-- 「思考中」占位徽章：呼吸灯 + 点渐变（提交后等待首次流式输出） -->
  <div class="event-enter flex items-center gap-1.5 px-1 py-2 text-xs text-muted">
    <span class="thinking-dot h-1.5 w-1.5 rounded-full bg-info"></span>
    <span>思考中</span>
    <span class="flex gap-0.5">
      <span class="thinking-dot h-1 w-1 rounded-full bg-info"></span>
      <span class="thinking-dot h-1 w-1 rounded-full bg-info"></span>
      <span class="thinking-dot h-1 w-1 rounded-full bg-info"></span>
    </span>
  </div>
</template>
