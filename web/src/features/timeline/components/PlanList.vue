<script setup lang="ts">
// PlanList：ACP / Cursor / todo_write 计划条目清单。
// 时间线 PlanCard、会话 sticky、todo 语义卡展开共用。
// UI 对齐 dsh TodoPanel：默认折叠、分状态进度、aria-expanded。
import { computed, ref, watch } from 'vue'
import type { PlanEntryDto } from '@/types/api'

const props = withDefaults(
  defineProps<{
    entries: PlanEntryDto[]
    /** 折叠态标题；空则用分状态进度 */
    headline?: string
    /** 受控折叠；不传则组件内自管，默认折叠（对齐 dsh） */
    folded?: boolean
  }>(),
  { headline: '' },
)

const emit = defineEmits<{ 'update:folded': [value: boolean] }>()

const localFolded = ref(props.folded ?? true)
watch(
  () => props.folded,
  (v) => {
    if (v !== undefined) localFolded.value = v
  },
)

const done = computed(() => props.entries.filter((e) => e.status === 'completed').length)
const active = computed(() => props.entries.filter((e) => e.status === 'in_progress').length)
const pending = computed(() => props.entries.length - done.value - active.value)

/** 分状态进度：「N 完成 · M 进行中 · K 待办」，零段省略（对齐 dsh progressLabel） */
const progress = computed(() => {
  const parts: string[] = []
  if (done.value > 0) parts.push(`${done.value} 完成`)
  if (active.value > 0) parts.push(`${active.value} 进行中`)
  if (pending.value > 0) parts.push(`${pending.value} 待办`)
  return parts.join(' · ') || (props.entries.length ? `${props.entries.length} 项` : '计划')
})

const title = computed(() => props.headline || progress.value)

const STATUS_MARK: Record<string, string> = {
  pending: '○',
  in_progress: '◉',
  completed: '●',
}
const STATUS_CLS: Record<string, string> = {
  pending: 'text-muted',
  in_progress: 'text-warning',
  completed: 'text-success',
}

function toggle() {
  const next = !localFolded.value
  localFolded.value = next
  emit('update:folded', next)
}
</script>

<template>
  <div
    class="rounded-lg border border-border/60 bg-surface/60"
    data-testid="plan-list"
  >
    <button
      type="button"
      class="flex w-full items-center gap-2 px-3 py-2 text-left text-xs hover:text-text"
      :aria-expanded="!localFolded"
      @click="toggle"
    >
      <span class="text-warning" aria-hidden="true">▤</span>
      <span class="shrink-0 font-semibold text-warning">计划</span>
      <span class="min-w-0 flex-1 truncate text-text">{{ title }}</span>
      <span
        class="ml-auto shrink-0 text-muted transition-transform"
        :class="localFolded ? '-rotate-90' : ''"
        aria-hidden="true"
      >▾</span>
    </button>
    <ul v-show="!localFolded" class="border-t border-border/40 px-3 py-2">
      <li
        v-for="(e, i) in entries"
        :key="i"
        class="flex gap-2 py-1 text-xs"
        :data-status="e.status"
      >
        <span
          class="mt-px shrink-0"
          :class="STATUS_CLS[e.status] || 'text-muted'"
          aria-hidden="true"
        >{{ STATUS_MARK[e.status] || '○' }}</span>
        <span
          class="min-w-0 flex-1 break-words"
          :class="e.status === 'completed' ? 'text-muted line-through' : 'text-text'"
        >{{ e.content || '（无描述）' }}</span>
        <span
          v-if="e.priority === 'high'"
          class="shrink-0 text-[10px] uppercase text-error"
        >高</span>
      </li>
      <li v-if="!entries.length" class="text-xs text-muted">（空计划）</li>
    </ul>
  </div>
</template>
