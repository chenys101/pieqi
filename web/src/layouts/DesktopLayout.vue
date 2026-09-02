<script setup lang="ts">
// 桌面布局（方案 §27）：Sidebar + Main。
// Sidebar 结构（按需求）：
//   顶部：仪表盘 / 审批 / 新建任务
//   中部：按项目分组的任务列表（组内按最近活跃倒序，状态小点，可删除）
//   底部：设置 / 连接指示
import { useRoute } from 'vue-router'
import { useWebSocket } from '@/composables/useWebSocket'
import { useSessionStore } from '@/stores/session'
import { useAppStore } from '@/stores/app'
import { useApprovalStore } from '@/stores/approval'
import { useTaskStore } from '@/stores/task'
import { useNotificationStore } from '@/stores/notification'
import type { TaskStatus } from '@/types/task'

const route = useRoute()
const { connection, reconnect } = useWebSocket()
const sessionStore = useSessionStore()
const appStore = useAppStore()
const approvalStore = useApprovalStore()
const taskStore = useTaskStore()
const notify = useNotificationStore()

/** 状态小点：失败=红 / 完成=绿 / 运行=绿(呼吸) / 待决策=黄 / 待运行=蓝 / 已取消=灰 */
const STATUS_DOT: Record<TaskStatus, string> = {
  running: 'bg-success status-breathe',
  completed: 'bg-success',
  failed: 'bg-error',
  waiting_input: 'bg-warning',
  pending: 'bg-info',
  cancelled: 'bg-muted',
}

/** 删除任务（悬停 × 触发，二次确认） */
async function onDelete(id: string) {
  if (!confirm('确定删除该任务？删除后不可恢复。')) return
  try {
    await taskStore.deleteTask(id)
  } catch (err) {
    notify.error(err instanceof Error ? err.message : '删除失败')
  }
}
</script>

<template>
  <div class="flex h-dvh overflow-hidden bg-background text-text">
    <aside class="flex w-64 shrink-0 flex-col border-r border-border bg-surface">
      <!-- 顶部：品牌 + 主导航 + 新建任务 -->
      <div class="shrink-0 px-3 pb-2 pt-4">
        <div class="px-2 pb-3 text-base font-bold tracking-tight">🥧 Pieqi</div>
        <nav class="space-y-0.5">
          <RouterLink
            to="/dashboard"
            class="block rounded-lg px-3 py-2 text-sm transition-colors"
            :class="route.path.startsWith('/dashboard') ? 'bg-elevated font-medium text-text' : 'text-muted hover:bg-elevated/60 hover:text-text'"
          >
            仪表盘
          </RouterLink>
          <RouterLink
            to="/approvals"
            class="flex items-center justify-between rounded-lg px-3 py-2 text-sm transition-colors"
            :class="route.path.startsWith('/approvals') ? 'bg-elevated font-medium text-text' : 'text-muted hover:bg-elevated/60 hover:text-text'"
          >
            审批
            <span
              v-if="approvalStore.pending.length"
              class="rounded-full bg-warning/20 px-1.5 text-xs tabular-nums text-warning"
            >
              {{ approvalStore.pending.length }}
            </span>
          </RouterLink>
        </nav>
        <button
          class="mt-2 w-full rounded-lg bg-accent px-3 py-2 text-sm font-medium text-white transition-opacity hover:opacity-90"
          @click="appStore.newTaskOpen = true"
        >
          ＋ 新建任务
        </button>
      </div>

      <!-- 中部：按项目分组的任务列表（组内最近活跃倒序；悬停显示删除） -->
      <nav class="min-h-0 flex-1 overflow-y-auto px-3 py-2">
        <section v-for="g in taskStore.groupsByProject" :key="g.key" class="mb-3">
          <div class="mb-1 truncate px-2 text-xs font-medium text-muted" :title="g.projectPath">
            {{ g.projectId || g.projectPath }}
          </div>
          <div v-for="t in g.tasks" :key="t.id" class="group/task relative">
            <RouterLink
              :to="`/sessions/${t.id}`"
              class="flex items-center gap-2 rounded-lg py-1.5 pl-2 pr-6 text-sm transition-colors"
              :class="route.path === `/sessions/${t.id}` ? 'bg-elevated font-medium text-text' : 'text-text/80 hover:bg-elevated/60 hover:text-text'"
              :title="t.prompt"
            >
              <span class="h-1.5 w-1.5 shrink-0 rounded-full" :class="STATUS_DOT[t.status]" />
              <span class="truncate">{{ t.title }}</span>
            </RouterLink>
            <!-- 悬停删除按钮 -->
            <button
              class="absolute right-1 top-1/2 hidden -translate-y-1/2 rounded px-1 text-xs leading-none text-muted transition-colors hover:bg-error/15 hover:text-error group-hover/task:block"
              title="删除任务"
              aria-label="删除任务"
              @click="onDelete(t.id)"
            >
              ×
            </button>
          </div>
        </section>
        <div v-if="!taskStore.groupsByProject.length" class="px-2 py-4 text-xs text-muted">
          暂无任务 — 点击「新建任务」开始
        </div>
      </nav>

      <!-- 底部：设置 + 连接指示 -->
      <div class="shrink-0 space-y-2 border-t border-border px-3 py-3">
        <RouterLink
          to="/settings"
          class="block rounded-lg px-3 py-2 text-sm transition-colors"
          :class="route.path.startsWith('/settings') ? 'bg-elevated font-medium text-text' : 'text-muted hover:bg-elevated/60 hover:text-text'"
        >
          ⚙ 设置
        </RouterLink>
        <!-- 连接指示（方案 §37）：状态色 + 文案，点击手动重连 -->
        <button
          class="flex w-full items-center gap-2 px-3 text-xs"
          :class="connection === 'connected' ? 'text-success' : connection === 'initial' ? 'text-muted' : 'text-warning'"
          title="点击重连"
          @click="reconnect()"
        >
          <span class="status-breathe h-1.5 w-1.5 rounded-full bg-current" />
          {{ sessionStore.connectionLabel }}
        </button>
      </div>
    </aside>

    <main class="min-w-0 flex-1 overflow-hidden">
      <slot />
    </main>
  </div>
</template>
