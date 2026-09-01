<script setup lang="ts">
// Session Header：返回 / 标题 / 状态 / 元信息 / 操作
import { useRouter } from 'vue-router'
import StatusBadge from '@/components/task/StatusBadge.vue'
import Button from '@/components/ui/Button.vue'
import type { Task } from '@/types/task'
import { timeAgo } from '@/utils/date'
import { shortId } from '@/utils/format'

defineProps<{ task: Task; canCancel: boolean }>()
const emit = defineEmits<{ cancel: []; remove: [] }>()
const router = useRouter()
</script>

<template>
  <header class="border-b border-border bg-surface/60 px-3 py-2.5 md:px-4">
    <div class="mx-auto flex max-w-3xl items-center gap-2">
      <button
        class="rounded-md px-1.5 py-1 text-muted transition-colors hover:bg-elevated hover:text-text"
        title="返回任务列表"
        aria-label="返回"
        @click="router.push('/tasks')"
      >
        <svg viewBox="0 0 24 24" class="h-4 w-4" aria-hidden="true">
          <path d="M15 18l-6-6 6-6" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
      </button>
      <h1 class="min-w-0 flex-1 truncate text-sm font-semibold" :title="task.prompt">
        {{ task.title }}
      </h1>
      <StatusBadge :status="task.status" />
      <Button v-if="canCancel" variant="ghost" size="sm" @click="emit('cancel')">中止</Button>
      <Button variant="ghost" size="sm" title="删除任务" @click="emit('remove')">删除</Button>
    </div>
    <div class="mx-auto mt-1 flex max-w-3xl items-center gap-2 pl-8 text-xs text-muted">
      <span class="font-mono">#{{ shortId(task.id) }}</span>
      <span class="truncate" :title="task.projectPath">{{ task.project }}</span>
      <span>{{ timeAgo(task.updatedAt || task.createdAt) }}前</span>
      <span v-if="task.error" class="truncate text-error" :title="task.error">{{ task.error }}</span>
    </div>
  </header>
</template>
