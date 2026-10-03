<script setup lang="ts">
// 决策横幅（方案 §20）：approval → 批准/拒绝；choice（已废弃）→ 提示文本回复
// P1：approval 卡可展开「前瞻性 Diff」——批准前看到将发生什么（p1-design.md §4）。
import { computed, ref } from 'vue'
import Button from '@/components/ui/Button.vue'
import { ApprovalDiffCard } from '@/features/feedback'
// 直接引组件文件而非 feature 出口：barrel 会把同目录的 ApprovalCard（含 router/store 依赖）
// 一起拖进会话页的 chunk —— 这里只需要摘要块。
import ApprovalSummary from '@/features/approval/components/ApprovalSummary.vue'
import type { ApprovalRequest } from '@/types/approval'

const props = defineProps<{ decision: ApprovalRequest; loading?: boolean }>()
const emit = defineEmits<{ approve: []; approveSession: []; deny: [] }>()

/** 前瞻性 Diff 展开/收起（不展开不请求） */
const showDiff = ref(false)

/**
 * 「同类免审」只在后端声明支持时出现 —— 动作集是后端的能力声明，不是前端的选项。
 * hook 路径（claude PreToolUse）的 Decision.options 不带 approve_session，
 * 那儿批准的含义只有"这一次"，多给一个按钮就是承诺一个后端不会兑现的行为。
 */
const canApproveSession = computed(() => props.decision.options.includes('approve_session'))
</script>

<template>
  <div class="rounded-lg border border-warning/40 bg-warning/10 px-3.5 py-3">
    <template v-if="decision.kind === 'approval'">
      <div class="text-sm font-medium text-warning">⚠ 需决策：{{ decision.tool }}</div>
      <ApprovalSummary :text="decision.summary" />

      <!-- P1：展开前瞻性 Diff（决策前看将发生什么） -->
      <ApprovalDiffCard v-if="showDiff" :task-id="decision.taskId" :decision-id="decision.id" />

      <div class="mt-3 flex flex-wrap gap-2">
        <Button variant="primary" size="sm" :loading="loading" @click="emit('approve')">✓ 批准</Button>
        <Button
          v-if="canApproveSession"
          variant="secondary"
          size="sm"
          :disabled="loading"
          @click="emit('approveSession')"
        >
          批准·同类免审
        </Button>
        <Button variant="danger" size="sm" :disabled="loading" @click="emit('deny')">✗ 拒绝</Button>
        <Button variant="ghost" size="sm" :disabled="loading" @click="showDiff = !showDiff">
          {{ showDiff ? '收起 Diff' : '查看 Diff' }}
        </Button>
      </div>
    </template>
    <template v-else>
      <div class="text-sm font-medium text-warning">❓ {{ decision.summary || '请选择' }}</div>
      <div class="mt-1 text-xs text-muted">模型已列出方案，请在下方输入框直接回复你的选择。</div>
    </template>
  </div>
</template>
