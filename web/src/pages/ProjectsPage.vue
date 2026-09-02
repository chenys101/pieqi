<script setup lang="ts">
// Projects 页（方案 §24）：第一版只做 项目 / 路径 / 运行任务 / 活跃会话。
// 知识库等预留入口，不提前实现。
import { computed } from 'vue'
import { useTaskStore } from '@/stores/task'
import EmptyState from '@/components/ui/EmptyState.vue'
import Badge from '@/components/ui/Badge.vue'
import { timeAgo } from '@/utils/date'

const taskStore = useTaskStore()

const projects = computed(() =>
  taskStore.groupsByProject.map((g) => ({
    key: g.key,
    name: g.projectId || g.projectPath,
    path: g.projectPath,
    running: g.counts.running + g.counts.pending,
    waiting: g.counts.waiting_input,
    total: g.tasks.length,
    lastActive: g.tasks[0]?.updatedAt || g.tasks[0]?.createdAt,
  })),
)
</script>

<template>
  <div class="h-full overflow-y-auto">
    <div class="mx-auto max-w-3xl px-4 py-5 md:px-6">
      <h1 class="mb-3 text-base font-semibold">项目</h1>

      <div v-if="projects.length" class="flex flex-col gap-3">
        <RouterLink
          v-for="p in projects"
          :key="p.key"
          to="/tasks"
          class="block rounded-lg border border-border bg-surface p-4 transition-colors hover:border-accent/50"
        >
          <div class="flex items-center justify-between gap-2">
            <span class="min-w-0 truncate text-sm font-medium">{{ p.name }}</span>
            <div class="flex shrink-0 items-center gap-1.5">
              <Badge v-if="p.running" tone="success">运行中 {{ p.running }}</Badge>
              <Badge v-if="p.waiting" tone="warning">待决策 {{ p.waiting }}</Badge>
              <Badge tone="neutral">{{ p.total }} 任务</Badge>
            </div>
          </div>
          <div class="mt-1.5 flex items-center gap-2 text-xs text-muted">
            <span class="truncate font-mono" :title="p.path">{{ p.path }}</span>
            <span v-if="p.lastActive" class="shrink-0">· {{ timeAgo(p.lastActive) }}前活跃</span>
          </div>
        </RouterLink>
      </div>
      <EmptyState v-else title="还没有项目" hint="创建第一个任务后，其项目会出现在这里" />
    </div>
  </div>
</template>
