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
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import type { Ref } from 'vue'
import { useSessionStore } from '@/stores/session'
import { useTaskStore } from '@/stores/task'
import { useFeedbackBundleStore } from '@/stores/feedbackBundle'
import { useFeedbackPanelStore } from '@/stores/feedbackPanel'
import { useTimelineScroll } from '@/composables/useSession'
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
 * jsdom 下 getBoundingClientRect 全为 0 → 基准线命中所有组 → 稳定返回最后一个 Turn，且不抛错。
 */
function trackActive() {
  const root = el.value
  if (!root || railTurns.value.length === 0) {
    activeTurn.value = null
    return
  }
  const baseline = root.getBoundingClientRect().top + 80
  let current = railTurns.value[0].turn
  for (const t of railTurns.value) {
    const node = root.querySelector<HTMLElement>(`[data-testid="turn-group-${t.turn}"]`)
    if (!node) continue
    if (node.getBoundingClientRect().top <= baseline) current = t.turn
  }
  activeTurn.value = current
}

/** 滚动事件：原滚动跟随 + 当前 Turn 跟踪 */
function onScrollAll() {
  onScroll()
  trackActive()
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

// 初载 / Turn 集合变化后重算当前 Turn，保证初始高亮正确
onMounted(() => {
  nextTick(() => trackActive())
})
watch(railTurns, () => {
  nextTick(() => trackActive())
})
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
    <div
      ref="el"
      class="h-full min-w-0 flex-1 overflow-y-auto px-3 py-4 md:px-4"
      data-testid="session-timeline"
      @scroll.passive="onScrollAll"
    >
      <div class="mx-auto flex max-w-3xl flex-col gap-2.5">
        <!-- 分组渲染（有 user_message 事件即分组） -->
        <template v-if="groups.length">
          <section
            v-for="g in groups"
            :key="g.turn"
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
        <!-- 平铺兜底：旧任务（无 user_message 事件）→ 与改动前行为一致（AC-R3-04） -->
        <template v-else>
          <TimelineEventView v-for="event in events" :key="event.id" :event="event" />
        </template>
        <!-- 旧任务兜底：直接展示累积输出 -->
        <TextBubble v-if="outputFallback" :text="task!.output!" />
        <ThinkingBadge v-if="showThinking" />
      </div>
    </div>
  </div>
</template>
