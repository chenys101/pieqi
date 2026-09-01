<script setup lang="ts">
// 任务卡片（Dashboard / Tasks 列表通用）：标题 / 状态 / 项目 / 相对时间
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import StatusBadge from '@/components/task/StatusBadge.vue'
import type { Task } from '@/types/task'
import { timeAgo, duration } from '@/utils/date'

const props = defineProps<{ task: Task }>()
const router = useRouter()

// 运行中展示已运行时长，其余展示最近活跃时间
const timeText = computed(() =>
  props.task.status === 'running' || props.task.status === 'pending'
    ? duration(props.task.startedAt)
    : `${timeAgo(props.task.updatedAt || props.task.createdAt)}前`,
)

function open() {
  router.push(`/sessions/${props.task.id}`)
}
</script>

<template>
  <article
    class="cursor-pointer rounded-lg border border-border bg-surface p-4 transition-colors hover:border-accent/50 hover:bg-elevated"
    role="button"
    tabindex="0"
    @click="open"
    @keydown.enter="open"
  >
    <div class="flex items-start justify-between gap-3">
      <h3 class="line-clamp-2 min-w-0 flex-1 text-sm font-medium" :title="task.prompt">
        {{ task.title }}
      </h3>
      <StatusBadge :status="task.status" />
    </div>
    <div class="mt-2 flex items-center gap-2 text-xs text-muted">
      <span class="truncate">{{ task.project }}</span>
      <span>·</span>
      <span class="shrink-0">{{ timeText }}</span>
    </div>
    <div v-if="task.decision" class="mt-2 truncate rounded border border-warning/40 bg-warning/10 px-2 py-1 text-xs text-warning">
      ⚠ 需决策：{{ task.decision.tool || task.decision.summary }}
    </div>
  </article>
</template>
