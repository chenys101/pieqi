<script setup lang="ts">
// PreviewSection：Preview 运行态控制（p0-design.md §7.3 + p1-design.md §10 + p2-design.md §3）。
// start → 异步启动（starting → running，轮询感知）；stop → 停止；
// restart → 停止后重启（P1：Rewind→Verify 后 / 非 HMR 框架改动后手动刷新）；
// attach → 外链 + 二维码（P1：隧道可达时在外部浏览器打开）；
// （原 capture → 采集截图/console/network，2026-10-15 随视觉采集层移除，见 ADR-0012/0013）
// running 时提供子路径代理入口（新标签打开，鉴权经 cookie/header 同源）。
import { computed, onUnmounted, ref, watch } from 'vue'
import {
  getPreviewAttach,
  getPreviewStatus,
  getTaskArtifacts,
  restartPreview,
  startPreview,
  stopPreview,
} from '@/services/api/feedback'
import type { ArtifactDto, PreviewAttachDto, PreviewStatusDto } from '@/types/api'
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

/**
 * 产物清单（agent 产出的可查看文件：方案页 / 报告 / 截图 / pdf）。
 *
 * 与「预览」按钮的关系：产物是**文件**，点开即看，不需要先启动；preview 只在
 * 需要完整同源资源（多文件页面的相对 css/js）时才用得上。服务端已经把每项的
 * 打开 URL 算好（目录内 html → preview 路径，其余 → /file），前端不判断通道。
 */
const artifacts = ref<ArtifactDto[]>([])
const showAllArtifacts = ref(false)
const ARTIFACT_PREVIEW_COUNT = 6
const visibleArtifacts = computed(() =>
  showAllArtifacts.value ? artifacts.value : artifacts.value.slice(0, ARTIFACT_PREVIEW_COUNT),
)

const KIND_LABEL: Record<string, string> = {
  html: '页面',
  markdown: '文档',
  image: '图片',
  pdf: 'PDF',
}

function fmtSize(n?: number): string {
  if (!n) return ''
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${Math.round(n / 1024)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}

const stateLabel = computed(() => {
  const map: Record<string, string> = {
    // 两形态都可预览（dev server / 静态产物），所以不说"未识别到 dev server"——
    // 真实原因常是"还没把页面放到预览目录"，具体原因由下面的 hint 说明。
    unavailable: '不可用',
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
/**
 * 静态产物：pieqi 进程内直接读文件，没有进程可重启、也没有必要"停止"
 * （"停止"只会让页面暂时打不开，是纯粹的坑）。所以只留「打开 / 外链」。
 */
const isStatic = computed(() => status.value?.framework === 'static')

/** 代理入口（相对路径，同源携带鉴权） */
const previewURL = computed(() => `/preview/${encodeURIComponent(props.taskId)}/`)

async function refresh() {
  try {
    status.value = await getPreviewStatus(props.taskId)
  } catch {
    /* 静默：面板内已有错误提示通道 */
  }
}

/** 拉产物清单（失败静默：没有产物不是错误，列表为空即可）。 */
async function refreshArtifacts() {
  try {
    artifacts.value = await getTaskArtifacts(props.taskId)
  } catch {
    artifacts.value = []
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
    await refreshArtifacts() // 静态目录登记后，页面的打开通道会变成 preview
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
    await refreshArtifacts()
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

// 打开时拉一次状态；taskId 变化时重新拉取。
//
// **若上来就是 starting，必须把轮询接回去** ——「预览」现在是反馈面板里的一个 Tab，
// 用户点完「启动」切去别的 Tab 再切回来会重新挂载；只刷新不续轮询的话，
// 状态会一直停在"启动中"直到下次切回来，看起来像卡死了。
async function init() {
  await Promise.all([refresh(), refreshArtifacts()])
  if (isStarting.value) pollWhileStarting()
}

watch(() => props.taskId, init, { immediate: true })

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
        <!-- 「截图」按钮及其背后的视觉采集（截图/console/network）已于 2026-10-15 移除，
             见 ADR-0012 / ADR-0013。保留 preview 的启动/重启/打开——那是 P1 能力，与采集无关。 -->
        <!-- P1：重启（停止后重启，代码改动非 HMR 时手动刷新）。静态产物无进程可重启 -->
        <Button
          v-if="!isStarting && !isStatic"
          variant="ghost"
          size="sm"
          :loading="busy"
          :title="'停止后重启（改动非热更新时刷新预览）'"
          @click="onRestart"
        >重启</Button>
        <!-- P1：外链 + 二维码（隧道可达时在外部浏览器打开） -->
        <Button variant="ghost" size="sm" :loading="attaching" title="生成外链与二维码" @click="onAttach">外链</Button>
        <Button v-if="!isRunning && !isStarting" variant="secondary" size="sm" :loading="busy" @click="onStart">启动</Button>
        <!-- 静态产物没有「停止」：它不占进程，停掉只会让页面暂时打不开（见 isStatic 注释） -->
        <Button v-else-if="!isStatic" variant="ghost" size="sm" :loading="busy" @click="onStop">停止</Button>
      </span>
    </div>

    <!-- 产物：agent 产出的可查看文件。点开即看，不需要先「启动」——
         启动是 dev server 的概念，文件本身没有准备期。 -->
    <div v-if="artifacts.length" class="mt-2">
      <div class="mb-1 flex items-center gap-1.5 text-[11px] text-muted">
        <span>产物</span>
        <span class="text-text-tertiary">{{ artifacts.length }}</span>
      </div>
      <div class="flex flex-col gap-1">
        <a
          v-for="a in visibleArtifacts"
          :key="a.path"
          :href="a.open"
          target="_blank"
          rel="noopener"
          class="flex min-h-11 items-center gap-2 rounded-md border border-border/50 bg-background/40 px-2 py-1 text-xs hover:bg-surface-muted"
          :title="a.path"
        >
          <span class="shrink-0 rounded bg-background/80 px-1 text-[10px] text-muted">{{ KIND_LABEL[a.kind] ?? a.kind }}</span>
          <span class="min-w-0 flex-1 truncate">{{ a.path }}</span>
          <span class="shrink-0 text-[10px] text-text-tertiary">{{ fmtSize(a.size) }}</span>
        </a>
      </div>
      <button
        v-if="artifacts.length > ARTIFACT_PREVIEW_COUNT"
        type="button"
        class="mt-1 text-[11px] text-muted hover:text-text"
        @click="showAllArtifacts = !showAllArtifacts"
      >
        {{ showAllArtifacts ? '收起' : `展开全部 ${artifacts.length} 个` }}
      </button>
    </div>

    <!-- 指路：认不出预览入口时，直接告诉用户/agent 该把产物放哪（ADR-0017 补记）。
         目录约定是"魔法位置"，只回一句"不可用"会让人以为功能坏了。 -->
    <div v-if="status?.hint" class="mt-2 rounded-md border border-warning/30 bg-warning/5 px-2.5 py-1.5 text-[11px] leading-relaxed text-muted">
      {{ status.hint }}
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
