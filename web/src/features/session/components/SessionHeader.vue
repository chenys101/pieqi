<script setup lang="ts">
// Session Header：返回 / 标题 / 状态 / 元信息 / 操作
//
// **compact（移动端）只留一行**：返回 + 标题 + 状态 + 中止 + 反馈图标。
// 这一行是滚不走的常驻 chrome，每省一行都是真的还给时间线，所以：
//   - #id / 项目 / 时间不显示（手机上横向空间本来就紧，id 又没有可操作性）；
//   - 删除不放在这里 —— 移动端删任务的入口是任务列表页，详情页不该有一个不可恢复的动作常驻；
//   - 反馈入口从文字按钮换成图标，把宽度让给标题。
// 例外是 error：任务为什么失败在别处看不到，所以只有它值得再多占一行。
import { useRouter } from 'vue-router'
import StatusBadge from '@/components/task/StatusBadge.vue'
import Button from '@/components/ui/Button.vue'
import type { Task } from '@/types/task'
import { timeAgo } from '@/utils/date'
import { shortId } from '@/utils/format'

withDefaults(
  defineProps<{
    task: Task
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
      <Button v-if="canCancel" variant="ghost" size="sm" @click="emit('cancel')">中止</Button>
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
      <span v-if="task.error" class="min-w-0 truncate text-error" :title="task.error">{{ task.error }}</span>
    </div>
  </header>
</template>
