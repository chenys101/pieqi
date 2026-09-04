<script setup lang="ts">
// OutcomeCard：P1 Task Outcome（p1-design.md §6）—— Task 结构化结果，手机端主验收面。
// 完成度（completed/partial/failed）由后端规则派生；未终态也实时派生供中途验收。
import { computed, ref, watch } from 'vue'
import { getOutcome } from '@/services/api/feedback'
import type { OutcomeStatusDto, TaskOutcomeDto } from '@/types/api'
import Spinner from '@/components/ui/Spinner.vue'
import { timeAgo } from '@/utils/date'

const props = defineProps<{ taskId: string }>()

const outcome = ref<TaskOutcomeDto | null>(null)
const loading = ref(false)
/** 折叠态只显示完成度 + 摘要；展开看 issues / rewinds 明细 */
const expanded = ref(false)

const statusMeta: Record<OutcomeStatusDto, { label: string; cls: string; icon: string }> = {
  completed: { label: '已完成', cls: 'text-success border-success/40', icon: '✓' },
  partial: { label: '部分完成', cls: 'text-warning border-warning/40', icon: '△' },
  failed: { label: '失败', cls: 'text-error border-error/40', icon: '✗' },
}

const meta = computed(() => statusMeta[outcome.value?.status ?? 'completed'])

/** checks 汇总：通过 n / 失败 m */
const checkCounts = computed(() => {
  const c = { ok: 0, fail: 0, other: 0 }
  for (const ck of outcome.value?.checks ?? []) {
    if (ck.status === 'success') c.ok++
    else if (ck.status === 'failed') c.fail++
    else c.other++
  }
  return c
})

async function refresh() {
  loading.value = true
  try {
    outcome.value = await getOutcome(props.taskId)
  } catch {
    /* 静默：面板内已有错误提示通道 */
  } finally {
    loading.value = false
  }
}

watch(
  () => props.taskId,
  () => refresh(),
  { immediate: true },
)
</script>

<template>
  <div class="rounded-lg border border-border/60 bg-surface/60 px-3 py-2.5">
    <div v-if="loading && !outcome" class="flex items-center gap-2 py-1 text-xs text-muted">
      <Spinner class="h-3 w-3" /> 加载结果…
    </div>

    <template v-else-if="outcome">
      <!-- 摘要行：完成度徽章 + 变更/checks 统计（点击展开明细） -->
      <button class="flex w-full flex-wrap items-center gap-2 text-left text-xs" @click="expanded = !expanded">
        <span class="rounded border px-1.5 py-0.5 font-semibold" :class="meta.cls">{{ meta.icon }} {{ meta.label }}</span>
        <span class="text-muted">
          {{ outcome.changes.files }} 个文件
          <span class="font-mono">
            <span class="text-success">+{{ outcome.changes.additions }}</span>
            <span class="ml-1 text-error">-{{ outcome.changes.deletions }}</span>
          </span>
        </span>
        <span v-if="outcome.checks.length" class="text-muted">
          检查 <span class="text-success">{{ checkCounts.ok }}✓</span>
          <span v-if="checkCounts.fail" class="ml-1 text-error">{{ checkCounts.fail }}✗</span>
        </span>
        <span v-if="outcome.rewinds.length" class="text-muted">回退 {{ outcome.rewinds.length }} 次</span>
        <span class="ml-auto text-[10px] text-muted" :title="outcome.generated_at">{{ timeAgo(outcome.generated_at) }}前</span>
      </button>

      <!-- 展开明细：issues + rewinds 审计 -->
      <div v-if="expanded" class="mt-2 border-t border-border/40 pt-2">
        <div v-if="!outcome.issues.length && !outcome.rewinds.length" class="text-xs text-muted">无问题记录</div>
        <ul v-else class="flex flex-col gap-1">
          <li v-for="(issue, i) in outcome.issues" :key="'i' + i" class="text-xs text-error">{{ issue }}</li>
          <li v-for="(rw, i) in outcome.rewinds" :key="'r' + i" class="text-xs text-muted">
            回退至 Turn #{{ rw.to_turn }} 之前（恢复 {{ rw.restored.length }} 个文件，{{ timeAgo(rw.at) }}前）
          </li>
        </ul>
      </div>
    </template>
  </div>
</template>
