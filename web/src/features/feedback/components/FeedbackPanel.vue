<script setup lang="ts">
// FeedbackPanel：反馈总览面板（p0-design.md §5.1 + p1-design.md §2-9）。
// P0：累计统计 + Preview 控制 + Turn 卡片列表（最新在前，展开看文件 diff）。
// P1：双视角 tab（本轮 Event / 累计 Baseline）、Outcome 结构化结果、Checks、
//      Evidence→Continue 续问闭环、Rewind→Verify 回退验证。
// 数据流：打开/刷新时现场派生（后端不存第二份聚合，ADR-0001）。
import { computed, ref, watch } from 'vue'
import { getFeedback, rewindToTurn } from '@/services/api/feedback'
import type { FileChangeDto, FeedbackBundleDto, RewindVerificationDto } from '@/types/api'
import Modal from '@/components/ui/Modal.vue'
import Spinner from '@/components/ui/Spinner.vue'
import Button from '@/components/ui/Button.vue'
import TurnCard from './TurnCard.vue'
import PreviewSection from './PreviewSection.vue'
import ChecksPanel from './ChecksPanel.vue'
import OutcomeCard from './OutcomeCard.vue'
import EvidenceCard from './EvidenceCard.vue'
import DiffView from './DiffView.vue'
import { useNotificationStore } from '@/stores/notification'

const props = defineProps<{
  taskId: string
  open: boolean
  /** Agent 执行中禁止回退/续问（静止边界原则）；面板仍可查看 */
  canRewind: boolean
}>()

const emit = defineEmits<{ close: [] }>()

const notify = useNotificationStore()
const bundle = ref<FeedbackBundleDto | null>(null)
const loading = ref(false)
const rewinding = ref<number | null>(null)
/** 双视角：event = 按 Turn 展开；baseline = 累计文件集（p1-design.md §3） */
const view = ref<'event' | 'baseline'>('event')
/** Baseline 视角下展开的文件路径 */
const openBaselinePath = ref<string | null>(null)
/** Rewind → Verify 的验证摘要（最近一次） */
const verification = ref<RewindVerificationDto | null>(null)

/** 快照 Turn 集合（O(1) 查询） */
const checkpointSet = computed(() => new Set(bundle.value?.checkpoints ?? []))
/** Turn 列表倒序：最新一轮排最前（反馈场景先看最近改动） */
const turnsDesc = computed(() => [...(bundle.value?.turns ?? [])].reverse())

/** Baseline 视角：聚合各 Turn 的文件变更（后 Turn 覆盖同路径，最新态为准） */
const baselineFiles = computed<FileChangeDto[]>(() => {
  const byPath = new Map<string, FileChangeDto>()
  for (const t of bundle.value?.turns ?? []) {
    for (const fc of t.changes ?? []) byPath.set(fc.path, fc)
  }
  return [...byPath.values()].sort((a, b) => a.path.localeCompare(b.path))
})

/** Continue 后 Agent 已在跑：与回退同用静止边界判断（面板仅查看） */
const canContinue = computed(() => props.canRewind)

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

/**
 * 回退到 Turn N 之前（P1 起带 verify：回退后自动重跑目标轮 checks + 重启 preview）。
 * 成功后展示验证摘要；文件恢复与验证解耦——验证失败不影响已恢复的文件。
 */
async function onRewind(turn: number) {
  rewinding.value = turn
  try {
    const res = await rewindToTurn(props.taskId, turn, true)
    verification.value = res.verification ?? null
    notify.success(`已回退到 Turn #${turn} 之前（恢复 ${res.restored.length} 个文件）`)
    await refresh()
  } catch (err) {
    notify.error(err instanceof Error ? err.message : '回退失败')
  } finally {
    rewinding.value = null
  }
}

