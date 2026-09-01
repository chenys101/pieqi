<script setup lang="ts">
// 审批卡片（方案 §20）：集中审批（手机免进会话直接操作）
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import Button from '@/components/ui/Button.vue'
import { useApprovalStore } from '@/stores/approval'
import { useTaskStore } from '@/stores/task'
import type { ApprovalRequest } from '@/types/approval'
import { timeAgo } from '@/utils/date'

const props = defineProps<{ approval: ApprovalRequest }>()
const approvalStore = useApprovalStore()
const taskStore = useTaskStore()
const router = useRouter()

const loading = ref<'approve' | 'deny' | null>(null)
const task = computed(() => taskStore.byId(props.approval.taskId))

async function act(kind: 'approve' | 'deny') {
  loading.value = kind
  try {
    if (kind === 'approve') await approvalStore.approve(props.approval.taskId)
    else await approvalStore.deny(props.approval.taskId)
  } finally {
    loading.value = null
  }
}
</script>

<template>
  <div class="rounded-lg border border-warning/40 bg-surface p-4">
    <div class="flex items-start justify-between gap-2">
      <div class="text-sm font-medium text-warning">🔐 需要授权</div>
      <span class="text-xs text-muted">{{ timeAgo(approval.createdAt) }}前</span>
    </div>
    <div class="mt-1.5 text-xs text-muted">
      {{ task?.title ?? '' }}
      <span v-if="task"> · {{ task.project }}</span>
    </div>
    <div class="mt-2 break-all rounded border border-border/60 bg-background px-2.5 py-2 font-mono text-xs">
      {{ approval.tool ? `${approval.tool}: ` : '' }}{{ approval.summary }}
    </div>
    <div class="mt-3 flex gap-2">
      <Button variant="primary" size="sm" :loading="loading === 'approve'" :disabled="!!loading" @click="act('approve')">
        允许一次
      </Button>
      <Button variant="danger" size="sm" :loading="loading === 'deny'" :disabled="!!loading" @click="act('deny')">
        拒绝
      </Button>
      <Button
        variant="ghost"
        size="sm"
        class="ml-auto"
        @click="router.push(`/sessions/${approval.taskId}`)"
      >
        查看会话
      </Button>
    </div>
  </div>
</template>
