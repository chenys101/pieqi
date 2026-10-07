<script setup lang="ts">
// Agent 文本输出气泡（需求 3：输出结果支持 markdown 渲染）。
//
// 渲染分工全部下沉到 MarkdownBody（含消毒与"是否值得按 markdown 渲染"的判据），
// 这里只管气泡外观 —— 事件流里正文是**增量累积**的，每次 text_delta 都会带着
// 更长的 text 重渲染一次，所以组件必须无状态（不能缓存 token 流，否则末尾块永远要重算）。
import MarkdownBody from './MarkdownBody.vue'

defineProps<{ text: string }>()
</script>

<template>
  <div class="event-enter max-w-full">
    <div class="break-words rounded-xl border border-border/60 bg-surface px-3.5 py-2 text-sm leading-relaxed">
      <MarkdownBody :text="text" />
    </div>
  </div>
</template>
