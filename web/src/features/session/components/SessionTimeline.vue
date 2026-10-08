<script setup lang="ts">
// Session Timeline（方案 §17/§39）：滚动容器 + 事件流 + 思考占位。
// 滚动策略：底部跟随 / 强制到底（切换会话、提交后）/ 翻历史不打断。
//
// R3 Turn 分组（AC-R3-01~06）：边界从事件流的 user_message 派生（与后端
// StartEventSeq 同源，见 groupTurns.ts 头注释）；TurnInfo 只做头部增强。
// 旧任务无 user_message → groups 为空 → 平铺兜底（AC-R3-04）。
//
// 详情页 UI 优化 · 需求 1：**一轮结束后自动收起"过程"，但提示词与结果始终可见**。
// 分类与时机见下方 PROCESS_TYPES / autoFolded 两处注释 —— 判据放在本组件而不是
// groupTurns（那是纯分组逻辑，不带"何时折叠"的 UI 语义）。
//
// 详情页 UI 优化 · 需求 2：**只挂载最近两个 Turn，更早的按需挂载**。
// 判定见下方 MOUNT_WINDOW —— 折叠只是视觉上收起了过程，那批事件仍在 DOM 里，
// 所以真正决定首次渲染代价的是"挂载了几个 Turn"。
//
// 详情页 UI 优化 · 需求 3：**上拉查历史时浮出「直达最新输出」**。
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { Ref } from 'vue'
import { useSessionStore } from '@/stores/session'
import { useTaskStore } from '@/stores/task'
import { useFeedbackBundleStore } from '@/stores/feedbackBundle'
import { useFeedbackPanelStore } from '@/stores/feedbackPanel'
import { useTimelineScroll, BOTTOM_SLACK } from '@/composables/useSession'
import { isTerminalStatus } from '@/types/task'
import { groupEventsByTurn, countUserMessages } from '../groupTurns'
import type { TurnGroup, TurnRailItem } from '../groupTurns'
import type { AgentEvent, AgentEventType } from '@/types/event'
import TimelineEventView from '@/features/timeline/components/TimelineEventView.vue'
import ThinkingBadge from '@/features/timeline/components/ThinkingBadge.vue'
import TextBubble from '@/features/timeline/components/TextBubble.vue'
import TurnRail from './TurnRail.vue'

const props = defineProps<{
  taskId: string
  /** 强制滚动标记消费函数（由 useSession 提供） */
  consumeForceScroll: () => boolean
}>()

const sessionStore = useSessionStore()
const taskStore = useTaskStore()
const bundleStore = useFeedbackBundleStore()
const fb = useFeedbackPanelStore()

const events = computed(() => sessionStore.events(props.taskId))
const task = computed(() => taskStore.byId(props.taskId))
const bundle = computed(() => bundleStore.bundle(props.taskId))

/** 思考占位：提交后标记 / 冷启动（运行中且无事件无输出）兜底 */
const showThinking = computed(() => {
  if (!task.value) return false
  const active = task.value.status === 'running' || task.value.status === 'pending'
  if (!active) return false
  if (sessionStore.isThinking(task.value.id)) return true
  return events.value.length === 0 && !task.value.output
})

/** 旧任务兜底：无 events 但有 output */
const outputFallback = computed(() => events.value.length === 0 && !!task.value?.output)

// ---- Turn 分组 ----

const groups = computed(() => groupEventsByTurn(events.value, bundle.value?.turns))
const userMsgCount = computed(() => countUserMessages(events.value))

// 初载拉 bundle（失败静默：分组照常渲染，增强等面板侧的重试 —— 那边有 toast）
watch(
  () => props.taskId,
  (id) => {
    bundleStore.load(id).catch(() => {})
  },
  { immediate: true },
)

// bundle 落后于事件流（新一轮 user_message 已进流但 turns 还没它）→ 增量重拉（AC-R3-05）
watch(userMsgCount, (n) => {
  if (n > (bundle.value?.turns.length ?? 0)) bundleStore.load(props.taskId, true).catch(() => {})
})

