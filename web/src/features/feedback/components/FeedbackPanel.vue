<script setup lang="ts">
// FeedbackPanel：变更反馈（p0/p1/p2-design 的内容 + SPEC §5.2 / §5.2.1 的形态）。
//
// 一个组件三种形态，由 `mode` 决定 —— 形态差异全部来自 SPEC §6.4 的三档断点：
//   dock    宽屏（≥1280）常驻右栏：收起是 46px 贴边条，展开是 420px 面板
//   drawer  平板（768–1279）420px 侧栏，靠头部按钮开合（没有"收起"这个概念）
//   full    移动端整屏：由顶部分段切换进入，没有贴边条也没有收起按钮
//
// 「移动端不套用收起态」不是省事，是必需：手机上没有"收起"的目标物，
// 留着贴边条与收起按钮就是**点了没反应的死控件**。
//
// 数据流：打开/刷新时现场派生（后端不存第二份聚合，ADR-0001）。
import { computed, nextTick, ref, watch } from 'vue'
import { rewindFileToTurn, rewindToTurn } from '@/services/api/feedback'
import type { FileStatDto, RewindVerificationDto } from '@/types/api'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import Spinner from '@/components/ui/Spinner.vue'
import TurnCard from './TurnCard.vue'
import PreviewSection from './PreviewSection.vue'
import ChecksPanel from './ChecksPanel.vue'
import OutcomeCard from './OutcomeCard.vue'
import EvidenceCard from './EvidenceCard.vue'
import DiffView from './DiffView.vue'
import FilePreview from './FilePreview.vue'
import { previewKind } from '../filePreview'
import { useNotificationStore } from '@/stores/notification'
import { useFeedbackPanelStore } from '@/stores/feedbackPanel'
import { useFeedbackBundleStore } from '@/stores/feedbackBundle'

const props = withDefaults(
  defineProps<{
    taskId: string
    /** Agent 执行中禁止回退/续问（静止边界原则）；面板仍可查看 */
    canRewind: boolean
    /** 形态，见文件头 */
    mode?: 'dock' | 'drawer' | 'full'
    /**
     * 面板当前是否真的可见（移动端只有切到「变更反馈」分段时为 true）。
     * 用来**闸住数据请求**：不可见时不拉，切走时不因为保持挂载而白拉。
     */
    active?: boolean
  }>(),
  { mode: 'full', active: true },
)

const emit = defineEmits<{ close: [] }>()

const notify = useNotificationStore()
const fb = useFeedbackPanelStore()
/** bundle 数据持有者 = 共享 store（Timeline 分组头同源，AC-R3-02/06） */
const bundleStore = useFeedbackBundleStore()

/** 四个视图：SPEC §5.2 的「概览 · 变更 · 检查 · 预览」 */
const TABS = [
  { value: 'overview', label: '概览' },
  { value: 'changes', label: '变更' },
  { value: 'checks', label: '检查' },
  { value: 'preview', label: '预览' },
]
const tab = ref('overview')

/**
 * 双视角（本轮 / 累计）**降级为「变更」Tab 内的二段切换**。
 * 它本来就是同一份数据的两个切法，升到顶层会与 Tab 语义打架 ——
 * 顶层 Tab 之间是"看哪一类东西"，它俩是"看同一类东西的哪种口径"。
 */
const VIEWS = [
  { value: 'event', label: '本轮变化' },
  { value: 'baseline', label: '累计变化' },
]
const view = ref('event')

/** bundle 来自共享 store：Timeline 与本面板读同一份对象，不存在两个版本 */
const bundle = computed(() => bundleStore.bundle(props.taskId))
const loading = ref(false)
const rewinding = ref<number | null>(null)
/** P2：文件级回退中的路径（按钮态） */
const rewindingFile = ref<string | null>(null)
/** Baseline 视角下展开的文件路径 */
const openBaselinePath = ref<string | null>(null)
/** Baseline 视角下展开预览的文件路径 */
const previewBaselinePath = ref<string | null>(null)
/** Rewind → Verify 的验证摘要（最近一次） */
const verification = ref<RewindVerificationDto | null>(null)

/** 快照 Turn 集合（O(1) 查询） */
const checkpointSet = computed(() => new Set(bundle.value?.checkpoints ?? []))
/** Turn 列表倒序：最新一轮排最前（反馈场景先看最近改动） */
const turnsDesc = computed(() => [...(bundle.value?.turns ?? [])].reverse())

