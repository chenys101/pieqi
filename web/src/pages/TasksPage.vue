<script setup lang="ts">
// Tasks 页（方案 §16）：状态 Tab + 项目过滤 + 任务列表。
import { computed, ref, onMounted } from 'vue'
import { useTaskStore } from '@/stores/task'
import { useAppStore } from '@/stores/app'
import { TaskFilterBar } from '@/features/task'
import TaskCard from '@/components/task/TaskCard.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Spinner from '@/components/ui/Spinner.vue'
import type { TaskStatus } from '@/types/task'
import { timeAgo } from '@/utils/date'

const taskStore = useTaskStore()
const appStore = useAppStore()

const status = ref<TaskStatus | 'all'>('all')
const project = ref('')

onMounted(() => void taskStore.loadTasks())

/** 过滤：状态 + 项目路径归一匹配（groupKey，方案 §9） */
const filtered = computed(() =>
  taskStore.tasks
    .filter((t) => (status.value === 'all' ? true : t.status === status.value))
    .filter((t) => (project.value ? t.projectPath === project.value : true))
    .sort((a, b) => new Date(b.updatedAt || b.createdAt).getTime() - new Date(a.updatedAt || a.createdAt).getTime()),
)

const emptyHint = computed(() =>
  taskStore.loading ? '加载中…' : status.value === 'all' ? '还没有任务 — 新建一个开始' : '该状态下暂无任务',
)
</script>

<template>
  <div class="flex h-full flex-col">
    <div class="mx-auto w-full max-w-4xl space-y-3 px-4 pt-4 md:px-6">
      <div class="flex items-center justify-between">
        <h1 class="text-base font-semibold">任务</h1>
        <button
          class="rounded-lg bg-accent px-3 py-1.5 text-xs font-medium text-white transition-opacity hover:opacity-90"
          @click="appStore.newTaskOpen = true"
        >
          ＋ 新建
        </button>
      </div>
      <TaskFilterBar v-model:status="status" v-model:project="project" :projects="taskStore.recentProjects" />
    </div>

    <div class="min-h-0 flex-1 overflow-y-auto">
      <div class="mx-auto max-w-4xl px-4 py-3 md:px-6">
        <div v-if="taskStore.loading && !taskStore.tasks.length" class="flex justify-center py-12">
          <Spinner class="h-6 w-6 text-muted" />
        </div>
        <div v-else-if="taskStore.error" class="rounded-lg border border-error/40 bg-error/10 p-4 text-sm text-error">
          加载失败：{{ taskStore.error }}
        </div>
        <div v-else-if="filtered.length" class="grid gap-2.5 md:grid-cols-2">
          <TaskCard v-for="t in filtered" :key="t.id" :task="t" />
        </div>
        <EmptyState v-else :title="emptyHint" :hint="`${taskStore.tasks.length} 个任务 · 最近更新 ${timeAgo(new Date().toISOString())}前`" />
      </div>
    </div>
  </div>
</template>
