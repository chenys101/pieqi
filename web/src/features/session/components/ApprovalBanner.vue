<script setup lang="ts">
// 决策横幅（方案 §20）：approval → 批准/拒绝；choice（已废弃）→ 提示文本回复
import Button from '@/components/ui/Button.vue'
import type { ApprovalRequest } from '@/types/approval'

defineProps<{ decision: ApprovalRequest; loading?: boolean }>()
const emit = defineEmits<{ approve: []; deny: [] }>()
</script>

<template>
  <div class="rounded-lg border border-warning/40 bg-warning/10 px-3.5 py-3">
    <template v-if="decision.kind === 'approval'">
      <div class="text-sm font-medium text-warning">⚠ 需决策：{{ decision.tool }}</div>
      <div class="mt-1 break-all font-mono text-xs text-text/80">{{ decision.summary }}</div>
      <div class="mt-3 flex gap-2">
        <Button variant="primary" size="sm" :loading="loading" @click="emit('approve')">✓ 批准</Button>
        <Button variant="danger" size="sm" :disabled="loading" @click="emit('deny')">✗ 拒绝</Button>
      </div>
    </template>
    <template v-else>
      <div class="text-sm font-medium text-warning">❓ {{ decision.summary || '请选择' }}</div>
      <div class="mt-1 text-xs text-muted">模型已列出方案，请在下方输入框直接回复你的选择。</div>
    </template>
  </div>
</template>