/**
 * 「累计变化」列表：直接取 `cumulative.entries`（每路径一条，与表头合计同源）。
 *
 * 以前这里是「把各 Turn 的 changes 按路径去重、后轮覆盖前轮」——
 * 于是列表里的 +/- 是**某一个 Turn**的数字，而点开看到的是 baseline→当前
 * 的累计 diff：同一个文件、同一个面板，两套数。
 * 数据没有第二个来源，就不该有第二个算法。
 */
const cumulativeFiles = computed<FileStatDto[]>(() => bundle.value?.cumulative?.entries ?? [])

/**
 * 贴边条上的变更文件数 —— **收起态自带的"要不要展开"判据**。
 *
 * 取后端 `cumulative.files`（走 `git diff --numstat` 与基线对比），
 * 而不是自己数 `turns[].changes` 里的去重路径：后者只覆盖**有快照的 Turn**，
 * 老任务 / 未落快照的轮次会数出 0，贴边条就会在明明有改动时显示"没有值得看的"。
 *
 * 0 时**不显示徽章而不是显示 0**：徽章要说的是"有东西值得看"，
 * 一个恒亮的 `0` 会把"没有变更"和"还没加载完"混成同一种长相，
 * 而这两种情况用户要做的事完全不同（一个是没事，一个是等一下）。
 * 不显示 = 没有值得看的，这个编码本身没有歧义。
 */
const fileCount = computed(() => bundle.value?.cumulative?.files ?? 0)

/** Continue 后 Agent 已在跑：与回退同用静止边界判断（面板仅查看） */
const canContinue = computed(() => props.canRewind)

/** 收起态（仅 dock 形态有 46px 贴边条这一说） */
const railOnly = computed(() => props.mode === 'dock' && fb.collapsed)
/**
 * 是否需要/允许拉数据。
 * dock 形态**无论收没收起都要拉** —— 贴边条上的文件数就来自这份数据，
 * 「不展开也知道有没有值得看的东西」是 SPEC 对收起态提的硬要求，
 * 不是可以省掉的一次请求。其余形态只在可见时才拉。
 */
const shouldLoad = computed(() => props.mode === 'dock' || props.active)
/** 面板主体是否渲染（贴边条态下整个主体让位给那条 46px） */
const bodyOn = computed(() => (props.mode === 'dock' ? !fb.collapsed : props.active))

const baselineLine = computed(() => {
  const sha = bundle.value?.baseline?.head_sha
  return sha ? `baseline ${sha.slice(0, 7)}` : ''
})