/** 收起的 Turn 集合 —— 按 turn 号记账，新分组出现不影响已有开关状态（AC-R3-05） */
const folded = ref(new Set<number>())
function toggleFold(turn: number) {
  const next = new Set(folded.value)
  if (next.has(turn)) next.delete(turn)
  else next.add(turn)
  folded.value = next
}

// ---- 需求 1：一轮完成后自动折叠"过程" ----

/**
 * **过程**类事件 = 这一轮"怎么做的"：思考、工具调用与结果。
 * 其余（user_message = 提示词、text_delta = 结果正文、status / completed / error /
 * rewind）都留在可见区 —— 折叠的是过程，不是这一轮的交代。
 */
const PROCESS_TYPES: ReadonlySet<AgentEventType> = new Set(['thinking_delta', 'tool_call', 'tool_result'])
const isProcess = (e: AgentEvent) => PROCESS_TYPES.has(e.type)

/** 该轮的过程事件条数（折叠态头部要报给用户"收起了 N 条工具调用"） */
function processCount(g: TurnGroup): number {
  return g.events.reduce((n, e) => (isProcess(e) ? n + 1 : n), 0)
}

/**
 * 该轮（去掉开头的提示词气泡后）是否以**正文**收尾。
 *
 * 这是区分"本轮已经交付了结果"与"正文只是过场、后面还有工具调用"的关键：
 * agent 常见的节奏是 [正文"我来看看…"] [工具调用] [正文=最终结果]，按
 * "组内有 text_delta 就展开"会把前一轮的过场正文误当成结果，于是收不起来。
 */
function endsWithText(g: TurnGroup): boolean {
  const tail = g.turn > 0 ? g.events.filter((e) => e.type !== 'user_message') : []
  return tail.at(-1)?.type === 'text_delta'
}

/**
 * 该轮是否"已交出结果"（= 该自动折叠）。
 *
 * 两个来源缺一不可：
 *   1. 用户已发下一轮（存在更大的 turn）→ 前一轮必然结束，哪怕它只是被中止
 *      （事件流里没有 completed，但用户已经翻篇了）；
 *   2. 任务进终态且这一轮以正文收尾 → 最后一轮也收起来。
 * 只看 (2) 会让「连问几轮」的过程全部摊开，只看 (1) 则最后一轮永远不折。
 */
function hasProduced(g: TurnGroup): boolean {
  return (groups.value.at(-1)?.turn ?? 0) > g.turn || (!!task.value && isTerminalStatus(task.value.status) && endsWithText(g))
}

/**
 * 已经**自动**折叠过的 Turn 号（单调集合）。
 *
 * 它守的是一条纪律：**用户手动展开后，不许再被自动折叠吞回去**。
 * 折叠只由"这一轮首次变成已完成"触发一次 —— 之后 `folded` 完全归用户，
 * 否则每次事件追加（流式正文、下一轮的 delta）都会把用户刚展开的 Turn 再折上，
 * 表现就是"点了没反应"。手动折叠不需要记账（folded 本身就是用户意图）。
 */
const autoFolded = ref(new Set<number>())

watch(
  [groups, () => task.value?.status],
  () => {
    const nextFolded = new Set(folded.value)
    const nextAuto = new Set(autoFolded.value)
    let changed = false
    for (const g of groups.value) {
      // turn 0 是前导区（首条提示词之前的系统事件），没有"提示词 + 结果"可留，不参与
      if (g.turn === 0 || nextAuto.has(g.turn) || !hasProduced(g)) continue
      // 只有真的收起了东西才记"已自动折叠"：全是正文的一轮标记它，反而会
      // 让用户之后手动折叠再展开时，被这个已置位的标记挡住（无法再自动折）
      if (processCount(g) === 0) continue
      nextAuto.add(g.turn)
      if (!nextFolded.has(g.turn)) nextFolded.add(g.turn)
      changed = true
    }
    if (changed) {
      folded.value = nextFolded
      autoFolded.value = nextAuto
    }
  },
  { immediate: true, deep: true },
)

