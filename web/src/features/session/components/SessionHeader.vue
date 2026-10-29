<script setup lang="ts">
// Session Header：返回 / 标题 / 状态 / 元信息 / 操作
//
// **compact（移动端）是四个独立入口**（用户 2026-10-29 定案，方案 C）：
//   · 状态：裸圆点按钮，点开是状态详情浮层。**不再**和"其他任务数"挤在同一个按钮里 ——
//     一个入口同时代表两件事，用户既分不清哪个是状态、也点不开想要的那个。
//   · 其他任务：描边圆 + 堆叠图标 + 数量，点开**顶部下拉**（待决策优先，最多 3 个，
//     其余走「查看全部」）。它和状态点靠形状/填充/图标/数字区分。
//   · 新建任务：accent 实心圆 + 加号，从底栏上移到顶栏（方案 C 的需求 4）。
//   · 反馈：方形面板图标（无文字、无 +/-），与新建的实心圆在形状/填充/图标上区分。
//
// **Agent 与上下文用量都已下移到输入条**（见 InterveneInput 的 #lead）：
//   头部只负责"当前是谁、在什么状态、还能去哪"，执行上下文（agent / model / usage）
//   跟着输入条走，运行中禁用输入时也始终可见。移动端不再有"执行中"那一行。
//
// 桌面（非 compact）保持原样：状态徽章 + 元信息行（#id / agent / 项目 / 时间 / 用量）
// + 反馈/删除按钮。移动端不常驻删除（不可恢复的动作不该在拇指区）。
//
// **「中止」不在这里**（详情页 UI 优化 · 需求 2）：它与输入框内右下角那枚 ■ 是同一个
// 动作（都 emit cancel → taskStore.cancelTask），顶部再挂一个就是同一件事给两个入口，
// 且顶部那个在手机上还紧挨着「返回」，误触成本高。中止的唯一入口 = 输入框内那枚按钮
// （InterveneInput 的 canCancel 双态按钮），它就在拇指区、且与发送同位。
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useAgentStore } from '@/stores/agent'
import { useTaskStore } from '@/stores/task'
import StatusBadge from '@/components/task/StatusBadge.vue'
import Button from '@/components/ui/Button.vue'
import ContentColumn from '@/components/layout/ContentColumn.vue'
import UsageBadge from './UsageBadge.vue'
import { otherRunningTasks } from '../otherTasks'
import type { Task } from '@/types/task'
import { STATUS_DOT, STATUS_LABELS, shortId } from '@/utils/format'
import { timeAgo } from '@/utils/date'

const props = withDefaults(
  defineProps<{
    task: Task
    /**
     * 任务是否运行中（可中止）。**本组件不再消费它** —— 中止入口已收归输入框
     * （见文件头注释）；保留 props 是为了不动调用方的接线与既有测试契约，
     * 也让「这个任务此刻可不可中止」这件事在头部仍是可读的。
     */
    canCancel: boolean
    /**
     * 是否显示「反馈」按钮。**宽屏不需要** —— 它有常驻贴边条，
     * 再挂一个按钮就是同一件事给两个入口，而且位置还不是它真正生效的地方。
     */
    showFeedback?: boolean
    /** 移动端四个独立入口（见文件头注释）；桌面仍走原头部 */
    compact?: boolean
    /** 移动端：当前是否正看着变更反馈（图标按下态） */
    feedbackActive?: boolean
  }>(),
  { showFeedback: true, compact: false, feedbackActive: false },
)
const emit = defineEmits<{ cancel: []; remove: []; feedback: [] }>()
const router = useRouter()
const agentStore = useAgentStore()
const taskStore = useTaskStore()

/**
 * 本任务的 agent：**展示名**（Claude Code / Qoder CLI）而非业务名（claude / qoder）。
 *
 * 目录未加载 / 该 agent 已被关掉时（旧任务、或用户收窄了 agents.enabled），
 * byId 返回 undefined —— 此时原样显示业务名，"不知道它是什么"也好过显示空白。
 * 旧任务没有 agent 字段，适配层已兜底成默认 agent（见 api/client.ts 的 adaptTask）。
 */
