<script setup lang="ts">
// 移动端「其他运行中任务」下拉。
//
// 待决策优先、最多 3 个，其余走「查看全部」；排序规则在 ../otherTasks.ts。
// 数据与导航都从父级传入/回调 —— 这个组件不碰 store，也不决定去哪。
import { STATUS_DOT, STATUS_LABELS } from '@/utils/format'
import { timeAgo } from '@/utils/date'
import type { Task } from '@/types/task'

defineProps<{
  tasks: Task[]
  count: number
}>()

const emit = defineEmits<{
  close: []
  select: [id: string]
  all: []
}>()
</script>

<template>
  <div
    class="absolute right-3 top-full z-50 mt-1.5 w-[300px] max-w-[calc(100vw-24px)] overflow-hidden rounded-xl border border-border bg-surface shadow-md"
    data-testid="session-tasks-pop"
    @click.stop
  >
    <div class="flex items-center gap-2 px-3 pb-1.5 pt-2.5">
      <span class="flex-1 text-xs font-semibold">其他运行中任务</span>
      <span class="grid h-4 min-w-4 place-items-center rounded-full bg-elevated px-1 text-[9px] font-bold text-muted">
        {{ count }}
      </span>
      <button
        type="button"
        class="grid h-6 w-6 place-items-center rounded-md text-muted transition-colors hover:bg-elevated hover:text-text"
        aria-label="关闭"
        @click="emit('close')"
      >
        ×
      </button>
    </div>
    <div class="px-3 pb-1.5 text-[9px] text-text-tertiary">待决策优先 · 最多显示 3 个</div>
    <div class="max-h-[60vh] overflow-y-auto px-2 pb-2">
      <button
        v-for="t in tasks"
        :key="t.id"
        type="button"
        class="flex w-full items-center gap-2 rounded-md px-2 py-2 text-left text-xs transition-colors hover:bg-elevated"
        :class="t.status === 'waiting_input' ? 'border border-warning/25 bg-warning/5' : 'border border-transparent'"
        :data-testid="`session-other-task-${t.id}`"
        @click="emit('select', t.id)"
      >
        <span class="block h-1.5 w-1.5 shrink-0 rounded-full" :class="STATUS_DOT[t.status]" />
        <span class="min-w-0 flex-1">
          <span class="block truncate">{{ t.title }}</span>
          <span class="block truncate text-[9px] text-text-tertiary">
            {{ t.project }} · {{ timeAgo(t.updatedAt) }}前
          </span>
        </span>
        <span class="shrink-0 text-[10px] text-text-tertiary">{{ STATUS_LABELS[t.status] }}</span>
        <span class="shrink-0 text-text-tertiary" aria-hidden="true">›</span>
      </button>

      <button
        v-if="count > 3"
        type="button"
        class="mt-1 flex h-9 w-full items-center justify-center rounded-md border border-dashed border-border-strong text-[11px] text-accent transition-colors hover:bg-accent/5"
        data-testid="session-all-tasks"
        @click="emit('all')"
      >
        查看全部 {{ count }} 个运行中任务 →
      </button>
    </div>
  </div>
</template>
