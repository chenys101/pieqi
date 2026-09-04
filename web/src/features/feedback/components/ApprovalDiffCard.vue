<script setup lang="ts">
// ApprovalDiffCard：P1 Approval → Diff（p1-design.md §4）—— 审批前的「前瞻性 Diff」。
// decisionId = tool_use id；Diff 完全由工具入参派生（Edit/Write/Delete/NotebookEdit），
// Bash 等无文件语义的工具 → 后端 404 → 展示「无文件 Diff」提示。
// 展开即懒加载（审批卡高频出现，不展开不请求）。
import { computed, ref, watch } from 'vue'
import { getApprovalDiff } from '@/services/api/feedback'
import { ApiError } from '@/services/api/client'
import type { ApprovalDiffDto } from '@/types/api'
import Spinner from '@/components/ui/Spinner.vue'
import DiffLines from './DiffLines.vue'

const props = defineProps<{ taskId: string; decisionId: string }>()

const state = ref<'idle' | 'loading' | 'done' | 'none' | 'error'>('idle')
const result = ref<ApprovalDiffDto | null>(null)
const errorMsg = ref('')

const opLabel: Record<string, string> = { create: '新建', modify: '修改', delete: '删除', rename: '重命名' }

const header = computed(() => {
  if (!result.value) return ''
  const op = opLabel[result.value.operation] ?? result.value.operation
  return `${op} ${result.value.path}（+${result.value.additions} -${result.value.deletions}）`
})

/** 展开时懒加载一次；decisionId 变化（连续审批）后重新加载 */
async function load() {
  if (state.value === 'loading') return
  state.value = 'loading'
  try {
    result.value = await getApprovalDiff(props.taskId, props.decisionId)
    state.value = 'done'
  } catch (err) {
    // 404 = 工具无文件语义（Bash 等），不算错误，降级为提示
    if (err instanceof ApiError && err.status === 404) {
      state.value = 'none'
    } else {
      errorMsg.value = err instanceof Error ? err.message : '加载失败'
      state.value = 'error'
    }
  }
}

watch(
  () => [props.taskId, props.decisionId],
  () => {
    state.value = 'idle'
    result.value = null
  },
)

if (props.taskId && props.decisionId) load()
</script>

<template>
  <div class="mt-2 rounded-md border border-border/60 bg-background/60">
    <div v-if="state === 'loading'" class="flex items-center gap-2 px-3 py-2 text-xs text-muted">
      <Spinner class="h-3 w-3" /> 计算将发生的变更…
    </div>
    <div v-else-if="state === 'error'" class="px-3 py-2 text-xs text-error">{{ errorMsg }}</div>
    <div v-else-if="state === 'none'" class="px-3 py-2 text-xs text-muted">
      该操作无文件 Diff（如 Shell 命令）
    </div>
    <template v-else-if="result">
      <!-- 前瞻性头：操作 + 路径 + 增删统计 -->
      <div class="border-b border-border/40 px-3 py-1.5 font-mono text-[11px] text-muted">{{ header }}</div>
      <DiffLines :diff="result.diff" :truncated="result.truncated" :binary="result.binary" />
    </template>
  </div>
</template>