// ---- 需求 2：只挂载最近两个 Turn ----

/**
 * 首次挂载保留几个 Turn，更早的收进「更早的 N 轮」。
 *
 * **为什么是 2**：一个 Turn 的全部过程事件原样留在 DOM 里（折叠用 hidden，
 * 不是 v-if —— 见下方模板注释），所以"折叠过的长会话"其实一点也没省下渲染。
 * 而用户点进详情页要看的**永远是最新那一轮的输出**，历史放在那里只是"以防万一"。
 * 2 而不是 1：留一轮上下文，"上一轮说了什么"是读最新结果时最常回看的东西，
 * 只挂 1 轮会让人每读一次结果都先点一次"展开"。
 *
 * 与"只加载最近 4 个任务"同一个判据：**默认渲染的应该是"多数时候要看的那部分"**，
 * 其余交给一个明确、便宜、可逆的展开动作。
 */
const MOUNT_WINDOW = 2

/**
 * 判断一个 Turn 是否"太大，值得先拦一道"。
 *
 * 这是"要不要做这件事"的核心取舍，不是可选优化：
 * 挂载窗口本身有代价 —— 向后翻历史时多一次点击，且需要重建滚动位置。
 * 代价能换来什么，取决于被省掉的那部分有多重：
 *   - 100 个 Turn 的长会话：省下的是 98 个 Turn 的 DOM（含全部工具卡片 /
 *     Markdown / diff），首次渲染从"几秒的白屏"变成"一次排版" —— 很值。
 *   - 3 个 Turn 的短会话：省下的 DOM 微乎其微，用户却凭空多一次点击 —— 不值。
 * 所以窗口**只对"确实是长会话"的长会话生效**：
 *   - **分组数 > MOUNT_WINDOW**：有东西可省（2 轮以内整体挂载，与改动前无差别）；
 *   - **已加载事件数超过门槛**：轮数少但每轮巨长（几十条工具调用）同样很重，
 *     不过"重不重"要看事件量，不能只看轮数。
 * 两条都不满足 → 返回 null = 不截断。
 *
 * ⚠️ 判据是 `groups.length` 而**不是轮数**：前导区（turn 0，首条 user_message
 * 之前的系统事件）也占一个分组。所以"N 轮 ⇒ N 组"只在事件流以 user_message
 * 开头时成立；带前导的会话会多一组。两者都属正常，窗口按分组数走即可。
 */
const HEAVY_EVENT_THRESHOLD = 160
const mountWindow = computed<number | null>(() =>
  railTurns.value.length > MOUNT_WINDOW || events.value.length > HEAVY_EVENT_THRESHOLD
    ? MOUNT_WINDOW
    : null,
)

/**
 * 是否展示全部 Turn。
 *
 * 三态而不是布尔，且**默认态必须由 `mountWindow` 决定**（null → 展开）：
 * 一个"用户没表过态、数据也不长"的会话不该被拦，所以它不能是 `ref(false)`。
 * 这里存的是**用户的显式选择**（null = 还没选过）。
 * 换会话要复位 —— 上一任务的"展开全部"不是这一任务的意图。
 */
const expandAll = ref<boolean | null>(null)
const allMounted = computed(() => (mountWindow.value === null ? true : expandAll.value === true))

/** 首个挂载的 Turn 序号（`groups` 的索引；-1 = 全部挂载） */
const mountFrom = computed(() => {
  if (allMounted.value) return -1
  return Math.max(0, groups.value.length - MOUNT_WINDOW)
})

/** 被收起的更早轮数（= 挂载窗口之前的 Turn 组数，含前导区 turn 0） */
const olderCount = computed(() => (mountFrom.value > 0 ? mountFrom.value : 0))

/** 需要"补挂"的 Turn：**包含当前正在看的那一轮**、之前已挂载的、以及用户点开的。
 *  三者取最大，保证这些轮永远不会因为窗口滑动而被卸载。 */