const agentLabel = computed(() => {
  const id = props.task.agent
  if (!id) return ''
  return agentStore.byId(id)?.name || id
})
/** 悬停说明：业务名 + 传输层，用来区分"显示名相同但通道不同"的情形 */
const agentTitle = computed(() => {
  const id = props.task.agent
  if (!id) return ''
  const info = agentStore.byId(id)
  return info ? `${info.name}（${id} · ${info.transport}）` : id
})

/** 当前状态：移动端只显示圆点，文字在这里，点开浮层才出现 */
const statusLabel = computed(() => STATUS_LABELS[props.task.status])
const statusDotClass = computed(() => STATUS_DOT[props.task.status])
/** 需决策：在圆点外补一圈光晕（9px 的点只靠颜色不够，且不能破坏"只有一个点"） */
const statusDotRing = computed(() => (props.task.status === 'waiting_input' ? 'ring-4 ring-warning/15' : ''))

/**
 * 其他运行中任务：规则在 otherTasks.ts（待决策 → 运行中 → 待运行，同档 updatedAt 倒序）。
 * 取前 3 个常显，其余走「查看全部」；一个都没有时连按钮都不渲染。
 */
const otherTasks = computed(() => otherRunningTasks(taskStore.tasks, props.task.id))
const visibleOtherTasks = computed(() => otherTasks.value.slice(0, 3))
const otherCount = computed(() => otherTasks.value.length)
/** 按钮里放两位数没问题；100 个以上才收成 99+（不要一过 9 就把 18 伪装成 9+） */
const otherCountLabel = computed(() => (otherCount.value > 99 ? '99+' : String(otherCount.value)))

/** 新建任务：带着当前任务的项目进新建页（原底栏入口上移，口径不变） */
const newTaskHref = computed(() => {
  const p = (props.task.projectPath || '').trim()
  return { path: '/tasks/new', query: p ? { project: p } : {} }
})
const newTaskTitle = computed(() => {
  const name = props.task.project || props.task.projectPath
  return name ? `在「${name}」新建任务` : '新建任务'
})

/** 同一时刻只允许一个浮层：打开任务下拉会顺手收起状态详情（反之亦然）。 */
const openPopover = ref<'status' | 'tasks' | null>(null)
function togglePopover(which: 'status' | 'tasks') {
  openPopover.value = openPopover.value === which ? null : which
}
function closePopovers() {
  openPopover.value = null
}

/** 点空白 / Esc 收起。触发器与浮层各自 @click.stop，因此这里收到的只可能是"外面"。 */
function onDocClick() {
  closePopovers()
}
function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') closePopovers()
}
onMounted(() => {
  document.addEventListener('click', onDocClick)
  document.addEventListener('keydown', onKeydown)
})
onBeforeUnmount(() => {
  document.removeEventListener('click', onDocClick)
  document.removeEventListener('keydown', onKeydown)
})

function goNewTask() {
  closePopovers()
  void router.push(newTaskHref.value)
}
function goTask(id: string) {
  closePopovers()
  void router.push(`/sessions/${id}`)
}
function goAllTasks() {
  closePopovers()
  void router.push('/tasks')
}

/**
 * ← 是"回上一页"，不是"回首页"：手机上多数是从 /tasks 列表或仪表盘待审批组点进来的，
 * 一律 push('/dashboard') 会跳到另一个页面，还丢掉列表的滚动位置（只有 popstate 会还原
 * Vue Router 的 savedPosition）。
 *
 * 兜底给 dashboard：飞书深链/冷启动直达时站内没有上一页，router.back() 会没反应。
 * 读 history.state.back（vue-router 自己写的上一页 full path），比 history.length 可靠 ——
 * 后者在 PWA 里几乎总是 >1，判不出"上一页是不是站内页"。
 */