async function refresh() {
  if (!props.taskId) return
  loading.value = true
  try {
    // force：回退 / 重跑后必须看到新数字，本地有缓存也要重拉
    await bundleStore.load(props.taskId, true)
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

/**
 * P2 文件级回退（p2-design.md §7）：只恢复单个文件到 Turn N 开始之前。
 * 同样带 verify（回退后重跑目标轮 checks + 重启 preview）。
 */
async function onRewindFile(turn: number, path: string) {
  rewindingFile.value = path
  try {
    const res = await rewindFileToTurn(props.taskId, turn, path, true)
    verification.value = res.verification ?? null
    notify.success(`已回退 ${path} 到 Turn #${turn} 之前`)
    await refresh()
  } catch (err) {
    notify.error(err instanceof Error ? err.message : '文件回退失败')
  } finally {
    rewindingFile.value = null
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

// 可见（或需要贴边条计数）时拉取；taskId 变化时重拉
watch(
  () => [shouldLoad.value, props.taskId] as const,
  ([on]) => {
    if (on) refresh()
  },
  { immediate: true },
)

/** AC-R3-06：Timeline「查看本轮变更」→ 变更 Tab（本轮变化视图）滚动定位到对应
 *  TurnCard。注意不是「概览」—— 那里是 R4 的证据卡，TurnCard 只住在「变更」。
 *  activeTurn 在 store 里持久（切页回来仍定位），但只有变化瞬间才滚动 ——
 *  常驻高亮会让"上次的选中"变成噪音。 */
const panelEl = ref<HTMLElement | null>(null)
watch(
  () => fb.activeTurn,
  async (turn) => {
    if (!turn || !bodyOn.value) return
    tab.value = 'changes'
    view.value = 'event'
    await nextTick()
    panelEl.value?.querySelector(`[data-turn="${turn}"]`)?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
  },
)
</script>

<template>
  <div
    ref="panelEl"
    class="flex h-full min-h-0 flex-col bg-surface-subtle"
    :class="mode === 'drawer' ? 'w-[420px] shrink-0' : 'w-full'"
    data-testid="feedback-panel"
  >
    <!-- 收起态：46px 竖向贴边条。
         它本身就是展开入口，且带着变更文件数 —— 用户不展开也知道
         「有没有值得看的东西」。这就是判断一个结构能否默认收起的标准：
         **收起态必须自己携带"要不要展开"的判据**，否则等于纯隐藏。 -->
    <button
      v-if="railOnly"
      type="button"
      class="flex w-full flex-1 cursor-pointer flex-col items-center gap-2.5 border-0 bg-transparent py-3 transition-colors hover:bg-surface-muted"
      aria-expanded="false"
      aria-controls="fbPanel"
      title="展开变更反馈"
      data-testid="feedback-rail"
      @click="fb.expand()"
    >
      <!-- 面板从右边缘**向左**拉出，箭头就指向左。
           写成 › 会被读成"再往右推"，与点击后的实际动效相反。 -->
      <svg
        class="h-4 w-4 shrink-0 text-text-tertiary"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        aria-hidden="true"
      >
        <path d="M15 18l-6-6 6-6" stroke-linecap="round" stroke-linejoin="round" />
      </svg>
      <span class="text-[12px] font-semibold tracking-[.14em] text-text-secondary [writing-mode:vertical-rl]">
        变更反馈
      </span>
      <span
        v-if="fileCount"
        class="inline-flex h-5 min-w-[20px] shrink-0 items-center justify-center rounded-full bg-accent/10 px-1.5 text-[11px] font-bold tabular-nums text-accent"
        :title="`${fileCount} 个文件有变更`"
      >{{ fileCount }}</span>
    </button>

    <template v-else-if="bodyOn">
      <header class="flex shrink-0 items-center gap-1 border-b border-border bg-surface px-3.5 py-2.5">
        <h2 class="flex-1 truncate text-[13.5px] font-semibold text-text">变更反馈</h2>
        <span v-if="bundle" class="mr-0.5 shrink-0 font-mono text-[11px]">
          <span class="text-success">+{{ bundle.cumulative.additions }}</span>
          <span class="ml-1 text-error">-{{ bundle.cumulative.deletions }}</span>
        </span>
        <button
          type="button"
          class="shrink-0 cursor-pointer rounded px-1.5 py-0.5 text-[13px] leading-none text-text-tertiary transition-colors hover:bg-surface-muted hover:text-text"
          title="刷新"
          @click="refresh"
        >↻</button>
        <!-- 「收起」只在 dock 形态出现：另外两档没有可收起的目标物，
             留着就是一个点了没反应的死控件（SPEC §5.2.1）。 -->
        <button
          v-if="mode === 'dock'"
          type="button"
          class="shrink-0 cursor-pointer rounded px-1.5 py-0.5 text-[13px] leading-none text-text-tertiary transition-colors hover:bg-surface-muted hover:text-text"
          title="收起变更反馈"
          aria-expanded="true"
          aria-controls="fbPanel"
          @click="fb.collapse()"
        >›</button>
        <button
          v-else-if="mode === 'drawer'"
          type="button"
          class="shrink-0 cursor-pointer rounded px-1.5 py-0.5 text-base leading-none text-text-tertiary transition-colors hover:bg-surface-muted hover:text-text"
          title="关闭"
          @click="emit('close')"
        >×</button>
      </header>

      <div class="shrink-0 px-3.5 pt-2.5">
        <SegmentedControl v-model="tab" :options="TABS" fill aria-label="变更反馈视图" />
      </div>

      <!-- 验证摘要放在 Tab **之上**：回退动作发生在「变更」Tab，
           摘要只挂在「概览」里的话，用户会在原地看不到刚做完那件事的结果。 -->
      <div
        v-if="verification"
        class="mx-3.5 mt-2.5 shrink-0 rounded-lg border border-accent/40 bg-accent/5 px-3 py-2 text-[11.5px]"
      >
        <span class="font-semibold text-accent">回退验证</span>
        <span class="ml-1.5 text-text-secondary">{{ verifyLine(verification) }}</span>
      </div>

      <div id="fbPanel" class="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto px-3.5 py-3">
        <div v-if="loading && !bundle" class="flex items-center justify-center gap-2 py-10 text-xs text-text-tertiary">
          <Spinner class="h-4 w-4" /> 加载中…
        </div>

        <template v-else-if="bundle">
          <!-- 概览：任务结果 + 基线信息 + 证据（带证据继续） -->
          <template v-if="tab === 'overview'">
            <OutcomeCard :task-id="taskId" />
            <div
              v-if="baselineLine"
              class="flex items-center gap-1.5 rounded-lg border border-border/60 bg-surface/60 px-3 py-2 text-[11.5px] text-text-tertiary"
            >
              <span class="font-mono">{{ baselineLine }}</span>
              <span class="ml-auto">{{ turnsDesc.length }} 个 Turn</span>
            </div>
            <EvidenceCard :task-id="taskId" :can-continue="canContinue" />
          </template>

          <!-- 变更：本轮 / 累计两个视角 + Turn 列表 / 累计文件集 -->
          <template v-else-if="tab === 'changes'">
            <SegmentedControl v-model="view" :options="VIEWS" fill aria-label="变更视角" />
            <div v-if="view === 'event'" class="flex flex-col gap-2">
              <TurnCard
                v-for="t in turnsDesc"
                :key="t.turn"
                :task-id="taskId"
                :turn="t"
                :checkpointed="checkpointSet.has(t.turn)"
                :can-rewind="canRewind && rewinding === null && rewindingFile === null"
                :highlighted="fb.activeTurn === t.turn"
                @rewind="onRewind"
                @rewind-file="onRewindFile"
              />
              <div v-if="!turnsDesc.length" class="py-6 text-center text-xs text-text-tertiary">暂无 Turn 记录</div>
            </div>
            <div v-else class="rounded-lg border border-border/60 bg-surface/60">
              <div v-if="!cumulativeFiles.length" class="px-3 py-2 text-xs text-text-tertiary">暂无累计变更</div>
              <div v-for="fc in cumulativeFiles" :key="fc.path" class="border-b border-border/30 last:border-b-0">
                <div class="flex items-center gap-2">
                  <button
                    type="button"
                    class="flex min-w-0 flex-1 cursor-pointer items-center gap-2 px-3 py-1.5 text-left text-xs hover:bg-surface-muted"
                    @click="openBaselinePath = openBaselinePath === fc.path ? null : fc.path"
                  >
                    <span class="min-w-0 flex-1 truncate font-mono text-text-secondary" :title="fc.path">{{ fc.path }}</span>
                    <span class="shrink-0 font-mono">
                      <span v-if="fc.additions || fc.deletions" class="text-success">+{{ fc.additions }}</span>
                      <span v-if="fc.additions || fc.deletions" class="ml-1 text-error">-{{ fc.deletions }}</span>
                    </span>
                    <span class="shrink-0 text-text-tertiary transition-transform" :class="openBaselinePath === fc.path ? '' : '-rotate-90'">▾</span>
                  </button>
                  <button
                    v-if="previewKind(fc.path)"
                    type="button"
                    class="shrink-0 cursor-pointer px-2.5 py-1 text-xs"
                    :class="previewBaselinePath === fc.path ? 'text-accent' : 'text-text-tertiary hover:text-text'"
                    @click="previewBaselinePath = previewBaselinePath === fc.path ? null : fc.path"
                  >预览</button>
                </div>
                <!-- turn 省略 = Baseline 累计 diff -->
                <DiffView v-if="openBaselinePath === fc.path" :task-id="taskId" :path="fc.path" />
                <!-- 文件预览（markdown/pdf） -->
                <FilePreview v-if="previewBaselinePath === fc.path" :task-id="taskId" :path="fc.path" />
              </div>
            </div>
          </template>

          <!-- 检查 -->
          <ChecksPanel v-else-if="tab === 'checks'" :task-id="taskId" />

          <!-- 预览 -->
          <PreviewSection v-else :task-id="taskId" />
        </template>

        <div v-else class="py-10 text-center text-xs text-text-tertiary">暂无数据</div>
      </div>
    </template>
  </div>
</template>
