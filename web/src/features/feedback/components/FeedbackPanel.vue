<script setup lang="ts">
// FeedbackPanel：反馈总览面板（p0-design.md §5.1，手机端一屏拉齐）。
// 结构：累计统计 + Preview 控制 + Turn 卡片列表（最新在前，展开看文件 diff）。
// 数据流：打开/刷新时拉 GET /feedback 现场派生（后端不存第二份聚合，ADR-0001）。
import { computed, ref, watch } from 'vue'
import { getFeedback, rewindToTurn } from '@/services/api/feedback'
import type { FeedbackBundleDto } from '@/types/api'
import Modal from '@/components/ui/Modal.vue'
import Spinner from '@/components/ui/Spinner.vue'
import Button from '@/components/ui/Button.vue'
import TurnCard from './TurnCard.vue'
import PreviewSection from './PreviewSection.vue'
import { useNotificationStore } from '@/stores/notification'

const props = defineProps<{
  taskId: string
  open: boolean
  /** Agent 执行中禁止回退（静止边界原则）；面板仍可查看 */
  canRewind: boolean
}>()

const emit = defineEmits<{ close: [] }>()

const notify = useNotificationStore()
const bundle = ref<FeedbackBundleDto | null>(null)
const loading = ref(false)
const rewinding = ref<number | null>(null)

/** 快照 Turn 集合（O(1) 查询） */
const checkpointSet = computed(() => new Set(bundle.value?.checkpoints ?? []))
/** Turn 列表倒序：最新一轮排最前（反馈场景先看最近改动） */
const turnsDesc = computed(() => [...(bundle.value?.turns ?? [])].reverse())

async function refresh() {
  if (!props.taskId) return
  loading.value = true
  try {
    bundle.value = await getFeedback(props.taskId)
  } catch (err) {
    notify.error(err instanceof Error ? err.message : '加载反馈数据失败')
  } finally {
    loading.value = false
  }
}

/** 回退到 Turn N 之前：成功后刷新（时间线由 WS task_updated 自动更新） */
async function onRewind(turn: number) {
  rewinding.value = turn
  try {
    const res = await rewindToTurn(props.taskId, turn)
    notify.success(`已回退到 Turn #${turn} 之前（恢复 ${res.restored.length} 个文件）`)
    await refresh()
  } catch (err) {
    notify.error(err instanceof Error ? err.message : '回退失败')
  } finally {
    rewinding.value = null
  }
}

// 打开时拉取；taskId 变化时重拉
watch(
  () => [props.open, props.taskId],
  ([open]) => {
    if (open) refresh()
  },
  { immediate: true },
)
</script>

<template>
  <Modal :open="open" title="变更反馈" max-width="max-w-2xl" @close="emit('close')">
    <div v-if="loading && !bundle" class="flex items-center justify-center gap-2 py-10 text-sm text-muted">
      <Spinner class="h-4 w-4" /> 加载中…
    </div>

    <template v-else-if="bundle">
      <!-- 累计统计（Baseline 起）：文件数 / 增删行 -->
      <div class="mb-3 flex items-center gap-3 rounded-lg border border-border/60 bg-surface/60 px-3 py-2.5 text-sm">
        <span class="font-semibold">累计变更</span>
        <span class="text-muted">{{ bundle.cumulative.files }} 个文件</span>
        <span class="font-mono">
          <span class="text-success">+{{ bundle.cumulative.additions }}</span>
          <span class="ml-1.5 text-error">-{{ bundle.cumulative.deletions }}</span>
        </span>
        <span class="ml-auto flex items-center gap-2">
          <span v-if="bundle.baseline?.head_sha" class="hidden font-mono text-[11px] text-muted md:inline" :title="bundle.baseline.head_sha">
            baseline {{ bundle.baseline.head_sha.slice(0, 7) }}
          </span>
          <Button variant="ghost" size="sm" title="刷新" @click="refresh">↻</Button>
        </span>
      </div>

      <!-- Preview 运行态 -->
      <PreviewSection :task-id="taskId" class="mb-3" />

      <!-- Turn 列表（最新在前） -->
      <div class="flex flex-col gap-2">
        <TurnCard
          v-for="t in turnsDesc"
          :key="t.turn"
          :task-id="taskId"
          :turn="t"
          :checkpointed="checkpointSet.has(t.turn)"
          :can-rewind="canRewind && rewinding === null"
          @rewind="onRewind"
        />
        <div v-if="!turnsDesc.length" class="py-6 text-center text-xs text-muted">暂无 Turn 记录</div>
      </div>
    </template>

    <div v-else class="py-10 text-center text-xs text-muted">暂无数据</div>
  </Modal>
</template>
