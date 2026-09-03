<script setup lang="ts">
// DiffView：单文件 unified diff 的懒加载与渲染（p0-design.md §5.2）。
// 懒加载：仅在展开时才请求 GET /feedback/diff；结果缓存在组件内。
import { computed, ref, watch } from 'vue'
import { getFeedbackDiff } from '@/services/api/feedback'
import type { FeedbackDiffDto } from '@/types/api'
import Spinner from '@/components/ui/Spinner.vue'

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

/** diff 文本 → 渲染行（按 unified diff 语义着色） */
interface DiffLine {
  kind: 'add' | 'del' | 'hunk' | 'meta' | 'ctx'
  text: string
}
const lines = computed<DiffLine[]>(() => {
  const diff = result.value?.diff ?? ''
  if (!diff) return []
  return diff.split('\n').map((text) => {
    if (text.startsWith('@@')) return { kind: 'hunk', text }
    // +++/--- 与 diff --git/index 头部按元信息弱化
    if (text.startsWith('+++') || text.startsWith('---') || text.startsWith('diff ') || text.startsWith('index ')) {
      return { kind: 'meta', text }
    }
    if (text.startsWith('+')) return { kind: 'add', text }
    if (text.startsWith('-')) return { kind: 'del', text }
    return { kind: 'ctx', text }
  }) as DiffLine[]
})

const lineClass: Record<DiffLine['kind'], string> = {
  add: 'bg-success/10 text-success',
  del: 'bg-error/10 text-error',
  hunk: 'text-accent',
  meta: 'text-muted/70',
  ctx: 'text-muted',
}

/** 展开状态由父组件通过 v-if 控制（展开即 mount → 立即加载） */
if (props.taskId && props.path) load()
</script>

<template>
  <div class="text-xs">
    <div v-if="state === 'loading'" class="flex items-center gap-2 px-3 py-2 text-muted">
      <Spinner class="h-3 w-3" /> 加载 diff…
    </div>
    <div v-else-if="state === 'error'" class="px-3 py-2 text-error">{{ errorMsg }}</div>
    <template v-else-if="result">
      <!-- 二进制文件：不渲染文本 diff -->
      <div v-if="result.binary" class="px-3 py-2 text-muted">二进制文件（不支持文本 diff）</div>
      <div v-else-if="!result.diff.trim()" class="px-3 py-2 text-muted">无差异</div>
      <div v-else class="overflow-x-auto">
        <div
          v-for="(line, i) in lines"
          :key="i"
          class="whitespace-pre font-mono leading-5 px-2"
          :class="lineClass[line.kind]"
        >{{ line.text }}</div>
        <div v-if="result.truncated" class="px-3 py-1 text-muted">… diff 过长已截断</div>
      </div>
    </template>
  </div>
</template>