/** 验证摘要 → 一行人读文本（失败项可感知） */
function verifyLine(v: RewindVerificationDto): string {
  const parts = [`恢复 ${v.restored_files} 个文件`]
  const ok = v.checks.filter((c) => c.status === 'success').length
  const fail = v.checks.filter((c) => c.status === 'failed')
  if (v.checks.length) parts.push(`检查 ${ok}/${v.checks.length} 通过`)
  if (fail.length) parts.push(`失败: ${fail.map((c) => c.name).join('、')}`)
  parts.push(`预览 ${v.preview.state}`)
  return parts.join(' · ')
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
      <!-- P1：Task 结构化结果（完成度 / checks / issues，手机端主验收面） -->
      <OutcomeCard :task-id="taskId" class="mb-3" />

      <!-- P1：Rewind → Verify 验证摘要（回退后自动重跑 checks + 重启 preview） -->
      <div v-if="verification" class="mb-3 rounded-lg border border-accent/40 bg-accent/5 px-3 py-2 text-xs">
        <span class="font-semibold text-accent">回退验证</span>
        <span class="ml-1.5 text-muted">{{ verifyLine(verification) }}</span>
      </div>

      <!-- 双视角 tab：本轮变化（Event）/ 累计变化（Baseline），p1-design.md §3 -->
      <div class="mb-3 flex items-center gap-1 rounded-lg border border-border/60 bg-surface/60 p-1 text-xs">
        <button
          class="flex-1 rounded-md px-2 py-1 transition-colors"
          :class="view === 'event' ? 'bg-elevated font-semibold' : 'text-muted hover:text-text'"
          @click="view = 'event'"
        >本轮变化</button>
        <button
          class="flex-1 rounded-md px-2 py-1 transition-colors"
          :class="view === 'baseline' ? 'bg-elevated font-semibold' : 'text-muted hover:text-text'"
          @click="view = 'baseline'"
        >累计变化</button>
        <span class="ml-auto pr-2 font-mono text-[11px] text-muted">
          <span class="text-success">+{{ bundle.cumulative.additions }}</span>
          <span class="ml-1 text-error">-{{ bundle.cumulative.deletions }}</span>
        </span>
      </div>

      <!-- Preview 运行态（P1 起含「重启」入口） -->
      <PreviewSection :task-id="taskId" class="mb-3" />

      <!-- P1：Checks（agent 复用 + 重跑） -->
      <ChecksPanel :task-id="taskId" class="mb-3" />

      <!-- P1：Evidence Card + 带证据继续（控制闭环） -->
      <EvidenceCard :task-id="taskId" :can-continue="canContinue" class="mb-3" />

      <!-- Event 视角：Turn 列表（最新在前） -->
      <div v-if="view === 'event'" class="flex flex-col gap-2">
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

      <!-- Baseline 视角：累计文件集（点击展开 Baseline 累计 diff） -->
      <div v-else class="rounded-lg border border-border/60 bg-surface/60">
        <div v-if="!baselineFiles.length" class="px-3 py-2 text-xs text-muted">暂无累计变更</div>
        <div v-for="fc in baselineFiles" :key="fc.path" class="border-b border-border/30 last:border-b-0">
          <button
            class="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs hover:bg-elevated"
            @click="openBaselinePath = openBaselinePath === fc.path ? null : fc.path"
          >
            <span class="min-w-0 flex-1 truncate font-mono text-muted" :title="fc.path">{{ fc.path }}</span>
            <span class="shrink-0 font-mono">
              <span v-if="fc.additions || fc.deletions" class="text-success">+{{ fc.additions ?? 0 }}</span>
              <span v-if="fc.additions || fc.deletions" class="ml-1 text-error">-{{ fc.deletions ?? 0 }}</span>
            </span>
            <span class="shrink-0 text-muted transition-transform" :class="openBaselinePath === fc.path ? '' : '-rotate-90'">▾</span>
          </button>
          <!-- turn 省略 = Baseline 累计 diff -->
          <DiffView v-if="openBaselinePath === fc.path" :task-id="taskId" :path="fc.path" />
        </div>
      </div>

      <!-- 刷新入口（底部） -->
      <div class="mt-3 flex justify-end">
        <span v-if="bundle.baseline?.head_sha" class="mr-2 self-center font-mono text-[11px] text-muted" :title="bundle.baseline.head_sha">
          baseline {{ bundle.baseline.head_sha.slice(0, 7) }}
        </span>
        <Button variant="ghost" size="sm" title="刷新" @click="refresh">↻ 刷新</Button>
      </div>
    </template>

    <div v-else class="py-10 text-center text-xs text-muted">暂无数据</div>
  </Modal>
</template>