const pinnedTurn = ref(0)

function pin(turn: number) {
  if (turn > pinnedTurn.value) pinnedTurn.value = turn
}

/** 是否挂载第 i 个分组（i 是 groups 的下标） */
function isMounted(i: number): boolean {
  if (mountFrom.value < 0) return true
  if (i >= mountFrom.value) return true
  return groups.value[i]?.turn !== 0 && groups.value[i].turn <= pinnedTurn.value
}

/**
 * 展开更早的 N 轮。
 *
 * 先把当前可见的那一轮钉住：**在列表上方插入内容会把内容推下去**，
 * 浏览器为了保持视觉位置会自动补偿 scrollTop —— 但只在内容确实位于
 * 视口上方时成立。钉住当前轮是这一动作对"我正在读第 7 轮"的承诺：
 * 展开历史不该让我丢掉正在读的位置。
 */
function showOlder() {
  pin(railTurns.value.at(-1)?.turn ?? 0)
  expandAll.value = true
}

/** 当前正在读的 Turn 变深时钉住它（翻历史过程中窗口会跟着往下滑时不至于卸载）。
 *  ⚠️ 这条 watch 必须待在 `activeTurn` 的声明**之后**（见下方 trackActive 一节）——
 *  `watch()` 的第一个参数在这里是求值后的值，提前引用会直接
 *  `ReferenceError: Cannot access 'activeTurn' before initialization`（整个组件挂不起来）。 */

const { el, onScroll, scrollToEnd } = useTimelineScroll(
  events as unknown as Ref<unknown[]>,
  props.consumeForceScroll,
)

// 切换会话：跳到最新
watch(
  () => props.taskId,
  () => scrollToEnd(),
  { immediate: true },
)

// ---- 左侧快速跳转（详情页 UI 优化 · 需求 2） ----

/** 可跳转的 Turn（turn > 0）；仅当 ≥2 个时父模板才渲染 TurnRail */
const railTurns = computed<TurnRailItem[]>(() =>
  groups.value.filter((g) => g.turn > 0).map((g) => ({ turn: g.turn, text: g.events[0]?.payload.text ?? '' })),
)

/** 当前所在 Turn（滚动时跟踪）；null = 尚未确定 */
const activeTurn = ref<number | null>(null)

/**
 * 跟踪当前所在 Turn：以滚动容器顶部往下约 80px 为基准线，
 * 取最后一个「顶部已越过基准线」的 Turn。
 *
 * ⚠️ 逐 Turn 的 `getBoundingClientRect()` 会**强制同步布局**，是滚动里最贵的一步；
 * 长会话每 tick 十几次就是卡顿甚至白屏的来源。这里用两条来压：
 *   1. **单次查询**：`querySelectorAll` 一次拿到所有已挂载的 Turn 分组，
 *      而不是每个 Turn 各查一次（O(n) 选择器 → O(1)）；
 *   2. 提前退出：用 rect 数组从后往前找第一个越过基准线的即可。
 *
 * jsdom 下 getBoundingClientRect 全为 0 → 基准线命中所有组 → 稳定返回最后一个 Turn，且不抛错。
 */
function trackActive() {
  const root = el.value
  if (!root || railTurns.value.length === 0) {
    activeTurn.value = null
    return
  }
  const baseline = root.getBoundingClientRect().top + 80
  // 只认**已挂载**的 Turn 分组（窗口外的根本不在 DOM 里）
  const nodes = root.querySelectorAll<HTMLElement>('[data-testid^="turn-group-"]')
  let current = activeTurn.value ?? railTurns.value[0].turn
  for (const node of nodes) {
    const turn = Number(node.getAttribute('data-testid')!.slice('turn-group-'.length))
    if (!turn) continue // turn 0 前导区不参与跳转
    if (node.getBoundingClientRect().top <= baseline) current = turn
  }
  activeTurn.value = current
}

