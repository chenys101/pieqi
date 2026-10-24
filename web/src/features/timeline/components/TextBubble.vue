<script setup lang="ts">
// Agent 文本输出气泡（需求 3：输出结果支持 markdown 渲染）。
//
// 渲染分工全部下沉到 MarkdownBody（含消毒与"是否值得按 markdown 渲染"的判据），
// 这里只管气泡外观 —— 事件流里正文是**增量累积**的，每次 text_delta 都会带着
// 更长的 text 重渲染一次，所以组件必须无状态（不能缓存 token 流，否则末尾块永远要重算）。
//
// 宽度：靠调用方的 flex-col（items-start）做交叉轴摆放，气泡自己 `max-w-full` +
// 内部 `break-words` 就够；**不要**在这里写 flex-1/self-stretch，那会把左对齐的
// 气泡拉满整行、与用户气泡的右对齐在视觉上分不出方向。
import MarkdownBody from './MarkdownBody.vue'

// taskId 往下传是给正文里的**本地图片**用的：`![x](D:\code\...\a.png)` 要靠它拼出
// /api/tasks/:id/local-image?path=... （见 MarkdownBody / markdownImages.ts）。
// 可空：旧任务兜底（直接用 task.output 拼一个气泡）那条路径早期没有 id 概念。
defineProps<{ text: string; taskId?: string }>()
</script>

<template>
  <div class="event-enter max-w-full" data-testid="text-bubble">
    <div class="break-words rounded-xl border border-border/60 bg-surface px-3.5 py-2 text-sm leading-relaxed">
      <MarkdownBody :text="text" :task-id="taskId" />
    </div>
  </div>
</template>
