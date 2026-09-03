<script setup lang="ts">
// PreviewSection：Preview 运行态控制（p0-design.md §7.3）。
// start → 异步启动（starting → running，轮询感知）；stop → 停止；
// running 时提供子路径代理入口（新标签打开，鉴权经 cookie/header 同源）。
import { computed, onUnmounted, ref, watch } from 'vue'
import { getPreviewStatus, startPreview, stopPreview } from '@/services/api/feedback'
import type { PreviewStatusDto } from '@/types/api'
import Button from '@/components/ui/Button.vue'
import Spinner from '@/components/ui/Spinner.vue'
import { useNotificationStore } from '@/stores/notification'

const props = defineProps<{ taskId: string }>()

/** 当前 preview 状态（初值 null = 未拉取） */
const status = ref<PreviewStatusDto | null>(null)
const busy = ref(false)
const notify = useNotificationStore()
let pollTimer: ReturnType<typeof setInterval> | null = null

const stateLabel = computed(() => {
  const map: Record<string, string> = {
    unavailable: '不可用（未识别到 dev server）',
    available: '可用',
    starting: '启动中',
    running: '运行中',
    stopped: '已停止',
    error: '异常退出',
  }
  return map[status.value?.state ?? ''] ?? status.value?.state ?? '未知'
})

const isRunning = computed(() => status.value?.state === 'running')
const isStarting = computed(() => status.value?.state === 'starting')

/** 代理入口（相对路径，同源携带鉴权） */
const previewURL = computed(() => `/api/tasks/${encodeURIComponent(props.taskId)}/preview/`)

async function refresh() {
  try {
    status.value = await getPreviewStatus(props.taskId)
  } catch {
    /* 静默：面板内已有错误提示通道 */
  }
}

/** starting → 每 2s 轮询直到 running/error（面板在即可轮询） */
function pollWhileStarting() {
  stopPoll()
  pollTimer = setInterval(async () => {
    await refresh()
    if (status.value && status.value.state !== 'starting') stopPoll()
  }, 2000)
}

function stopPoll() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

async function onStart() {
  busy.value = true
  try {
    await startPreview(props.taskId)
    await refresh()
    pollWhileStarting()
  } catch (err) {
    notify.error(err instanceof Error ? err.message : '启动失败')
    await refresh()
  } finally {
    busy.value = false
  }
}

async function onStop() {
  busy.value = true
  try {
    await stopPreview(props.taskId)
  } catch (err) {
    notify.error(err instanceof Error ? err.message : '停止失败')
  } finally {
    busy.value = false
    await refresh()
  }
}

// 打开时拉一次状态；taskId 变化时重新拉取
watch(
  () => props.taskId,
  () => refresh(),
  { immediate: true },
)

onUnmounted(stopPoll)
</script>

<template>
  <div class="rounded-lg border border-border/60 bg-surface/60 px-3 py-2.5">
    <div class="flex items-center gap-2 text-xs">
      <span class="font-semibold">预览</span>
      <!-- 状态：running 绿点 / starting 转圈 / 其余灰 -->
      <span v-if="isStarting" class="flex items-center gap-1 text-muted"><Spinner class="h-3 w-3" />{{ stateLabel }}</span>
      <span v-else class="flex items-center gap-1" :class="isRunning ? 'text-success' : 'text-muted'">
        <span class="inline-block h-1.5 w-1.5 rounded-full" :class="isRunning ? 'bg-success' : 'bg-muted'" />
        {{ stateLabel }}
      </span>
      <span v-if="status?.framework" class="rounded border border-border px-1 text-[10px] text-muted">{{ status.framework }}</span>

      <span class="ml-auto flex shrink-0 items-center gap-1.5">
        <!-- running：新标签打开代理入口 -->
        <a v-if="isRunning" :href="previewURL" target="_blank" rel="noopener"
           class="rounded-md border border-accent/40 px-2.5 py-1 text-xs text-accent hover:bg-accent/10"
        >打开</a>
        <Button v-if="!isRunning && !isStarting" variant="secondary" size="sm" :loading="busy" @click="onStart">启动</Button>
        <Button v-else variant="ghost" size="sm" :loading="busy" @click="onStop">停止</Button>
      </span>
    </div>
  </div>
</template>
