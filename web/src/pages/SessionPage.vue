<script setup lang="ts">
// Session 页（方案 §17）：V2 核心页面 —— Header / 审批横幅 / Timeline / 干预输入。
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useTaskStore } from '@/stores/task'
import { useSession } from '@/composables/useSession'
import { SessionHeader, SessionTimeline, ApprovalBanner, InterveneInput } from '@/features/session'
import { FeedbackPanel } from '@/features/feedback'
import EmptyState from '@/components/ui/EmptyState.vue'

const route = useRoute()
const router = useRouter()
const taskStore = useTaskStore()

const taskId = computed(() => route.params.id as string)
const { task, canCancel, canSendPrompt, submitPrompt, cancel, approve, deny, consumeForceScroll } = useSession(taskId)

/** 决策横幅：waiting_input 且带 decision 时展示 */
const decision = computed(() => (task.value?.status === 'waiting_input' ? task.value.decision : undefined))
const approvalBusy = ref(false)
/** 冷启动探测完成（详情已尝试拉取），仍无任务 → 视为不存在 */
const probed = ref(false)

/** 变更反馈面板（Feedback P0）：Agent 执行中禁止回退（静止边界） */
const feedbackOpen = ref(false)
const canRewind = computed(() => !!task.value && task.value.status !== 'running' && task.value.status !== 'pending')

// 冷启动兜底：WS 快照未到时按 id 拉详情（含全量 events）
watch(
  taskId,
  (id) => {
    probed.value = false
    if (!id) return
    if (taskStore.byId(id)) {
      probed.value = true
      return
    }
    taskStore
      .refreshTask(id)
      .catch(() => {})
      .finally(() => (probed.value = true))
  },
  { immediate: true },
)

async function onApprove() {
  approvalBusy.value = true
  try {
    await approve()
  } finally {
    approvalBusy.value = false
  }
}

async function onDeny() {
  approvalBusy.value = true
  try {
    await deny()
  } finally {
    approvalBusy.value = false
  }
}

async function onRemove() {
  if (!task.value) return
  if (!confirm('确定删除该任务？删除后不可恢复。')) return
  await taskStore.deleteTask(task.value.id)
  router.replace('/tasks')
}
</script>

<template>
  <div v-if="task" class="flex h-full flex-col">
    <SessionHeader :task="task" :can-cancel="canCancel" @cancel="cancel" @remove="onRemove" @feedback="feedbackOpen = true" />
    <SessionTimeline :task-id="task.id" :consume-force-scroll="consumeForceScroll" />

    <!-- 决策横幅：在输入区上方，手机免滚动直接操作（方案 §20） -->
    <div v-if="decision" class="mx-auto w-full max-w-3xl px-3 pb-2 md:px-4">
      <ApprovalBanner :decision="decision" :loading="approvalBusy" @approve="onApprove" @deny="onDeny" />
    </div>

    <InterveneInput
      :can-cancel="canCancel"
      :can-send="canSendPrompt || !!decision"
      @send="submitPrompt"
      @cancel="cancel"
    />

    <!-- 变更反馈面板（Feedback P0）：总览 / Diff / 回退 / 预览 -->
    <FeedbackPanel :task-id="task.id" :open="feedbackOpen" :can-rewind="canRewind" @close="feedbackOpen = false" />
  </div>

  <!-- 任务不存在（已删除 / 链接失效 / 探测中） -->
  <div v-else class="flex h-full items-center justify-center">
    <EmptyState
      :title="probed ? '任务不存在或已删除' : '加载中…'"
      :hint="probed ? '任务可能已被删除，或链接失效' : ''"
    >
      <RouterLink to="/dashboard" class="text-xs text-accent hover:underline">← 返回仪表盘</RouterLink>
    </EmptyState>
  </div>
</template>