function goBack() {
  if (window.history.state?.back) router.back()
  else router.push('/dashboard')
}
</script>

<template>
  <header class="relative z-30 border-b border-border bg-surface/60 px-3 py-2 md:px-4">
    <!-- 正文列口径由 ContentColumn 统一给（宽屏左对齐 + 可调宽度，窄屏居中 max-w-3xl）。
         头部两行必须与时间线/输入框共用同一条左边界 —— 这正是"错位"的修复点。 -->
    <ContentColumn :resizable="false">
      <div class="flex items-center" :class="compact ? 'gap-1' : 'gap-2'">
        <button
          class="shrink-0 rounded-md px-1.5 py-1 text-muted transition-colors hover:bg-elevated hover:text-text"
          title="返回"
          aria-label="返回"
          @click="goBack"
        >
          <svg viewBox="0 0 24 24" class="h-4 w-4" aria-hidden="true">
            <path d="M15 18l-6-6 6-6" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round" />
          </svg>
        </button>

        <h1 class="min-w-0 flex-1 truncate text-sm font-semibold" :title="task.prompt">
          {{ task.title }}
        </h1>

        <!-- ── 移动端：状态 / 任务列表 / 新建 / 反馈，四个独立入口 ─────────── -->
        <template v-if="compact">
          <!-- 当前状态：裸圆点；点开是这个任务的状态详情（浮层本体挂在 header 上，见后） -->
          <div class="shrink-0">
            <button
              class="grid h-7 w-7 place-items-center rounded-md text-muted transition-colors hover:bg-elevated hover:text-text"
              :class="task.status === 'waiting_input' ? 'bg-warning/5' : ''"
              :aria-label="`任务状态：${statusLabel}`"
              :aria-expanded="openPopover === 'status'"
              aria-haspopup="dialog"
              data-testid="session-status"
              title="任务状态"
              @click.stop="togglePopover('status')"
            >
              <span class="block h-2 w-2 rounded-full" :class="[statusDotClass, statusDotRing]" />
            </button>
          </div>

          <!-- 其他任务：描边圆 + 堆叠图标 + 数量；下拉只列任务，不混状态详情 -->
          <button
            v-if="otherCount"
            class="flex h-7 w-7 shrink-0 items-center justify-center gap-0.5 rounded-full border border-border bg-surface-subtle text-muted transition-colors hover:border-border-strong hover:bg-elevated hover:text-text"
            :aria-label="`其他运行中任务（${otherCount} 个）`"
            :aria-expanded="openPopover === 'tasks'"
            aria-haspopup="dialog"
            data-testid="session-tasks"
            title="其他运行中任务"
            @click.stop="togglePopover('tasks')"
          >
            <svg viewBox="0 0 24 24" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <rect x="4" y="5" width="16" height="4" rx="1.5" />
              <rect x="6" y="11" width="12" height="4" rx="1.5" />
              <rect x="8" y="17" width="8" height="4" rx="1.5" />
            </svg>
            <span class="text-[8px] font-bold tabular-nums">{{ otherCountLabel }}</span>
          </button>

          <!-- 新建任务：accent 实心圆 + 加号（从底栏上移） -->
          <button
            type="button"
            class="grid h-7 w-7 shrink-0 place-items-center rounded-full bg-accent text-white shadow-sm transition-opacity hover:opacity-90 active:scale-95"
            :title="newTaskTitle"
            :aria-label="newTaskTitle"
            data-testid="session-new-task"
            @click="goNewTask"
          >
            <svg viewBox="0 0 24 24" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" aria-hidden="true">
              <path d="M12 5v14M5 12h14" />
            </svg>
          </button>

          <!-- 变更反馈：方形面板图标（无文字、无 +/-），与实心圆新建区分 -->
          <button
            type="button"
            class="grid h-7 w-7 shrink-0 place-items-center rounded-md border transition-colors"
            :class="
              feedbackActive
                ? 'border-accent/40 bg-accent/10 text-accent'
                : 'border-border bg-surface-subtle text-muted hover:border-border-strong hover:bg-elevated hover:text-text'
            "
            :aria-pressed="feedbackActive"
            aria-label="变更反馈"
            title="变更反馈"
            data-testid="session-feedback"
            @click="emit('feedback')"
          >
            <svg viewBox="0 0 24 24" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <rect x="3.5" y="4.5" width="17" height="15" rx="2.5" />
              <path d="M14.5 4.5v15" />
              <path d="M16.2 8.5h2.4M16.2 12h2.4" />
            </svg>
          </button>
        </template>

        <!-- ── 桌面 / 平板：原头部（状态徽章 + 反馈/删除） ─────────────────── -->
        <template v-else>
          <StatusBadge :status="task.status" />
          <Button v-if="showFeedback" variant="ghost" size="sm" title="变更反馈" @click="emit('feedback')">反馈</Button>
          <Button variant="ghost" size="sm" title="删除任务" @click="emit('remove')">删除</Button>
        </template>
      </div>

      <!-- 移动端不再有 agent 行：agent 已下移到输入条（见 InterveneInput #lead） -->

      <!-- 元信息行：移动端只在有 error 时出现；桌面展示 #id / agent / 项目 / 时间 / 用量 -->
      <div v-if="!compact || task.error" class="mt-1 flex items-center gap-2 pl-8 text-xs text-muted">
        <template v-if="!compact">
          <span class="font-mono">#{{ shortId(task.id) }}</span>
          <!-- agent：本任务跑在哪个 agent 上（与项目名同级、同一行）。
               放在项目名**之前**：换了 agent 往往比换了项目更能解释"行为为什么不一样"。 -->
          <span
            v-if="agentLabel"
            class="shrink-0 rounded bg-elevated px-1.5 py-0.5 text-[11px] font-medium text-text"
            :title="agentTitle"
            data-testid="session-agent"
          >
            {{ agentLabel }}
          </span>
          <span class="truncate" :title="task.projectPath">{{ task.project }}</span>
          <span>{{ timeAgo(task.updatedAt || task.createdAt) }}前</span>
        </template>
        <!-- 桌面：用量跟在时间后面（同一行，同一层级的信息） -->
        <UsageBadge v-if="!compact && task.usage" :usage="task.usage" />
        <span v-if="task.error" class="min-w-0 truncate text-error" :title="task.error">{{ task.error }}</span>
      </div>
    </ContentColumn>

    <!-- 状态详情：同样**挂在 header 上、右对齐**（与下面的「其他任务」下拉同一条判据）。
         ⚠️ 不要改回挂在 28px 触发按钮上做 `left-1/2 -translate-x-1/2` 居中：
         「其他任务」按钮在 otherCount === 0 时**不渲染**（见上面那个 v-if），
         状态点于是成了右簇最左的那个，面板中心 = 按钮中心 ⇒ 面板右缘越出视口。
         375 宽实测：面板 [173, 397] vs 视口 375 —— 右缘 22px 被外层 overflow-hidden 裁掉，
         右对齐的「Agent / 项目」值只剩开头几个字（用户反馈的「点状态展示不全」就是它，
         截图里 dsh→d、pieqi→pie）。
         现在面板右缘固定对齐 header 的 12px 内边距，任何宽度都不会越界；
         max-w 兜住极窄屏（< 248px），宽度不足时先缩面板而不是让它被裁。 -->
    <div
      v-if="compact && openPopover === 'status'"
      class="absolute right-3 top-full z-50 mt-1.5 w-56 max-w-[calc(100vw-24px)] rounded-lg border border-border bg-surface p-3 shadow-md"
      data-testid="session-status-pop"
      @click.stop
    >
      <div class="flex items-center gap-2 text-xs font-semibold">
        <span class="block h-2 w-2 rounded-full" :class="[statusDotClass, statusDotRing]" />
        {{ statusLabel }}
      </div>
      <div class="mt-0.5 pl-4 text-[10px] text-text-tertiary">
        {{ timeAgo(task.updatedAt || task.createdAt) }}前
      </div>
      <div
        v-if="agentLabel"
        class="mt-2 flex items-center justify-between gap-3 border-t border-border-subtle pt-1.5 text-[10px]"
        data-testid="session-status-agent"
      >
        <span class="shrink-0 text-text-tertiary">Agent</span>
        <span class="min-w-0 truncate text-text" :title="agentTitle">{{ agentLabel }}</span>
      </div>
      <!-- 项目名可能是长路径的最后一段：截断时给出完整值（移动端没有 hover，
           但 PWA/桌面仍能看全；截断只是最后手段，不再是"被外侧裁掉"） -->
      <div
        v-if="task.project"
        class="mt-1 flex items-center justify-between gap-3 text-[10px]"
        data-testid="session-status-project"
      >
        <span class="shrink-0 text-text-tertiary">项目</span>
        <span class="min-w-0 truncate text-text" :title="task.projectPath || task.project">{{ task.project }}</span>
      </div>
    </div>

    <!-- 其他任务下拉：挂在 header 上、右对齐（不是挂在 28px 的按钮上，
         否则 320 宽时 300px 的面板会从左边溢出）。只有 compact 才有。 -->
    <div
      v-if="compact && openPopover === 'tasks'"
      class="absolute right-3 top-full z-50 mt-1.5 w-[300px] max-w-[calc(100vw-24px)] overflow-hidden rounded-xl border border-border bg-surface shadow-md"
      data-testid="session-tasks-pop"
      @click.stop
    >
      <div class="flex items-center gap-2 px-3 pb-1.5 pt-2.5">
        <span class="flex-1 text-xs font-semibold">其他运行中任务</span>
        <span class="grid h-4 min-w-4 place-items-center rounded-full bg-elevated px-1 text-[9px] font-bold text-muted">
          {{ otherCount }}
        </span>
        <button
          type="button"
          class="grid h-6 w-6 place-items-center rounded-md text-muted transition-colors hover:bg-elevated hover:text-text"
          aria-label="关闭"
          @click="closePopovers"
        >
          ×
        </button>
      </div>
      <div class="px-3 pb-1.5 text-[9px] text-text-tertiary">待决策优先 · 最多显示 3 个</div>
      <div class="max-h-[60vh] overflow-y-auto px-2 pb-2">
        <button
          v-for="t in visibleOtherTasks"
          :key="t.id"
          type="button"
          class="flex w-full items-center gap-2 rounded-md px-2 py-2 text-left text-xs transition-colors hover:bg-elevated"
          :class="t.status === 'waiting_input' ? 'border border-warning/25 bg-warning/5' : 'border border-transparent'"
          :data-testid="`session-other-task-${t.id}`"
          @click="goTask(t.id)"
        >
          <span class="block h-1.5 w-1.5 shrink-0 rounded-full" :class="STATUS_DOT[t.status]" />
          <span class="min-w-0 flex-1">
            <span class="block truncate">{{ t.title }}</span>
            <span class="block truncate text-[9px] text-text-tertiary">
              {{ t.project }} · {{ timeAgo(t.updatedAt) }}前
            </span>
          </span>
          <span class="shrink-0 text-[10px] text-text-tertiary">{{ STATUS_LABELS[t.status] }}</span>
          <span class="shrink-0 text-text-tertiary" aria-hidden="true">›</span>
        </button>

        <button
          v-if="otherCount > 3"
          type="button"
          class="mt-1 flex h-9 w-full items-center justify-center rounded-md border border-dashed border-border-strong text-[11px] text-accent transition-colors hover:bg-accent/5"
          data-testid="session-all-tasks"
          @click="goAllTasks"
        >
          查看全部 {{ otherCount }} 个运行中任务 →
        </button>
      </div>
    </div>
  </header>
</template>
