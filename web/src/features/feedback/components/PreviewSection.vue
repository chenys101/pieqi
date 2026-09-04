<script setup lang="ts">
// PreviewSection：Preview 运行态控制（p0-design.md §7.3 + p1-design.md §10）。
// start → 异步启动（starting → running，轮询感知）；stop → 停止；
// restart → 停止后重启（P1：Rewind→Verify 后 / 非 HMR 框架改动后手动刷新）；
// attach → 外链 + 二维码（P1：隧道可达时在外部浏览器打开）；
// running 时提供子路径代理入口（新标签打开，鉴权经 cookie/header 同源）。
import { computed, onUnmounted, ref, watch } from 'vue'
import { getPreviewAttach, getPreviewStatus, restartPreview, startPreview, stopPreview } from '@/services/api/feedback'
import type { PreviewAttachDto, PreviewStatusDto } from '@/types/api'
import Button from '@/components/ui/Button.vue'
import Spinner from '@/components/ui/Spinner.vue'
import { useNotificationStore } from '@/stores/notification'

const props = defineProps<{ taskId: string }>()

/** 当前 preview 状态（初值 null = 未拉取） */
const status = ref<PreviewStatusDto | null>(null)
const busy = ref(false)
const notify = useNotificationStore()
let pollTimer: ReturnType<typeof setInterval> | null = null

/** P1：Attach 结果（外链 + 二维码），null = 未拉取 */
const attach = ref<PreviewAttachDto | null>(null)
const attaching = ref(false)

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

/** P1：停止后重启（stop + start；Rewind→Verify / 非 HMR 改动手动刷新） */
async function onRestart() {
  busy.value = true
  try {
    await restartPreview(props.taskId)
    await refresh()
    pollWhileStarting()
  } catch (err) {
    notify.error(err instanceof Error ? err.message : '重启失败')
    await refresh()
  } finally {
    busy.value = false
  }
}

/**
 * P1：拉取外链 + 二维码（隧道可达时在外部浏览器打开）。
 * 409（preview 未跑 / 隧道未开）→ 提示前置条件，不弹错误。
 */
async function onAttach() {
  attaching.value = true
  try {
    attach.value = await getPreviewAttach(props.taskId)
  } catch (err) {
    attach.value = null
    notify.error(err instanceof Error ? err.message : '获取外链失败（需预览运行中且隧道已开启）')
  } finally {
    attaching.value = false
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
        <!-- P1：重启（停止后重启，代码改动非 HMR 时手动刷新） -->
        <Button
          v-if="!isStarting"
          variant="ghost"
          size="sm"
          :loading="busy"
          :title="'停止后重启（改动非热更新时刷新预览）'"
          @click="onRestart"
        >重启</Button>
        <!-- P1：外链 + 二维码（隧道可达时在外部浏览器打开） -->
        <Button variant="ghost" size="sm" :loading="attaching" title="生成外链与二维码" @click="onAttach">外链</Button>
        <Button v-if="!isRunning && !isStarting" variant="secondary" size="sm" :loading="busy" @click="onStart">启动</Button>
        <Button v-else variant="ghost" size="sm" :loading="busy" @click="onStop">停止</Button>
      </span>
    </div>

    <!-- P1：Attach 结果（外链 + 二维码，供其他设备扫码访问） -->
    <div v-if="attach" class="mt-2 flex items-start gap-3 rounded-md border border-border/60 bg-background/60 p-2.5">
      <img :src="attach.qr" alt="预览外链二维码" class="h-28 w-28 shrink-0 rounded border border-border" />
      <div class="flex min-w-0 flex-col gap-1">
        <div class="text-[11px] text-muted">外部浏览器可打开（含 token，勿外传）：</div>
        <a :href="attach.url" target="_blank" rel="noopener" class="break-all text-xs text-info hover:underline">{{ attach.url }}</a>
        <button class="self-start text-[11px] text-muted hover:text-text" @click="attach = null">收起</button>
      </div>
    </div>
  </div>
</template>
