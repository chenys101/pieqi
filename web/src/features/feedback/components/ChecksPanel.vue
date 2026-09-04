<script setup lang="ts">
// ChecksPanel：P1 Checks（p1-design.md §5）—— 验证「改得对不对」。
// 数据 = agent 事件流复用 + 用户重跑记录（GET /checks 合并返回）；
// 重跑异步：POST rerun 返回 running 态 → 存在 running 时每 2s 轮询刷新。
import { computed, onUnmounted, ref, watch } from 'vue'
import { getChecks, rerunCheck } from '@/services/api/feedback'
import type { CheckDto, CheckStatusDto } from '@/types/api'
import Button from '@/components/ui/Button.vue'
import Spinner from '@/components/ui/Spinner.vue'
import { useNotificationStore } from '@/stores/notification'

const props = defineProps<{ taskId: string }>()

const notify = useNotificationStore()
const checks = ref<CheckDto[]>([])
const loading = ref(false)
const rerunning = ref<string | null>(null)
/** 展开输出的 check id（null = 无） */
const openOutput = ref<string | null>(null)
let pollTimer: ReturnType<typeof setInterval> | null = null

/** 状态 → 图标与样式（✓ 绿 / ✗ 红 / ⟳ 转圈 / … 灰） */
const statusMeta: Record<CheckStatusDto, { icon: string; cls: string }> = {
  success: { icon: '✓', cls: 'text-success' },
  failed: { icon: '✗', cls: 'text-error' },
  running: { icon: '', cls: 'text-accent' },
  pending: { icon: '…', cls: 'text-muted' },
  skipped: { icon: '-', cls: 'text-muted' },
}

const hasRunning = computed(() => checks.value.some((ck) => ck.status === 'running'))

/** 秒级时长展示（ms → 人读） */
function fmtDuration(ms?: number): string {
  if (!ms) return ''
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(1)}s`
}

async function refresh() {
  try {
    checks.value = await getChecks(props.taskId)
  } catch {
    /* 静默：面板内已有错误提示通道 */
  }
}

/** 存在 running 态 → 轮询直到全部结束（running 态可见是重跑验收项） */
function schedulePoll() {
  stopPoll()
  pollTimer = setInterval(async () => {
    await refresh()
    if (!hasRunning.value) stopPoll()
  }, 2000)
}

function stopPoll() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

/** 重跑：立即返回 running 态记录，随后轮询收敛 */
async function onRerun(ck: CheckDto) {
  rerunning.value = ck.id
  try {
    await rerunCheck(props.taskId, ck.id)
    await refresh()
    schedulePoll()
    notify.success(`正在重跑 ${ck.name}`)
  } catch (err) {
    notify.error(err instanceof Error ? err.message : '重跑失败')
  } finally {
    rerunning.value = null
  }
}

watch(
  () => props.taskId,
  () => {
    loading.value = true
    refresh().finally(() => (loading.value = false))
  },
  { immediate: true },
)

onUnmounted(stopPoll)
</script>

<template>
  <div class="rounded-lg border border-border/60 bg-surface/60 px-3 py-2.5">
    <div class="flex items-center gap-2 text-xs">
      <span class="font-semibold">检查</span>
      <span class="text-muted">test / lint / build</span>
      <Button variant="ghost" size="sm" class="ml-auto" title="刷新" @click="refresh">↻</Button>
    </div>

    <div v-if="loading" class="flex items-center gap-2 py-3 text-xs text-muted">
      <Spinner class="h-3 w-3" /> 加载中…
    </div>
    <div v-else-if="!checks.length" class="py-3 text-xs text-muted">
      Agent 未运行可识别的检查命令（test / lint / build）
    </div>

    <div v-else class="mt-1 flex flex-col">
      <div v-for="ck in checks" :key="ck.id" class="border-b border-border/30 last:border-b-0">
        <!-- 摘要行：状态图标 + 命令 + 时长/exit code + 重跑 -->
        <div class="flex items-center gap-2 py-1.5 text-xs">
          <span class="w-4 shrink-0 text-center" :class="statusMeta[ck.status].cls">
            <Spinner v-if="ck.status === 'running'" class="h-3 w-3" />
            <template v-else>{{ statusMeta[ck.status].icon }}</template>
          </span>
          <span class="min-w-0 flex-1 truncate font-mono" :title="ck.command">{{ ck.name }}</span>
          <span v-if="ck.origin === 'rerun'" class="shrink-0 rounded border border-border px-1 text-[10px] text-muted">重跑</span>
          <span v-if="ck.turn" class="shrink-0 text-[10px] text-muted">T{{ ck.turn }}</span>
          <span v-if="ck.status === 'failed'" class="shrink-0 text-error">exit {{ ck.exit_code ?? 1 }}</span>
          <span class="shrink-0 text-muted">{{ fmtDuration(ck.duration_ms) }}</span>
          <Button
            variant="ghost"
            size="sm"
            class="shrink-0"
            :disabled="rerunning !== null"
            :title="ck.status === 'running' ? '运行中' : '重跑此命令'"
            @click="onRerun(ck)"
          >重跑</Button>
          <!-- 失败项可展开输出（错误摘要保尾） -->
          <button
            v-if="ck.output"
            class="shrink-0 text-muted transition-transform"
            :class="openOutput === ck.id ? '' : '-rotate-90'"
            title="查看输出"
            @click="openOutput = openOutput === ck.id ? null : ck.id"
          >▾</button>
        </div>
        <!-- 输出尾部（错误段通常在末尾） -->
        <pre
          v-if="ck.output && openOutput === ck.id"
          class="mb-1.5 max-h-48 overflow-auto whitespace-pre-wrap rounded border border-border/60 bg-background px-2 py-1.5 font-mono text-[11px] leading-4 text-muted"
        >{{ ck.output }}</pre>
      </div>
    </div>
  </div>
</template>
