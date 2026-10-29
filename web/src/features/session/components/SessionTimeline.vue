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
import { useArtifactsStore } from '@/stores/artifacts'
import { useTimelineScroll, BOTTOM_SLACK } from '@/composables/useSession'
import { isTerminalStatus } from '@/types/task'
import { groupEventsByTurn, countUserMessages } from '../groupTurns'
import type { TurnGroup, TurnRailItem } from '../groupTurns'
import type { AgentEvent, AgentEventType } from '@/types/event'
import TimelineEventView from '@/features/timeline/components/TimelineEventView.vue'
import ThinkingBadge from '@/features/timeline/components/ThinkingBadge.vue'
import TextBubble from '@/features/timeline/components/TextBubble.vue'
import ContentColumn from '@/components/layout/ContentColumn.vue'
import TurnRail from './TurnRail.vue'
import TurnSummary from './TurnSummary.vue'

const props = defineProps<{
  taskId: string
  /** 强制滚动标记消费函数（由 useSession 提供） */
  consumeForceScroll: () => boolean
}>()

const emit = defineEmits<{
  /** 每轮总结的「查看本轮变更」：由页面决定三档落点（宽屏/平板/移动端） */
  'focus-turn': [turn: number]
}>()

const sessionStore = useSessionStore()
const taskStore = useTaskStore()
const bundleStore = useFeedbackBundleStore()
const artifactsStore = useArtifactsStore()
const fb = useFeedbackPanelStore()

const events = computed(() => sessionStore.events(props.taskId))
const task = computed(() => taskStore.byId(props.taskId))
const bundle = computed(() => bundleStore.bundle(props.taskId))

/**
 * 每轮总结的按钮：先把面板 store 的 activeTurn 设好（宽屏展开由页面负责），
 * 再把"要定位到哪一轮"抛给页面 —— 三档落点不同（宽屏常驻 / 平板抽屉 / 移动端整屏），
 * 只有页面知道该切哪个 pane。
 */
function onTurnSummaryFocus(turn: number) {
  fb.focusTurn(turn, props.taskId)
  emit('focus-turn', turn)
}

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

// 初载拉 bundle + 产物（失败静默：分组照常渲染，增强等面板侧的重试 —— 那边有 toast）
watch(
  () => props.taskId,
  (id) => {
    bundleStore.load(id).catch(() => {})
    artifactsStore.load(id).catch(() => {})
  },
  { immediate: true },
)

