<script setup lang="ts">
// Approvals 页（方案 §23）：集中展示全部待审批，手机免进会话直接操作。
import { useApprovalStore } from '@/stores/approval'
import { ApprovalCard } from '@/features/approval'
import EmptyState from '@/components/ui/EmptyState.vue'

const approvalStore = useApprovalStore()
</script>

<template>
  <div class="h-full overflow-y-auto">
    <div class="mx-auto max-w-2xl px-4 py-5 md:px-6">
      <h1 class="mb-1 text-base font-semibold">审批</h1>
      <p class="mb-3 text-xs text-muted">全部待决策请求 — 无需进入会话即可处理</p>

      <div v-if="approvalStore.pending.length" class="flex flex-col gap-3">
        <ApprovalCard v-for="a in approvalStore.pending" :key="a.id" :approval="a" />
      </div>
      <EmptyState v-else title="没有待审批请求 🎉" hint="Agent 请求权限时会出现在这里" />
    </div>
  </div>
</template>
