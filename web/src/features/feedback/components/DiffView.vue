<script setup lang="ts">
// DiffView：单文件 unified diff 的懒加载与渲染（p0-design.md §5.2）。
// 懒加载：仅在展开时才请求 GET /feedback/diff；结果缓存在组件内。
// 渲染逻辑抽取到 DiffLines.vue（前瞻性 ApprovalDiffCard 共用）。
import { ref, watch } from 'vue'
import { getFeedbackDiff } from '@/services/api/feedback'
import type { FeedbackDiffDto } from '@/types/api'
import Spinner from '@/components/ui/Spinner.vue'
import DiffLines from './DiffLines.vue'

const props = defineProps<{
  taskId: string
  path: string
  /** Turn 号（>0 = 单 Turn diff；0/undefined = Baseline 累计） */
  turn?: number
}>()

const state = ref<'idle' | 'loading' | 'done' | 'error'>('idle')
const result = ref<FeedbackDiffDto | null>(null)
const errorMsg = ref('')

/** 展开时懒加载一次；path/turn 变化后重新加载 */
async function load() {
  if (state.value === 'loading') return
  state.value = 'loading'
  try {
    result.value = await getFeedbackDiff(props.taskId, props.path, props.turn)
    state.value = 'done'
  } catch (err) {
    errorMsg.value = err instanceof Error ? err.message : '加载失败'
    state.value = 'error'
  }
}

watch(
  () => [props.taskId, props.path, props.turn],
  () => {
    state.value = 'idle'
    result.value = null
  },
)

/** 展开状态由父组件通过 v-if 控制（展开即 mount → 立即加载） */
if (props.taskId && props.path) load()
</script>

<template>
  <div class="text-xs">
    <div v-if="state === 'loading'" class="flex items-center gap-2 px-3 py-2 text-muted">
      <Spinner class="h-3 w-3" /> 加载 diff…
    </div>
    <div v-else-if="state === 'error'" class="px-3 py-2 text-error">{{ errorMsg }}</div>
    <DiffLines
      v-else-if="result"
      :diff="result.diff"
      :truncated="result.truncated"
      :binary="result.binary"
    />
  </div>
</template>
