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
// **「中止」不在这里**（详情页 UI 优化 · 需求 2）：它与输入框内右下角那枚 ■ 是同一个
// 动作（都 emit cancel → taskStore.cancelTask），顶部再挂一个就是同一件事给两个入口，
// 且顶部那个在手机上还紧挨着「返回」，误触成本高。中止的唯一入口 = 输入框内那枚按钮
// （InterveneInput 的 canCancel 双态按钮），它就在拇指区、且与发送同位。
import { useRouter } from 'vue-router'
import StatusBadge from '@/components/task/StatusBadge.vue'
import Button from '@/components/ui/Button.vue'
import UsageBadge from './UsageBadge.vue'
import type { Task } from '@/types/task'
import { timeAgo } from '@/utils/date'
import { shortId } from '@/utils/format'

withDefaults(
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
    <div class="mx-auto flex max-w-3xl items-center gap-2">
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

    <!-- 元信息行：移动端只在有 error 时出现（#id 没有可操作性，项目名与时间在手机上不值得再占一行） -->
    <div v-if="!compact || task.error" class="mx-auto mt-1 flex max-w-3xl items-center gap-2 pl-8 text-xs text-muted">
      <template v-if="!compact">
        <span class="font-mono">#{{ shortId(task.id) }}</span>
        <span class="truncate" :title="task.projectPath">{{ task.project }}</span>
        <span>{{ timeAgo(task.updatedAt || task.createdAt) }}前</span>
      </template>
      <!-- 桌面：用量跟在时间后面（同一行，同一层级的信息） -->
      <UsageBadge v-if="task.usage && !compact" :usage="task.usage" />
      <span v-if="task.error" class="min-w-0 truncate text-error" :title="task.error">{{ task.error }}</span>
    </div>
  </header>
</template>