/** 点击泡泡：平滑滚动到该 Turn 分组，并同步高亮（jsdom 无 scrollTo → 降级为直接设 scrollTop） */
function jumpToTurn(turn: number) {
  const root = el.value
  if (root) {
    const node = root.querySelector<HTMLElement>(`[data-testid="turn-group-${turn}"]`)
    if (node) {
      const top = node.getBoundingClientRect().top - root.getBoundingClientRect().top + root.scrollTop
      try {
        root.scrollTo({ top, behavior: 'smooth' })
      } catch {
        root.scrollTop = top
      }
    }
  }
  activeTurn.value = turn
}

/** 当前正在读的 Turn 变深时钉住它（翻历史过程中窗口会跟着往下滑时不至于卸载）。
 *  ⚠️ 这条 watch 必须待在 `activeTurn` 的声明**之后** ——
 *  `watch()` 的第一个参数在这里是求值后的值，提前引用会直接
 *  `ReferenceError: Cannot access 'activeTurn' before initialization`（整个组件挂不起来）。 */
watch(activeTurn, (t) => {
  if (t) pin(t)
})

// 初载 / Turn 集合变化后重算当前 Turn，保证初始高亮正确。
// ⚠️ 这里**不再注册 window 级 scroll/resize 监听** —— 见 onScrollAll 的注释：
// 捕获阶段的全窗口监听会在无关容器滚动时也跑一遍每-Turn 的几何测量，
// 那是白屏的元凶。时间线自己的滚动由模板上的 @scroll.passive 驱动；
// resize 只需重算一次高亮，用节流版本即可。
onMounted(() => {
  nextTick(() => trackActive())
  window.addEventListener('resize', onResize)
})
onBeforeUnmount(() => {
  window.removeEventListener('resize', onResize)
  if (rafId !== 0) cancelAnimationFrame(rafId)
})

/** resize 只关心"基准线变了、高亮要重算"，节流到一帧一次就够 */
let rafId = 0
function onResize() {
  if (rafId !== 0) return
  rafId = requestAnimationFrame(() => {
    rafId = 0
    trackActive()
  })
}

watch(railTurns, () => {
  nextTick(() => trackActive())
})

// ---- 需求 3：直达最新输出 ----

/**
 * 是否已离开底部。
 *
 * 复用 useTimelineScroll 里那条 120px 的"贴底"判据（滚动跟随也用它）——
 * 两个东西对"在不在底部"必须给同一个答案，各写一个阈值就会出现
 * 「箭头说你在底部、而新输出没跟随」这种自相矛盾。
 * 所以返回值直接取自 `onScroll`：底部跟随仍然只有一处真相。
 *
 * 不能用 IntersectionObserver：pinnedTurn 会**卸载窗口之外**的 Turn，
 * 观察器的根是滚动容器，被观测的哨兵一旦从 DOM 里消失就再也不触发，
 * 按钮会永久卡在上一次的状态。滚动事件不依赖 DOM 结构，卸载多少次都对。
 */
const atBottom = ref(true)

/**
 * 滚动事件：底部跟随 + 当前 Turn 跟踪 + 直达按钮显隐。
 *
 * ⚠️ **必须只由时间线自己的滚动容器触发**（模板上是 `@scroll.passive`）。
 * 曾经这里还挂了 `window.addEventListener('scroll', onScrollAll, true)`，
 * 那是白屏的元凶：捕获阶段会在**任何**元素滚动时触发（输入框、反馈面板里的
 * diff 列表、外层页面…），而 `trackActive()` 每 tick 都要对每个 Turn 做一次
 * `querySelector` + `getBoundingClientRect` —— 那会强制同步布局，
 * 12 轮就是每 tick 12 次；再叠加"滚动中插入/卸载 Turn"就成了布局抖动，
 * 内容被反复重排到视口之外，看起来就是**白屏**。
 * （附带一害：`jumpToLatest()` 里也会调它，等于在自己触发的事件里重入。）
 */
function onScrollAll() {
  onScroll()
  trackActive()
  const root = el.value
  if (root) atBottom.value = root.scrollHeight - root.scrollTop - root.clientHeight < BOTTOM_SLACK
}

