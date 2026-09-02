<script setup lang="ts">
// Dashboard（方案 §15）：状态统计 + 运行中 Agent + 需关注列表。
import { computed, onMounted } from 'vue'
import { useTaskStore } from '@/stores/task'
import { useApprovalStore } from '@/stores/approval'
import { StatCard } from '@/features/dashboard'
import TaskCard from '@/components/task/TaskCard.vue'
import EmptyState from '@/components/ui/EmptyState.vue'

const taskStore = useTaskStore()
const approvalStore = useApprovalStore()

onMounted(() => {
  // 首屏兜底（providers 已拉过，重进页面时刷新）
  if (!taskStore.tasks.length && !taskStore.loading) void taskStore.loadTasks()
})

const stats = computed(() => [
  { label: '运行中', value: taskStore.counts.running + taskStore.counts.pending, tone: 'success' as const },
  { label: '需决策', value: taskStore.counts.waiting_input, tone: 'warning' as const },
  { label: '已完成', value: taskStore.counts.completed, tone: 'neutral' as const },
  { label: '失败', value: taskStore.counts.failed, tone: 'error' as const },
])
</script>

<template>
  <div class="h-full overflow-y-auto">
    <div class="mx-auto max-w-4xl space-y-5 px-4 py-5 md:px-6">
      <!-- 状态统计 -->
      <div class="grid grid-cols-2 gap-3 md:grid-cols-4">
        <StatCard v-for="s in stats" :key="s.label" v-bind="s" />
      </div>

      <!-- 运行中 -->
      <section>
        <h2 class="mb-2 text-sm font-semibold text-text">🟢 运行中</h2>
        <div v-if="taskStore.runningTasks.length" class="grid gap-2.5 md:grid-cols-2">
          <TaskCard v-for="t in taskStore.runningTasks" :key="t.id" :task="t" />
        </div>
        <EmptyState v-else title="当前没有运行中的任务" hint="点击「新建任务」开始" />
      </section>

      <!-- 需要关注：待决策 + 失败 -->
      <section v-if="taskStore.needsAttention.length">
        <h2 class="mb-2 text-sm font-semibold text-text">
          ⚠️ 需要关注
          <span v-if="approvalStore.pending.length" class="ml-1 text-warning">（{{ approvalStore.pending.length }} 条待审批）</span>
        </h2>
        <div class="grid gap-2.5 md:grid-cols-2">
          <TaskCard v-for="t in taskStore.needsAttention" :key="t.id" :task="t" />
        </div>
      </section>
    </div>
  </div>
</template>
