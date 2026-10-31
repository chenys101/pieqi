<script setup lang="ts">
// FeedbackPanel：变更反馈（p0/p1/p2-design 的内容 + SPEC §5.2 / §5.2.1 的形态）。
//
// 一个组件三种形态，由 `mode` 决定 —— 形态差异全部来自 SPEC §6.4 的三档断点：
//   dock    宽屏（≥1280）常驻右栏：收起是 46px 贴边条，展开是 420px 面板
//   drawer  平板（768–1279）420px 侧栏，靠头部按钮开合（没有"收起"这个概念）
//   full    移动端整屏：由会话头部那一行的反馈图标切换进入，没有贴边条也没有收起按钮
//
// 「移动端不套用收起态」不是省事，是必需：手机上没有"收起"的目标物，
// 留着贴边条与收起按钮就是**点了没反应的死控件**。
//
// 2026-10 变更（长列表 + 文件查看）：
//   · 变更 Tab 的文件分组**默认全展开**（用户要的是"点进来就看见文件"）；
//   · 顶部固定工具条：全部收起 / 全部展开、目录索引（>6 个目录）、筛选；
//   · 「全部收起」在「本轮变化」下**连 Turn 一起收**（受控 expanded），
//     在「累计变化」下收所有目录分组；
//   · 目录分组头 sticky；筛选命中时强制展开，只渲染命中的目录/文件；
//   · 文件行不再内联铺代码：md/code/text/pdf 走应用内查看层（ArtifactViewer），
//     html/image 仍新标签打开（真页面）。
//
// 数据流：打开/刷新时现场派生（后端不存第二份聚合，ADR-0001）。
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
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
import { previewKind } from '@/utils/filePreview'
import { baseNameOf, groupByFolder } from '@/utils/fileGroups'
import { useNotificationStore } from '@/stores/notification'
import { useFeedbackPanelStore } from '@/stores/feedbackPanel'
import { useFeedbackBundleStore } from '@/stores/feedbackBundle'
import { useArtifactsStore } from '@/stores/artifacts'