// bundle 落后于事件流（新一轮 user_message 已进流但 turns 还没它）→ 增量重拉（AC-R3-05）
watch(userMsgCount, (n) => {
  if (n > (bundle.value?.turns.length ?? 0)) {
    bundleStore.load(props.taskId, true).catch(() => {})
    artifactsStore.load(props.taskId, true).catch(() => {})
  }
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

// ---- 每轮时间标记 ----
//
// ⚠️ 时间徽标**不在本组件**：它跟着内容走，渲染在每条提示词/结论气泡的**下方**
// （TimelineEventView 的 prompt-at / result-at，取事件自己的 timestamp）。
// Turn 头部曾经各挂一枚（提示词一枚、结论一枚），已按"时间属于内容"的定案撤掉：
// 头部的结论时间取的是全轮最后一条 text_delta，正文被工具调用切成多段时
// 它落不到任何一条气泡上，于是同一轮出现两个互相矛盾的时间。

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

/**
 * 自动折叠的**触发信号**（2026-10-22 性能修复）。
 *
 * ⚠️ 原先这里是 `watch([groups, status], ..., { deep: true })`。那是本文件里最贵的一处：
 * `groups` 里每个分组的 `events` 就是 `eventsBySession[taskId]` 的引用，而 `applyDelta`
 * **每条流式增量都整体替换数组** ⇒ 每帧一次对全部分组、全部事件的深度遍历比较。
 * 2950 事件的任务上，这就是主线程卡顿的直接来源。
 *
 * 现在监听一组**标量摘要**：每个 turn 取 (turn 号, 事件条数, 末条 id, 末条文本长度,
 * 过程条数) —— `hasProduced` / `endsWithText` / `processCount` 的全部输入都在里面。
 *
 * 之所以要"末条 id + 文本长度"而不是只看类型：流式正文是**追加进末条**而非新建，
 * 类型不变、条数也不变，只有末条的文本在变。
 *
 * 判据底线：**宁可多触发一次，不可漏触发** —— 漏触发的表现正是"自动折叠静默失效"，
 * 而本文件第 138-146 行那条纪律（用户手动展开后不许再被自动折回）完全靠它守。
 *
 * ⚠️ 写成 computed 而不是在 watch 里直接求值：本节位于 `groups` 声明之前，
 * 任何在 setup 期间**立即**求值 `groups` 的代码都会撞上
 * `Cannot access 'groups' before initialization`（见第 174-181 行的教训）。
 * computed 的 getter 是惰性的，首次求值发生在 watch 回调里，那时 setup 早已结束。
 */
function groupSignature(gs: TurnGroup[]): string {
  const parts: string[] = []
  for (const g of gs) {
    const last = g.events.at(-1)
    parts.push(
      `${g.turn}:${g.events.length}:${last?.id ?? ''}:${last?.payload.text?.length ?? 0}:${processCount(g)}`,
    )
  }
  return parts.join('|')
}
const groupSig = computed(() => groupSignature(groups.value))

watch(
  [groupSig, () => task.value?.status],
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
  // ⚠️ **不要**加回 deep: true —— 见上方 groupSig 的注释。
  { immediate: true },
)

// ---- 需求 2：只挂载最近两个 Turn ----
//
// ⚠️ **声明顺序有硬约束**：`groups` / `railTurns` / `activeTurn` 这一组声明在本节
// **之后**（它们和左侧轨道跟踪放在一起），而本节一开始的常量与 computed 就引用
// `groups`。computed 的 getter 是惰性求值 —— 只要第一次求值发生在组件渲染时
// （那时 setup 早已结束）就没问题；但任何在 setup 期间**立即**读它的东西
// （模块级常量求值、`watch(…, { immediate: true })`、或渲染之外的直接调用）
// 都会撞上 `Cannot access 'groups' before initialization`。
// 历史教训：这里曾有一个"按轮数/事件数决定要不要截断"的 computed，它把这类
// 初始化顺序错误吞成了"窗口不生效"，而不是抛 ReferenceError —— 查起来极慢。

/**
 * 一次挂载几个 Turn（首屏的窗口大小，也是每次「加载更早」往前补的步长）。
 *
 * **为什么是 2**：一个 Turn 的全部过程事件原样留在 DOM 里（折叠用 hidden，
 * 不是 v-if —— 见下方模板注释），所以"折叠过的长会话"其实一点也没省下渲染。
 * 而用户点进详情页要看的**永远是最新那一轮的输出**，历史放在那里只是"以防万一"。
 * 2 而不是 1：留一轮上下文，"上一轮说了什么"是读最新结果时最常回看的东西，
 * 只挂 1 轮会让人每读一次结果都先点一次"展开"。
 *
 * 同一个 2 也当步长用：挂载窗口与向前分页是**同一条取舍**
 * （"一次塞进 DOM 的量"），拆成两个数就会出现"首屏省 2 轮、一点补 10 轮"的怪事。
 *
 * 与"只加载最近 4 个任务"同一个判据：**默认渲染的应该是"多数时候要看的那部分"**，
 * 其余交给一个明确、便宜、可逆的展开动作。
 *
 * **不再有"短会话整体挂载"特例**：曾有一个 `mountWindow`（分组数 > 2 或事件数 > 160
 * 才算窗口），它把"要不要截断"与"窗口多大"混成了一个变量。实测（见 2026-10 的
 * 一轮回归）它会让 `mountFrom` 出现两种语义，`isMounted` 必须写 `mountFrom < 0`
 * 的特殊分支，而那正是"一次点击全部挂载"bug 的温床。
 * 现在截断判据只有一个、无条件：**起点 = 分组数 - MOUNT_WINDOW - 已加载页数**。
 * 短会话（≤ 2 组）算出来天然是 0，效果与"不截断"完全一样，不需要特例。
 */
const MOUNT_WINDOW = 2

/**
 * 用户的「加载更早」请求：累计要往前多挂几个分组。
 *
 * 为什么不再是"展开全部"的布尔：多轮会话里用户点「加载更早的 N 轮」，
 * 要的是**往前读一段**，不是"把全部历史一次性塞进 DOM"。
 * 旧实现 `expandAll = true` ⇒ `mountFrom = 0` ⇒ 12 轮 2120 条事件全部挂载，
 * 一次点击就把"只挂最近两轮"省下的渲染代价全还回去了 —— 正是本组件开头
 * 那段"首次渲染代价"要避免的事，只不过从首屏挪到了点击时。
 * 现在的语义是**逐页向前**：每点一次 `extra += MOUNT_WINDOW`，
 * 已挂载的那批始终留在 DOM 里（只增不减），用户连点几次也能到最前。
 *
 * 换会话要复位 —— 上一任务读到哪一轮不是这一任务的意图（见下方 watch(taskId)）。
 */
const extraOlder = ref(0)

/**
 * 首个挂载的 Turn 序号（`groups` 的索引；0 = 一直挂到最前）。
 *
 * ⚠️ **这里的 min/max 方向曾经写反过**（`Math.min(尾部起点, 用户请求)`）：
 * 用户没点过时请求 = 0 ⇒ 取 min 得 0 ⇒ `olderCount` 恒为 0 ⇒「加载更早」
 * 按钮**永远不出现**，同时 `isMounted` 又把所有轮都挂上。看上去像"窗口化没生效"，
 * 实际是两个 bug 恰好互相抵消了一半效果。正确语义是：
 *   默认起点 = **尾部窗口起点**（`length - MOUNT_WINDOW`）；
 *   用户的请求只能让它**往更早**走 ⇒ `Math.max(尾部起点 - extra, 0)`。
 * 越界天然收敛到 0，不需要另一个"已全部展开"的标志位。
 *
 * `mountWindow === null` 也**不用**单独写"全挂"分支：那种会话的分组数
 * ≤ MOUNT_WINDOW，尾部起点本来就是 0，公式自然给出 0。
 */
const mountFrom = computed(() =>
  Math.max(0, groups.value.length - MOUNT_WINDOW - extraOlder.value),
)

/** 被收起的更早轮数（= 挂载窗口之前的 Turn 组数，含前导区 turn 0） */
const olderCount = computed(() => mountFrom.value)

/** 本步「加载更早」实际会挂几轮（步长与剩余取小）—— 按钮文案与行为共用它 */
const olderStep = computed(() => Math.min(MOUNT_WINDOW, olderCount.value))

/**
 * 需要"补挂"的 Turn：**用户主动跳到过的那一轮**。
 *
 * ⚠️ 这里**只能由用户的显式动作驱动**（点轨道泡泡 / 点「更早的 N 轮」），
 * 绝不能挂到 `activeTurn` 上。曾经写成 `watch(activeTurn, t => pin(t))`，
 * 结果是：初载时 `trackActive()` 把 activeTurn 设成**最后一轮**（12），
 * 于是 pinnedTurn=12 ⇒ `isMounted` 对**所有**轮都返回 true ⇒ 窗口形同虚设，
 * 12 轮 2120 条事件全部挂载。而 jsdom 里量不出这个（它不做布局、
 * 也不体现真实渲染代价），只有真机才会暴露 —— 所以必须靠 self-test.ps1 守。
 */
const pinnedTurn = ref(0)

function pin(turn: number) {
  if (turn > pinnedTurn.value) pinnedTurn.value = turn
}

/**
 * 是否挂载第 i 个分组（i 是 groups 的下标）。
 *
 * ⚠️ 这是**模板里唯一的窗口判据入口**：`olderCount` / `olderStep` 只决定按钮文案，
 * 挂载与否一律走这里。曾经只有"前导区(turn 0)不参与轨道 pin"这一条例外，
 * 现在没有例外：窗口之前的轮**一律不挂**（点「加载更早」逐步前移窗口才是唯一入口）。
 */
function isMounted(i: number): boolean {
  // 前导区（turn 0）：有没有系统事件属于**数据形状**，不是阅读位置 ——
  // 永远挂载。它不含任何工具卡片/正文，代价可忽略；而"首条提示词之前发生过
  // 什么"（创建、切 worktree、首条 status）是理解这轮起点的上下文。
  // 旧实现用它做例外，正是因为一旦让 pinnedTurn 放行整段窗口，前导区就一起被
  // 放掉；现在窗口与 pin 分开判，这个例外可以只按"是不是 turn 0"来写。
  if (groups.value[i]?.turn === 0) return true
  if (i >= mountFrom.value) return true
  return groups.value[i] ? groups.value[i].turn <= pinnedTurn.value : false
}

/**
 * 展开更早的一页（MOUNT_WINDOW 组）。
 *
 * ⚠️ **这里不调用 `pin()`**。`pinnedTurn` 的语义是"用户**跳到过**的那一轮"，
 * `isMounted` 对 `turn <= pinnedTurn` 的轮一律放行；而点这一下时窗口里本来
 * 就含最后一轮，把 pinnedTurn 设成最后一轮 ⇒ 窗口对**所有**轮失效，
 * 表现是"点一次全部挂上" —— 正是这次要修的 bug 换条路复发（测试抓到了）。
 * 在列表上方插入内容时，可视位置由浏览器自己的 scrollTop 补偿维持；
 * 真要钉，也只能钉**窗口起点**那一轮，不是最后一轮。
 *
 * 用"累计要多挂几组"而不是"设一个绝对起点"：起点由 `groups.length` 派生，
 * 而新的一轮随时可能让它变大（会话还在跑）。记成绝对下标的话，新 Turn 一到
 * 窗口就缩回去、用户刚加载出来的历史又被卸载；累计增量没有这个问题。
 */
function showOlder() {
  extraOlder.value += MOUNT_WINDOW
}

/** 当前正在读的 Turn 变深时钉住它（翻历史过程中窗口会跟着往下滑时不至于卸载）。
 *  ⚠️ 这条 watch 必须待在 `activeTurn` 的声明**之后**（见下方 trackActive 一节）——
 *  `watch()` 的第一个参数在这里是求值后的值，提前引用会直接
 *  `ReferenceError: Cannot access 'activeTurn' before initialization`（整个组件挂不起来）。 */

const { el, onScroll, scrollToEnd } = useTimelineScroll(
  events as unknown as Ref<unknown[]>,
  props.consumeForceScroll,
)

// 切换会话：跳到最新，并复位"往前翻到哪"
// （extraOlder / pinnedTurn 都是**上一个任务**的阅读位置，带过来会让新任务
//   凭空多挂一段历史、且按钮文案与新窗口对不上）
watch(
  () => props.taskId,
  () => {
    extraOlder.value = 0
    pinnedTurn.value = 0
    scrollToEnd()
  },
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

/** 点击泡泡：平滑滚动到该 Turn 分组，并同步高亮（jsdom 无 scrollTo → 降级为直接设 scrollTop）。
 *  这是**用户的显式动作**，所以要 pin 住它 —— 目标轮可能还在窗口之外（未挂载），
 *  pin 之后 `isMounted` 才会把它补挂进来，否则下面 querySelector 找不到节点、点了没反应。 */
function jumpToTurn(turn: number) {
  pin(turn)
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

/** 当前正在读的 Turn 变深时**不动窗口** —— 见 pinnedTurn 的注释：
 *  把 pin 挂在 activeTurn 上会让初载即 pin 到最后一轮，窗口直接失效。
 *  （曾经的 `watch(activeTurn, t => pin(t))` 就是这么把 12 轮全挂上去的。） */

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
  <div class="flex min-h-0 flex-1 flex-col">
    <!-- 文件清单的**唯一入口是反馈面板**（ADR-0018 修订 + 2026-10 精简）：
         时间线每轮只留一条「本轮完成 · N 个文件」总结 + 「查看本轮变更」按钮，
         不再重复列一遍文件（那是同一份数据的第二个入口）。 -->
    <div class="flex min-h-0 flex-1">
    <!-- 左侧快速跳转：≥2 个 Turn 才出现（移动端由 TurnRail 自身隐藏） -->
    <TurnRail
      v-if="railTurns.length > 1"
      :turns="railTurns"
      :active-turn="activeTurn"
      @jump="jumpToTurn"
    />
    <!-- ⚠️ **不要**把浮层放在这里当滚动容器的兄弟。
         曾经它就是这个位置的一个 `sticky bottom-0` flex 子项，结果（真机实测）：
           · flex 行里 `align-items` 默认 stretch，按钮被**拉满整屏高**（118×446）；
           · sticky 的"粘"只对**最近的滚动祖先**生效，而兄弟节点根本不在滚动容器内，
             所以它既不粘底、又按普通 flex item 占了 `w-full` 的宽度 ⇒
             滚动区被挤到只剩 24px 宽，整页看起来就是"被按钮占满"。
         浮层必须放在**滚动容器内部**：那样它才真正 sticky 在滚动区下沿，
         且不参与外层 flex 的宽度/高度分配。见下方滚动容器末尾。 -->
    <div class="relative flex min-h-0 min-w-0 flex-1">
      <div
        ref="el"
        class="relative h-full min-w-0 flex-1 overflow-y-auto px-3 py-4 md:px-4"
        data-testid="session-timeline"
        @scroll.passive="onScrollAll"
      >
        <ContentColumn>
          <div class="flex flex-col gap-2.5">
          <!-- 需求 2：默认只挂载最近 MOUNT_WINDOW 个 Turn，更早的收进
               「更早的 N 轮」**逐页向前**补挂。被收起的轮**真的不在 DOM 里**（v-if），
               这才是省下首次渲染代价的地方（折叠只藏过程，不省 DOM）。
               文案要如实说明这一步挂多少：「全部加载」会在长会话里
               一次把几千条事件塞进 DOM，与上面的窗口化自相矛盾。 -->
          <button
            v-if="olderCount"
            type="button"
            class="shrink-0 rounded-lg border border-border/60 bg-surface-subtle py-2 text-xs font-medium text-accent transition-colors hover:bg-surface-muted"
            data-testid="load-older-turns"
            :data-older-count="olderCount"
            @click="showOlder"
          >
            加载更早的 {{ olderStep }} 轮（还有 {{ olderCount }} 轮更早）
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
                    <!-- ⚠️ 这里**不再**放「时:分」徽标（原是提示词/结论各一枚）。
                         2026-10 定案：时间属于内容，一律移到内容**下方**（见 TimelineEventView
                         的 prompt-at / result-at）。一处数据只有一个展示位，否则同一轮的
                         "几点的提示词、几点的结论"会同时出现在头部和正文两处、两个数字
                         还都是末条正文的时间（旧结论徽标取的是全轮最后一条 text_delta，
                         在正文被拆成多段时对不上任何一条气泡）。 -->
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
                <!-- 每轮总结：唯一入口是这条总结条上的按钮（跳到反馈面板对应轮次）。
                     运行中的轮次也显示（文案变「本轮进行中」），数字来自同一份 TurnInfo.summary。 -->
                <TurnSummary
                  v-if="g.turn > 0 && g.info && g.info.summary.files > 0"
                  class="mt-1"
                  :turn="g.turn"
                  :summary="g.info.summary"
                  :done="hasProduced(g)"
                  @focus="onTurnSummaryFocus"
                />
              </section>
            </template>
          </template>
          <!-- 平铺兜底：旧任务（无 user_message 事件）→ 与改动前行为一致（AC-R3-04） -->
          <template v-else>
            <TimelineEventView v-for="event in events" :key="event.id" :event="event" />
          </template>
          <!-- 旧任务兜底：直接展示累积输出 -->
          <TextBubble v-if="outputFallback" :text="task!.output!" :task-id="task!.id" />
          <ThinkingBadge v-if="showThinking" />
          </div>
        </ContentColumn>

          <!-- 需求 3：上拉翻历史时浮出「回到最新」小箭头。
               ⚠️ 位置有讲究，三处都不能改：
               1. **必须待在滚动容器（el）内部**：sticky 只对最近的滚动祖先生效，
                  放到容器外就既不粘底、又会被当成 flex 子项拉满高度并被抢宽度
                  （真机实测：按钮 118×446、滚动区被挤到 24px 宽 = "占满整页"）。
               2. **sticky bottom-0**：参与正常流（只占自己那一行高），
                  再把这一行粘在可视区下沿 —— 不遮挡内容，也不用垫占位高度。
               3. **自身尺寸交给内容**：`justify-center` / `md:justify-end` 只决定水平位置，
                  绝不给外层 `w-full` —— 那正是上次把它撑满的写法。
               按用户的定案，它就是一个**小箭头**，不是一条文案按钮：
               文案会横跨内容区、压住正在读的正文。
               ⚠️ 它**刻意留在 ContentColumn 之外**：这枚箭头是"容器级"的浮层，
               贴的是滚动区右沿；若放进正文列，调窄正文后它会跟着缩到正文里，
               离手指更远、也不再贴边。 -->
          <div
            v-if="showJumpLatest"
            class="pointer-events-none sticky bottom-0 z-10 flex justify-center md:justify-end"
          >
            <button
              type="button"
              class="pointer-events-auto mb-1 inline-flex h-8 w-8 items-center justify-center rounded-full border border-border bg-surface text-text-secondary shadow-md transition-colors hover:border-accent hover:text-accent"
              data-testid="jump-latest"
              title="回到最新输出"
              aria-label="回到最新输出"
              @click="jumpToLatest"
            >
              <svg
                class="h-4 w-4"
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
    </div>
  </div>
</template>
