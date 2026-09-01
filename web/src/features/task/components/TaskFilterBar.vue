<script setup lang="ts">
// 任务过滤栏（方案 §16）：状态 Tab + 项目过滤
import { computed } from 'vue'
import type { TaskStatus } from '@/types/task'

const props = defineProps<{
  status: TaskStatus | 'all'
  project: string
  projects: { projectId: string; projectPath: string }[]
}>()
const emit = defineEmits<{ 'update:status': [v: TaskStatus | 'all']; 'update:project': [v: string] }>()

const tabs: { value: TaskStatus | 'all'; label: string }[] = [
  { value: 'all', label: '全部' },
  { value: 'running', label: '运行中' },
  { value: 'waiting_input', label: '待决策' },
  { value: 'completed', label: '已完成' },
  { value: 'failed', label: '失败' },
]

const status = computed({
  get: () => props.status,
  set: (v) => emit('update:status', v),
})
const project = computed({
  get: () => props.project,
  set: (v) => emit('update:project', v),
})
</script>

<template>
  <div class="flex flex-wrap items-center gap-2">
    <div class="flex gap-1 rounded-lg border border-border bg-surface p-1">
      <button
        v-for="tab in tabs"
        :key="tab.value"
        class="rounded-md px-3 py-1 text-xs font-medium transition-colors"
        :class="status === tab.value ? 'bg-accent text-white' : 'text-muted hover:text-text'"
        @click="status = tab.value"
      >
        {{ tab.label }}
      </button>
    </div>
    <select
      v-model="project"
      class="rounded-lg border border-border bg-surface px-2.5 py-1.5 text-xs outline-none focus:border-accent/60"
    >
      <option value="">全部项目</option>
      <option v-for="p in projects" :key="p.projectPath" :value="p.projectPath">
        {{ p.projectId || p.projectPath }}
      </option>
    </select>
  </div>
</template>