const props = withDefaults(
  defineProps<{
    taskId: string
    /** Agent 执行中禁止回退/续问（静止边界原则）；面板仍可查看 */
    canRewind: boolean
    /** 形态，见文件头 */
    mode?: 'dock' | 'drawer' | 'full'
    /**
     * 面板当前是否真的可见（移动端只有头部图标切到「变更反馈」时为 true）。
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
const artifactsStore = useArtifactsStore()

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
 *
 * 默认「本轮变化」：用户最常看的是"这一轮改了什么"；累计是跨轮对照。
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
/** Baseline 视角下展开 diff 的文件路径 */
const openBaselinePath = ref<string | null>(null)
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
 * 产物索引：与 Timeline 共用 artifacts store（ADR-0018 单一事实源）。
 * 文件清单**只在这张表里查"能不能点开"**，绝不自己拼 URL。
 */
const artifactsByPath = computed(() => artifactsStore.byPath(props.taskId))

/** 一行 = 一个文件 + 展示名 + 打开入口 */
interface FileRow {
  fc: FileStatDto
  /** 分组内只显示文件名（目录由分组头承担）；单一目录时保留完整路径 */
  name: string
  /** html/image 的「新标签打开」入口；其余为 null */
  open: { href: string; label: string } | null
  /** md/code/text/pdf：应用内查看层入口 */
  canView: boolean
}

/**
 * 累计变更按目录分组，**默认全展开**。
 *
 * 只有一个目录时 `grouped` 为 false：分组是为了收掉长列表，一组还能收起
 * 等于"多点一次才看得到内容"，且行内只剩文件名会丢掉唯一的目录上下文。
 */
const cumulativeGroups = computed(() => {
  const groups = groupByFolder(cumulativeFiles.value)
  const grouped = groups.length > 1
  return groups.map((g) => ({
    ...g,
    grouped,
    rows: g.files.map<FileRow>((fc) => ({
      fc,
      name: grouped ? baseNameOf(fc.path) : fc.path,
      open: openLinkOf(fc.path),
      canView: !!previewKind(fc.path),
    })),
  }))
})

/* ── 展开态 ─────────────────────────────────────────────────────────────
   累计：closedCum 记录**已收起**的目录（空 = 全展开）。
   本轮：openTurns 记录展开的 Turn（默认只有最新一轮）；目录分组在 TurnCard
   内部，用 groupReset 信号批量开合（默认全展开）。
   ─────────────────────────────────────────────────────────────────────── */
const closedCum = ref(new Set<string>())
const openTurns = ref(new Set<number>())
const turnGroupReset = ref<{ seq: number; open: boolean }>({ seq: 0, open: true })

/** 筛选：空 = 不筛；筛中时强制展开命中项，并禁用「全部收起」 */
const filterOpen = ref(false)
const filterQuery = ref('')
const q = computed(() => filterQuery.value.trim().toLowerCase())

/** 目录索引（只在累计视角且目录 > 6 时出现） */
const indexOpen = ref(false)
const flashDir = ref<string | null>(null)
let flashTimer: ReturnType<typeof setTimeout> | null = null

const scrollEl = ref<HTMLElement | null>(null)

/** 待定位的 Turn（数据还没到 / 面板还没可见时先记下来，等渲染好再滚） */
const pendingFocus = ref<number | null>(null)

/** 默认展开最新一轮；后续新出现的轮次也自动展开（用户手动收起的不强行拉回） */
watch(
  () => turnsDesc.value.map((t) => t.turn).join(','),
  () => {
    const latest = turnsDesc.value[0]?.turn
    if (latest == null) return
    if (!openTurns.value.has(latest)) {
      openTurns.value = new Set([...openTurns.value, latest])
    }
  },
  { immediate: true },
)

/**
 * 切任务：视图状态复位（不同任务的 Turn 号没有可比性）。
 *
 * ⚠️ **首次挂载不能清 openTurns**：bundle 可能已被缓存，`turnsDesc` 的 watch 在
 * 本 watch 之前（同为 immediate）已经把"最新一轮展开"加进去了；这里再清一次
 * 会把默认展开吃掉。只在**真的换任务**（上一个 id 存在且不同）时复位。
 */
let lastPanelTaskId: string | null = null
watch(
  () => props.taskId,
  () => {
    // 上一任务留下的 activeTurn 不能在这个任务里生效（Turn #1 撞号会定位到不相干的一轮）
    if (fb.activeTurnTaskId && fb.activeTurnTaskId !== props.taskId) fb.clearActiveTurn()
    if (lastPanelTaskId !== null && lastPanelTaskId !== props.taskId) {
      // 新任务的默认展开：最新一轮（bundle 若已缓存，turnsDesc 现在就是新任务的）
      const latest = turnsDesc.value[0]?.turn
      openTurns.value = latest != null ? new Set([latest]) : new Set()
      closedCum.value = new Set()
      filterOpen.value = false
      filterQuery.value = ''
      indexOpen.value = false
      openBaselinePath.value = null
      pendingFocus.value = null
      verification.value = null
    }
    lastPanelTaskId = props.taskId
  },
  { immediate: true },
)

const totalFiles = computed(() => cumulativeFiles.value.length)
const totalDirs = computed(() => cumulativeGroups.value.length)
const matchedFiles = computed(() =>
  q.value ? cumulativeFiles.value.filter((f) => f.path.toLowerCase().includes(q.value)).length : totalFiles.value,
)
const matchedDirs = computed(() =>
  q.value
    ? cumulativeGroups.value.filter(
        (g) => g.dir.toLowerCase().includes(q.value) || g.files.some((f) => f.path.toLowerCase().includes(q.value)),
      ).length
    : totalDirs.value,
)
const hasMatches = computed(() => !q.value || matchedDirs.value > 0)

const countLabel = computed(() => {
  if (q.value) return `${matchedFiles.value}/${totalFiles.value} 个文件 · ${matchedDirs.value}/${totalDirs.value} 个目录`
  return `${view.value === 'event' ? '共 ' : ''}${totalFiles.value} 个文件 · ${totalDirs.value} 个目录`
})

/** 累计视角下按筛选裁剪：目录命中 → 全留；只命中文件 → 只留命中的行 */
const visibleCumulativeGroups = computed(() => {
  if (!q.value) return cumulativeGroups.value
  return cumulativeGroups.value
    .map((g) => {
      const dirMatch = g.dir.toLowerCase().includes(q.value)
      const rows = dirMatch ? g.rows : g.rows.filter((r) => r.fc.path.toLowerCase().includes(q.value))
      return { ...g, rows, visible: dirMatch || rows.length > 0 }
    })
    .filter((g) => g.visible)
})

const showDirIndex = computed(() => view.value === 'baseline' && totalDirs.value > 6)
const visibleDirIndex = computed(() => {
  if (!q.value) return cumulativeGroups.value
  return cumulativeGroups.value.filter(
    (g) => g.dir.toLowerCase().includes(q.value) || g.files.some((f) => f.path.toLowerCase().includes(q.value)),
  )
})

const openTurnCount = computed(() => turnsDesc.value.filter((t) => openTurns.value.has(t.turn)).length)
const allClosed = computed(() => {
  if (view.value === 'baseline') {
    return totalDirs.value > 0 && cumulativeGroups.value.every((g) => closedCum.value.has(g.dir))
  }
  return openTurnCount.value === 0
})

function isDirOpen(dir: string): boolean {
  if (q.value) return true
  return !closedCum.value.has(dir)
}

function toggleDir(dir: string): void {
  const next = new Set(closedCum.value)
  if (next.has(dir)) next.delete(dir)
  else next.add(dir)
  closedCum.value = next
}

function toggleTurn(turn: number): void {
  const next = new Set(openTurns.value)
  if (next.has(turn)) next.delete(turn)
  else next.add(turn)
  openTurns.value = next
}

/**
 * 一键收起 / 展开。
 * 本轮视角：连 Turn 一起收（用户 2026-10 明确要求），展开时把各 Turn 内的目录也重置为全开；
 * 累计视角：只操作目录分组。
 */
function toggleAll(): void {
  if (q.value) return
  if (view.value === 'baseline') {
    closedCum.value = allClosed.value ? new Set() : new Set(cumulativeGroups.value.map((g) => g.dir))
    return
  }
  if (allClosed.value) {
    openTurns.value = new Set(turnsDesc.value.map((t) => t.turn))
    turnGroupReset.value = { seq: turnGroupReset.value.seq + 1, open: true }
  } else {
    openTurns.value = new Set()
    turnGroupReset.value = { seq: turnGroupReset.value.seq + 1, open: false }
  }
}

function toggleFilter(): void {
  filterOpen.value = !filterOpen.value
  if (!filterOpen.value) filterQuery.value = ''
}

/** 该路径的新标签入口；无产物登记时返回 null。html/image 给外链，其余走查看层 */
function openLinkOf(path: string): { href: string; label: string } | null {
  const a = artifactsByPath.value.get(path)
  if (!a) return null
  if (a.kind === 'html') return { href: a.open, label: '打开' }
  if (a.kind === 'image') return { href: a.open, label: '查看' }
  return null
}

/** md/code/text/pdf → 应用内只读查看层（单一入口，不再内联预览） */
function viewFile(path: string): void {
  artifactsStore.openPath(props.taskId, path)
}

function jumpToDir(dir: string): void {
  indexOpen.value = false
  const next = new Set(closedCum.value)
  next.delete(dir)
  closedCum.value = next
  void nextTick(() => {
    const el = scrollEl.value?.querySelector<HTMLElement>(`[data-dir="${CSS.escape(dir)}"]`)
    el?.scrollIntoView({ block: 'start', behavior: 'smooth' })
    flashDir.value = dir
    if (flashTimer) clearTimeout(flashTimer)
    flashTimer = setTimeout(() => (flashDir.value = null), 900)
  })
}

/**
 * 贴边条上的变更文件数 —— **收起态自带的"要不要展开"判据**。
 *
 * 取后端 `cumulative.files`（走 `git diff --numstat` 与基线对比），
 * 而不是自己数 `turns[].changes` 里的去重路径：后者只覆盖**有快照的 Turn**，
 * 老任务 / 未落快照的轮次会数出 0，贴边条就会在明明有改动时显示"没有值得看的"。
 */
const fileCount = computed(() => bundle.value?.cumulative?.files ?? 0)

/** Continue 后 Agent 已在跑：与回退同用静止边界判断（面板仅查看） */
const canContinue = computed(() => props.canRewind)

/** 收起态（仅 dock 形态有 46px 贴边条这一说） */
const railOnly = computed(() => props.mode === 'dock' && fb.collapsed)
/**
 * 是否需要/允许拉数据。
 * dock 形态**无论收没收起都要拉** —— 贴边条上的文件数就来自这份数据。
 */
const shouldLoad = computed(() => props.mode === 'dock' || props.active)
/** 面板主体是否渲染（贴边条态下整个主体让位给那条 46px） */
const bodyOn = computed(() => (props.mode === 'dock' ? !fb.collapsed : props.active))

const baselineLine = computed(() => {
  const sha = bundle.value?.baseline?.head_sha
  return sha ? `baseline ${sha.slice(0, 7)}` : ''
})

/**
 * 拉反馈数据。
 * force=false：有缓存就跳过（面板初次可见 / Timeline 已拉过）；
 * force=true：回退、手动刷新后必须看到新数字。
 *
 * artifacts 仅在面板主体可见时拉 —— dock 贴边条只需要 bundle 的 fileCount，
 * 不必为一条计数多打 /artifacts。
 */
async function refresh(force = true) {
  if (!props.taskId) return
  loading.value = true
  try {
    const jobs: Promise<void>[] = [bundleStore.load(props.taskId, force)]
    if (bodyOn.value) jobs.push(artifactsStore.load(props.taskId, force))
    await Promise.all(jobs)
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

// 可见（或需要贴边条计数）时拉取；taskId 变化时重拉。
// 初载 force=false：吃 Timeline / 上次面板的缓存，避免每次展开都双打接口。
watch(
  () => [shouldLoad.value, props.taskId] as const,
  ([on]) => {
    if (on) void refresh(false)
  },
  { immediate: true },
)

// dock：从贴边条展开后补拉 artifacts（初载只拉了 bundle）
watch(
  () => bodyOn.value,
  (on) => {
    if (on && props.taskId) void artifactsStore.load(props.taskId, false)
  },
)

/**
 * AC-R3-06：Timeline「查看本轮变更」→ 变更 Tab（本轮变化视图）滚动定位到对应 TurnCard。
 *
 * 三档落点不同：宽屏展开面板、平板打开抽屉、移动端切到反馈面板（由 SessionPage 负责），
 * 这里只负责"面板真的可见之后"的定位 —— 所以 `bodyOn` 也在依赖里。
 *
 * 数据可能还没到（移动端刚切过来）：此时把 turn 记进 pendingFocus，等 turns 渲染出来再滚。
 * 注意**不要**把 turns 长度直接作为依赖去重复滚动：用户正读着，新来一轮不该把他拽走。
 */
function scrollToTurn(turn: number): boolean {
  const el = scrollEl.value?.querySelector<HTMLElement>(`[data-turn="${turn}"]`)
  if (!el) return false
  el.scrollIntoView({ block: 'start', behavior: 'smooth' })
  return true
}

watch(
  [() => fb.activeTurn, bodyOn],
  async ([turn, on]) => {
    if (!turn || !on) return
    // 选中可能来自另一个任务（store 跨路由保留）：任务 id 对不上就不定位
    if (fb.activeTurnTaskId !== props.taskId) return
    tab.value = 'changes'
    view.value = 'event'
    const next = new Set(openTurns.value)
    next.add(turn)
    openTurns.value = next
    // 目标轮的所有目录也展开：用户点的是"看这轮的文件"，只开 Turn 头等于没响应
    turnGroupReset.value = { seq: turnGroupReset.value.seq + 1, open: true }
    await nextTick()
    if (!scrollToTurn(turn)) pendingFocus.value = turn
  },
  { immediate: true },
)

watch(
  [() => turnsDesc.value.length, bodyOn],
  async () => {
    const turn = pendingFocus.value
    if (turn == null || !bodyOn.value) return
    await nextTick()
    if (scrollToTurn(turn)) pendingFocus.value = null
  },
)

/** 目录索引点外关闭 */
function onDocClick(e: MouseEvent): void {
  if (!indexOpen.value) return
  const t = e.target as HTMLElement | null
  if (t?.closest('[data-dir-index]') || t?.closest('[data-dir-index-btn]')) return
  indexOpen.value = false
}

onMounted(() => document.addEventListener('click', onDocClick))
onBeforeUnmount(() => {
  document.removeEventListener('click', onDocClick)
  if (flashTimer) clearTimeout(flashTimer)
})

/** 切走变更 Tab / 切视角时收起目录索引（否则会浮在别的视图上） */
watch([tab, view], () => {
  indexOpen.value = false
})
</script>

<template>
  <div
    class="flex h-full min-h-0 flex-col bg-surface-subtle"
    :class="mode === 'drawer' ? 'w-[420px] shrink-0' : 'w-full'"
    data-testid="feedback-panel"
  >
    <!-- 收起态：46px 竖向贴边条。 -->
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
      <svg class="h-4 w-4 shrink-0 text-text-tertiary" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
        <path d="M15 18l-6-6 6-6" stroke-linecap="round" stroke-linejoin="round" />
      </svg>
      <span class="text-[12px] font-semibold tracking-[.14em] text-text-secondary [writing-mode:vertical-rl]">变更反馈</span>
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
          @click="() => refresh(true)"
        >↻</button>
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

      <!-- ── 变更 Tab：固定视角开关 + 工具条 + 筛选，下面是唯一滚动区 ───── -->
      <template v-if="tab === 'changes'">
        <div class="shrink-0 px-3.5 pt-2.5">
          <SegmentedControl v-model="view" :options="VIEWS" fill aria-label="变更视角" />
        </div>

        <div class="relative shrink-0 px-3.5 pt-2">
          <div class="flex items-center gap-1.5">
            <span class="min-w-0 flex-1 truncate text-[10.5px] text-text-tertiary" data-testid="fb-count">{{ countLabel }}</span>
            <button
              type="button"
              class="shrink-0 rounded-md border border-border bg-surface px-2 py-0.5 text-[10.5px] text-text-secondary transition-colors hover:border-accent/40 hover:text-accent disabled:cursor-default disabled:opacity-40"
              :disabled="!!q || (view === 'event' ? !turnsDesc.length : !totalDirs)"
              :title="q ? '筛选时自动展开；清除筛选后可一键收起' : ''"
              data-testid="fb-toggle-all"
              @click="toggleAll"
            >{{ allClosed ? '全部展开' : '全部收起' }}</button>
            <button
              v-if="showDirIndex"
              type="button"
              data-dir-index-btn
              class="shrink-0 rounded-md border border-border bg-surface px-1.5 py-0.5 text-[10.5px] text-text-secondary transition-colors hover:border-accent/40 hover:text-accent"
              :aria-expanded="indexOpen"
              title="跳到目录"
              @click="indexOpen = !indexOpen"
            >目录 {{ totalDirs }} ▾</button>
            <button
              type="button"
              class="shrink-0 rounded-md px-1.5 py-0.5 text-[10.5px] transition-colors"
              :class="filterOpen ? 'bg-accent/10 text-accent' : 'text-text-tertiary hover:text-text'"
              :aria-expanded="filterOpen"
              title="筛选目录 / 文件"
              data-testid="fb-filter-toggle"
              @click="toggleFilter"
            >
              <span class="inline-flex items-center gap-1">
                <svg viewBox="0 0 24 24" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
                  <circle cx="11" cy="11" r="7" /><path d="M20 20l-3.6-3.6" />
                </svg>
                筛选
              </span>
            </button>
          </div>

          <div
            v-if="indexOpen"
            data-dir-index
            class="absolute inset-x-3.5 top-full z-20 mt-1 max-h-60 overflow-y-auto rounded-lg border border-border bg-surface p-1 shadow-md"
          >
            <button
              v-for="g in visibleDirIndex"
              :key="g.dir || '.'"
              type="button"
              class="flex w-full items-center gap-2 rounded px-1.5 py-1 text-left hover:bg-surface-muted"
              :title="g.label"
              @click="jumpToDir(g.dir)"
            >
              <span class="min-w-0 flex-1 truncate font-mono text-[10.5px] text-text-secondary">{{ g.label }}</span>
              <span class="shrink-0 font-mono text-[10px] text-text-tertiary">{{ g.files.length }}</span>
              <span v-if="g.additions" class="shrink-0 font-mono text-[10px] text-success">+{{ g.additions }}</span>
              <span v-if="g.deletions" class="shrink-0 font-mono text-[10px] text-error">-{{ g.deletions }}</span>
            </button>
            <div v-if="!visibleDirIndex.length" class="px-2 py-2 text-[10.5px] text-text-tertiary">没有匹配的目录</div>
          </div>
        </div>

        <div v-if="filterOpen" class="shrink-0 px-3.5 pt-2">
          <label class="flex h-7 items-center gap-1.5 rounded-md border border-border bg-surface px-2 focus-within:border-accent/55">
            <svg viewBox="0 0 24 24" class="h-3 w-3 shrink-0 text-text-tertiary" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
              <circle cx="11" cy="11" r="7" /><path d="M20 20l-3.6-3.6" />
            </svg>
            <input
              v-model="filterQuery"
              type="text"
              class="min-w-0 flex-1 bg-transparent text-[11px] text-text outline-none placeholder:text-text-disabled"
              placeholder="筛选目录 / 文件…"
              spellcheck="false"
              data-testid="fb-filter-input"
            />
            <button
              v-if="filterQuery"
              type="button"
              class="grid h-4 w-4 shrink-0 place-items-center rounded text-[13px] leading-none text-text-tertiary hover:bg-surface-muted hover:text-text"
              title="清除筛选"
              @click="filterQuery = ''"
            >×</button>
          </label>
        </div>

        <div id="fbPanel" ref="scrollEl" class="min-h-0 flex-1 overflow-y-auto px-3.5 py-2">
          <div v-if="loading && !bundle" class="flex items-center justify-center gap-2 py-10 text-xs text-text-tertiary">
            <Spinner class="h-4 w-4" /> 加载中…
          </div>

          <template v-else-if="bundle">
            <!-- 本轮变化：Turn 卡片（最新一轮默认展开；「全部收起」连 Turn 一起收） -->
            <div v-if="view === 'event'" class="flex flex-col gap-2">
              <TurnCard
                v-for="t in turnsDesc"
                :key="`${taskId}-${t.turn}`"
                :task-id="taskId"
                :turn="t"
                :artifacts="artifactsByPath"
                :checkpointed="checkpointSet.has(t.turn)"
                :can-rewind="canRewind && rewinding === null && rewindingFile === null"
                :highlighted="fb.activeTurnTaskId === taskId && fb.activeTurn === t.turn"
                :expanded="openTurns.has(t.turn)"
                :filter="filterQuery"
                :group-reset="turnGroupReset"
                @toggle="toggleTurn(t.turn)"
                @rewind="onRewind"
                @rewind-file="onRewindFile"
              />
              <div v-if="!turnsDesc.length" class="py-6 text-center text-xs text-text-tertiary">暂无 Turn 记录</div>
              <div v-else-if="q && !hasMatches" class="py-6 text-center text-xs text-text-tertiary">没有匹配的文件</div>
            </div>

            <!-- 累计变化：按目录分组的完整文件集（默认全展开） -->
            <div v-else class="flex flex-col gap-2">
              <div v-if="!cumulativeFiles.length" class="py-6 text-center text-xs text-text-tertiary">暂无累计变更</div>
              <div v-else-if="q && !hasMatches" class="py-6 text-center text-xs text-text-tertiary">没有匹配的文件</div>
              <section
                v-for="g in visibleCumulativeGroups"
                :key="g.dir || '.'"
                :data-dir="g.dir"
                class="rounded-lg border border-border/60 bg-surface/60"
                :class="flashDir === g.dir ? 'ring-2 ring-accent/50' : ''"
              >
                <button
                  v-if="g.grouped"
                  type="button"
                  class="sticky top-0 z-[1] flex min-h-10 w-full items-center gap-2 rounded-t-lg border-b border-border/40 bg-surface px-3 py-1.5 text-left text-[11px] hover:bg-surface-muted"
                  :aria-expanded="isDirOpen(g.dir)"
                  :title="g.dir ? `展开/收起 ${g.dir}` : '展开/收起仓库根目录下的文件'"
                  @click="toggleDir(g.dir)"
                >
                  <span class="shrink-0 text-text-tertiary transition-transform" :class="isDirOpen(g.dir) ? '' : '-rotate-90'">▾</span>
                  <span class="min-w-0 flex-1 truncate font-mono" :class="isDirOpen(g.dir) ? 'text-text' : 'text-text-secondary'">{{ g.label }}</span>
                  <span class="shrink-0 text-text-tertiary">{{ g.files.length }} 个</span>
                  <span v-if="g.additions" class="shrink-0 font-mono text-success">+{{ g.additions }}</span>
                  <span v-if="g.deletions" class="shrink-0 font-mono text-error">-{{ g.deletions }}</span>
                </button>
                <div v-if="!g.grouped || isDirOpen(g.dir)">
                  <div v-for="row in g.rows" :key="row.fc.path" class="border-b border-border/30 last:border-b-0">
                    <div class="flex items-center gap-1.5">
                      <button
                        type="button"
                        class="flex min-w-0 flex-1 items-center gap-2 px-3 py-1.5 text-left text-xs hover:bg-surface-muted"
                        @click="openBaselinePath = openBaselinePath === row.fc.path ? null : row.fc.path"
                      >
                        <span class="min-w-0 flex-1 truncate font-mono text-text-secondary" :title="row.fc.path">{{ row.name }}</span>
                        <span v-if="row.fc.additions" class="shrink-0 font-mono text-success">+{{ row.fc.additions }}</span>
                        <span v-if="row.fc.deletions" class="shrink-0 font-mono text-error">-{{ row.fc.deletions }}</span>
                        <span class="shrink-0 text-text-tertiary transition-transform" :class="openBaselinePath === row.fc.path ? '' : '-rotate-90'">▾</span>
                      </button>
                      <a
                        v-if="row.open"
                        :href="row.open.href"
                        target="_blank"
                        rel="noopener"
                        class="shrink-0 px-2.5 py-1 text-xs text-accent hover:underline"
                        :title="`新标签打开 ${row.fc.path}`"
                      >{{ row.open.label }}</a>
                      <button
                        v-if="row.canView"
                        type="button"
                        class="shrink-0 px-2.5 py-1 text-xs text-text-tertiary hover:text-accent"
                        :title="`查看 ${row.fc.path}`"
                        @click="viewFile(row.fc.path)"
                      >查看</button>
                    </div>
                    <!-- turn 省略 = Baseline 累计 diff -->
                    <DiffView v-if="openBaselinePath === row.fc.path" :task-id="taskId" :path="row.fc.path" />
                  </div>
                </div>
              </section>
            </div>
          </template>

          <div v-else class="py-10 text-center text-xs text-text-tertiary">暂无数据</div>
        </div>
      </template>

      <!-- ── 其余 Tab：保持原来的整块滚动 ─────────────────────────────── -->
      <div v-else id="fbPanel" class="min-h-0 flex-1 overflow-y-auto px-3.5 py-3">
        <div v-if="loading && !bundle" class="flex items-center justify-center gap-2 py-10 text-xs text-text-tertiary">
          <Spinner class="h-4 w-4" /> 加载中…
        </div>

        <template v-else-if="bundle">
          <template v-if="tab === 'overview'">
            <OutcomeCard :task-id="taskId" />
            <div
              v-if="baselineLine"
              class="mt-3 flex items-center gap-1.5 rounded-lg border border-border/60 bg-surface/60 px-3 py-2 text-[11.5px] text-text-tertiary"
            >
              <span class="font-mono">{{ baselineLine }}</span>
              <span class="ml-auto">{{ turnsDesc.length }} 个 Turn</span>
            </div>
            <div class="mt-3"><EvidenceCard :task-id="taskId" :can-continue="canContinue" /></div>
          </template>

          <ChecksPanel v-else-if="tab === 'checks'" :task-id="taskId" />

          <PreviewSection v-else :task-id="taskId" />
        </template>

        <div v-else class="py-10 text-center text-xs text-text-tertiary">暂无数据</div>
      </div>
    </template>
  </div>
</template>