/**
 * 显示「直达最新输出」。
 *
 * `!atBottom` 就够了，不需要再加"内容够长"的条件：一个没得滚的会话里
 * atBottom 恒为真，按钮本来就不会出现。多写一个条件只是多一处会漂移的判据。
 */
const showJumpLatest = computed(() => !atBottom.value)

/**
 * 平滑到底。
 *
 * ⚠️ **不要在这里回手调 `onScrollAll()`**：平滑滚动会持续派发 scroll 事件，
 * 由它自己去更新 atBottom 才是唯一真相；手动再调一次既重入、又会用
 * "此刻还没滚到"的几何把 atBottom 写成 false，按钮迟迟不消失（闪一下又回来）。
 */
function jumpToLatest() {
  const root = el.value
  if (!root) return
  try {
    root.scrollTo({ top: root.scrollHeight, behavior: 'smooth' })
  } catch {
    root.scrollTop = root.scrollHeight
  }
}
</script>

<template>
  <div class="flex min-h-0 flex-1">
    <!-- 左侧快速跳转：≥2 个 Turn 才出现（移动端由 TurnRail 自身隐藏） -->
    <TurnRail
      v-if="railTurns.length > 1"
      :turns="railTurns"
      :active-turn="activeTurn"
      @jump="jumpToTurn"
    />
    <!-- 相对定位的容器：需求 3 的按钮要浮在滚动区右下角。
         不给滚动容器（el）加 relative 是刻意的 —— 那会让它成为浮层的定位祖先，
         浮层就会跟着内容滚走；挂在这一层（滚动容器的兄弟）才是"钉在视口"。
         滚动容器上的 h-full / min-w-0 / flex-1 也一个字不能动：
         TurnRail 与它都靠这几个类撑出"左侧轨道 + 右侧滚动区"的布局。 -->
    <div class="relative flex min-h-0 min-w-0 flex-1">
      <div
        ref="el"
        class="h-full min-w-0 flex-1 overflow-y-auto px-3 py-4 md:px-4"
        data-testid="session-timeline"
        @scroll.passive="onScrollAll"
      >
        <div class="mx-auto flex max-w-3xl flex-col gap-2.5">
          <!-- 需求 2：默认只挂载最近 MOUNT_WINDOW 个 Turn，更早的收进
               「更早的 N 轮」按需补挂。被收起的轮**真的不在 DOM 里**（v-if），
               这才是省下首次渲染代价的地方（折叠只藏过程，不省 DOM）。 -->
          <button
            v-if="olderCount"
            type="button"
            class="shrink-0 rounded-lg border border-border/60 bg-surface-subtle py-2 text-xs font-medium text-accent transition-colors hover:bg-surface-muted"
            data-testid="load-older-turns"
            @click="showOlder"
          >
            更早的 {{ olderCount }} 轮（点击展开）
          </button>

          <!-- 分组渲染（有 user_message 事件即分组） -->
          <template v-if="groups.length">
            <template v-for="(g, i) in groups" :key="g.turn">
              <section
                v-if="isMounted(i)"
                class="flex flex-col gap-2.5"
                :data-testid="`turn-group-${g.turn}`"
              >
                <!-- 前导区（turn 0）：首条用户消息前的系统事件，无头部 -->
                <div v-if="g.turn > 0" :data-testid="`turn-header-${g.turn}`">
                  <button
                    class="flex w-full items-center gap-2 border-t border-border/40 pt-2 text-left text-xs text-text-tertiary hover:text-text-secondary"
                    :aria-expanded="!folded.has(g.turn)"
                    @click="toggleFold(g.turn)"
                  >
                    <span class="shrink-0 font-mono font-semibold text-text-secondary">Turn #{{ g.turn }}</span>
                    <!-- 去重（需求 1）：展开态正文里的 UserBubble 已完整呈现该轮文案，
                         头部不再重复；折叠态正文被隐藏，头部才补回（截断），保证同一句只出现一次。 -->
                    <span v-if="folded.has(g.turn)" class="min-w-0 flex-1 truncate">{{
                      g.events[0]?.payload.text || '（无输入）'
                    }}</span>
                    <!-- 展开态占位：把右侧统计顶到右边，保持头部单行布局不塌 -->
                    <span v-else class="min-w-0 flex-1"></span>
                    <!-- 文件数取 TurnInfo.summary —— 与反馈面板同一份数据（AC-R3-02 同源）。
                         info 为 null（bundle 落后/缺失）就不显示数字，而不是算一个替代值。 -->
                    <span v-if="g.info" class="shrink-0 font-mono">
                      <span class="text-success">+{{ g.info.summary.additions }}</span>
                      <span class="text-error ml-1">-{{ g.info.summary.deletions }}</span>
                      <span class="ml-2">{{ g.info.summary.files }} 个文件</span>
                    </span>
                    <!-- 折叠态：只报"过程"的条数（需求 1）—— 该轮文案与结果仍在正文里可见，
                         这里说「5 条已收起」会让人以为整轮都没了 -->
                    <span v-if="folded.has(g.turn)" class="shrink-0">{{ processCount(g) }} 条过程已收起</span>
                  </button>
                  <button
                    v-if="g.info"
                    class="mt-1 text-xs text-accent hover:underline"
                    data-testid="turn-link"
                    @click="fb.focusTurn(g.turn)"
                  >
                    查看本轮变更
                  </button>
                </div>
                <!--
                  折叠只作用在"过程"事件上（需求 1）：该轮的 user_message（提示词）与
                  text_delta（结果）永远渲染。折叠时过程事件**保留在 DOM 里**用 hidden 藏起来，
                  而不是 v-if 摘掉 —— 它们自带展开态且是局部的，重建代价大于隐藏，
                  而且 hidden 不参与布局，视觉上与摘除等价。
                -->
                <template v-for="event in g.events" :key="event.id">
                  <TimelineEventView
                    v-if="!(folded.has(g.turn) && isProcess(event))"
                    :event="event"
                  />
                  <div v-else hidden :data-testid="`turn-process-hidden-${g.turn}`">
                    <TimelineEventView :event="event" />
                  </div>
                </template>
              </section>
            </template>
          </template>
          <!-- 平铺兜底：旧任务（无 user_message 事件）→ 与改动前行为一致（AC-R3-04） -->
          <template v-else>
            <TimelineEventView v-for="event in events" :key="event.id" :event="event" />
          </template>
          <!-- 旧任务兜底：直接展示累积输出 -->
          <TextBubble v-if="outputFallback" :text="task!.output!" />
          <ThinkingBadge v-if="showThinking" />
        </div>
      </div>

      <!-- 需求 3：上拉翻历史时浮出「直达最新输出」。
           定位用 sticky 而不是 absolute：absolute 会脱离文档流、需要额外给
           滚动内容垫一块占位高度，而 sticky 自己就参与布局 ——
           它先按正常流占一行（底部那 0 行高），再被粘在滚动区下沿。
           移动端（<768px）居中、桌面端贴右：手机拇指够得到中间，
           而桌面端鼠标在右半边操作，居中反而离手更远。 -->
      <div
        v-if="showJumpLatest"
        class="pointer-events-none sticky bottom-0 z-10 -mt-px flex w-full justify-center pb-2 md:justify-end md:pr-3"
      >
        <button
          type="button"
          class="pointer-events-auto inline-flex items-center gap-1.5 rounded-full border border-border bg-surface px-3 py-1.5 text-xs font-medium text-text-secondary shadow-md transition-colors hover:border-accent hover:text-accent"
          data-testid="jump-latest"
          title="回到最新输出"
          @click="jumpToLatest"
        >
          直达最新输出
          <svg
            class="h-3.5 w-3.5"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2.5"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <path d="M12 5v14M6 13l6 6 6-6" />
          </svg>
        </button>
      </div>
    </div>
  </div>
</template>
