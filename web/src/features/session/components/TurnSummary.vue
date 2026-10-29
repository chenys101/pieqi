<script setup lang="ts">
// TurnSummary：详情页每轮输出末尾的**唯一文件入口**。
//
// 为什么不再在这里列文件：文件清单已经在反馈面板（本轮 / 累计）里，
// 时间线再列一遍就是两份数据两个入口（用户 2026-10 明确要求精简）。
// 这里只保留"这一轮改了多少" + 一个跳到反馈面板对应轮次的按钮。
//
// 数字来自 TurnInfo.summary（与 Turn 头、反馈面板同源，AC-R3-02），不另算。
import type { ChangeSummaryDto } from '@/types/api'

defineProps<{
  turn: number
  summary: ChangeSummaryDto
  /** 该轮是否已结束（决定文案；运行中的轮次也会出现，数字是当下的） */
  done: boolean
}>()

const emit = defineEmits<{ focus: [turn: number] }>()
</script>

<template>
  <div
    class="flex items-center gap-2 rounded-lg border border-border bg-surface px-2.5 py-2 shadow-sm"
    data-testid="turn-summary"
  >
    <span
      class="grid h-4 w-4 shrink-0 place-items-center rounded-full text-[9px] font-bold"
      :class="done ? 'bg-success/10 text-success' : 'bg-accent/10 text-accent'"
      aria-hidden="true"
    >{{ done ? '✓' : '·' }}</span>
    <span class="shrink-0 text-[11px] font-semibold text-text-secondary">
      {{ done ? '本轮完成' : '本轮进行中' }}
    </span>
    <span class="min-w-0 truncate text-[10.5px] text-text-tertiary">
      <b class="font-mono text-[10.5px] text-text">{{ summary.files }}</b> 个文件
      <span class="ml-1 font-mono text-success">+{{ summary.additions }}</span>
      <span class="ml-0.5 font-mono text-error">-{{ summary.deletions }}</span>
    </span>
    <button
      type="button"
      class="ml-auto inline-flex shrink-0 items-center gap-1 rounded-md border border-accent/35 bg-accent/5 px-2 py-1 text-[10.5px] text-accent transition-colors hover:bg-accent/10 hover:border-accent/55"
      :title="`在变更反馈里定位到 Turn #${turn}`"
      data-testid="turn-summary-link"
      @click="emit('focus', turn)"
    >
      查看本轮变更
      <svg viewBox="0 0 24 24" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M5 12h14" /><path d="M13 6l6 6-6 6" />
      </svg>
    </button>
  </div>
</template>
