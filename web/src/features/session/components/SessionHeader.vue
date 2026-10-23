<script setup lang="ts">
// Session Header：返回 / 标题 / 状态 / 元信息 / 操作
//
// **compact（移动端）只留一行**：返回 + 标题 + 状态 + 反馈图标。
// 这一行是滚不走的常驻 chrome，每省一行都是真的还给时间线，所以：
//   - #id / 项目 / 时间不显示（手机上横向空间本来就紧，id 又没有可操作性）；
//   - 删除不放在这里 —— 移动端删任务的入口是任务列表页，详情页不该有一个不可恢复的动作常驻；
//   - 反馈入口从文字按钮换成图标，把宽度让给标题。
// 例外是 error：任务为什么失败在别处看不到，所以只有它值得再多占一行。
//
// **用量角标（UsageBadge）是第二个例外**，但只占行内一小段：它随 agent 的
// usage_update 实时变（每轮多条），放在时间线里会刷屏，放在反馈面板里又看不见。
// 移动端用 dense 形态（只有条 + 百分比）挤进头部那一行；没有上报的 agent 整块不渲染。
//
// **agent 名是第三个例外**（用户 2026-10-23 要求：详情页要能看出这个任务跑在哪个 agent 上）：
// 它是这个会话最重要的上下文之一 —— 同一句话交给 claude 和 qoder，行为与审批路径都不同
// （claude 走 PreToolUse hook、没有"同类免审"）。此前详情页只有新建页能选 agent、
// 进去之后就再也看不到，只能靠"印象"回忆。桌面放在元信息行（与项目名同级），
// 移动端放不进那一行（横向已被标题/状态/用量占满），改由标题下一行承载，
// 且**只在执行运行时出现** —— 它回答的是"现在是谁在干活"。
//
// **「中止」不在这里**（详情页 UI 优化 · 需求 2）：它与输入框内右下角那枚 ■ 是同一个
// 动作（都 emit cancel → taskStore.cancelTask），顶部再挂一个就是同一件事给两个入口，
// 且顶部那个在手机上还紧挨着「返回」，误触成本高。中止的唯一入口 = 输入框内那枚按钮
// （InterveneInput 的 canCancel 双态按钮），它就在拇指区、且与发送同位。
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { useAgentStore } from '@/stores/agent'
import StatusBadge from '@/components/task/StatusBadge.vue'
import Button from '@/components/ui/Button.vue'
import ContentColumn from '@/components/layout/ContentColumn.vue'
import UsageBadge from './UsageBadge.vue'
import type { Task } from '@/types/task'
import { timeAgo } from '@/utils/date'
import { shortId } from '@/utils/format'

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
    /** 移动端一行式头部（见文件头注释）：去元信息行与删除，反馈入口改为最右侧图标 */
    compact?: boolean
    /** 移动端：当前是否正看着变更反馈（图标按下态） */
    feedbackActive?: boolean
  }>(),
  { showFeedback: true, compact: false, feedbackActive: false },
)
/**
 * 唯一真的会被 emit 的是 remove / feedback —— `cancel` 仍声明着（调用方与既有测试
 * 按它接线），但本组件已不再 emit 它（中止入口收归输入框，见文件头注释）。
 */
const emit = defineEmits<{ cancel: []; remove: []; feedback: [] }>()
const router = useRouter()

const agentStore = useAgentStore()

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

/** 正在执行中：此时 agent 决定"此刻是谁在动这个仓库" */
const isActive = computed(() => props.task.status === 'running' || props.task.status === 'pending')

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
  <header class="border-b border-border bg-surface/60 px-3 py-2 md:px-4">
    <!-- 正文列口径由 ContentColumn 统一给（宽屏左对齐 + 可调宽度，窄屏居中 max-w-3xl）。
         头部两行必须与时间线/输入框共用同一条左边界 —— 这正是"错位"的修复点。 -->
    <ContentColumn :resizable="false">
      <div class="flex items-center gap-2">
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

        <StatusBadge :status="task.status" />
        <!-- 上下文用量：**只在该 agent 上报过时才显示**（task.usage 为 undefined 就整块不渲染）。
             移动端走 dense（只有条 + 百分比）—— 头部这一行是常驻 chrome，token 数不值得
             占掉标题的宽度；桌面有整行元信息位，就放在那里给全数字。 -->
        <UsageBadge v-if="task.usage && compact" :usage="task.usage" dense class="text-xs text-muted" />
        <!-- 变更反馈入口：移动端图标（＋上－下 = diff），其余档文字按钮 -->
        <button
          v-if="compact"
          class="shrink-0 rounded-md px-1.5 py-1 text-muted transition-colors hover:bg-elevated hover:text-text aria-pressed:bg-elevated aria-pressed:text-accent"
          :aria-pressed="feedbackActive"
          title="变更反馈"
          aria-label="变更反馈"
          @click="emit('feedback')"
        >
          <svg
            viewBox="0 0 24 24"
            class="h-4 w-4"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            aria-hidden="true"
          >
            <path d="M12 4v7" />
            <path d="M8.5 7.5h7" />
            <path d="M8.5 18h7" />
          </svg>
        </button>
        <Button v-else-if="showFeedback" variant="ghost" size="sm" title="变更反馈" @click="emit('feedback')">反馈</Button>
        <!-- 中止按钮**不在这里**：与输入框内那枚 ■ 重叠（详情页 UI 优化 · 需求 2） -->
        <!-- 删除只在非移动端常驻：不可恢复的动作不该出现在拇指最容易碰到的地方 -->
        <Button v-if="!compact" variant="ghost" size="sm" title="删除任务" @click="emit('remove')">删除</Button>
      </div>

      <!-- 移动端：agent 名单独一行（在标题下方、与标题左对齐）。
           为什么不塞进上面那一行：横向已被标题/状态/用量占满，再挤会截标题；
           而 agent 是"谁在干活"这种一等上下文，值得占一行**短行**。
           只在执行中出现：终态任务的 agent 已在元信息事实里，不必额外占常驻版面。 -->
      <div
        v-if="compact && agentLabel && isActive"
        class="mt-1 flex items-center gap-1.5 pl-8 text-xs text-muted"
        data-testid="session-agent-compact"
      >
        <span class="shrink-0 rounded bg-elevated px-1.5 py-0.5 text-[11px] text-text" :title="agentTitle">
          {{ agentLabel }}
        </span>
        <span class="text-text-tertiary">执行中</span>
      </div>

      <!-- 元信息行：移动端只在有 error 时出现（#id 没有可操作性，项目名与时间在手机上不值得再占一行） -->
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
        <UsageBadge v-if="task.usage && !compact" :usage="task.usage" />
        <span v-if="task.error" class="min-w-0 truncate text-error" :title="task.error">{{ task.error }}</span>
      </div>
    </ContentColumn>
  </header>
</template>
