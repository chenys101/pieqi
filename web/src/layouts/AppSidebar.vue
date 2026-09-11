<script setup lang="ts">
// 共享侧边栏：PC 常驻（移动端已被底栏替代，见 MobileLayout）。
// 结构：
//   顶部：品牌 / 仪表盘 / 审批 / 新建任务
//   中部：「任务列表」分组（默认展开）→ 项目（默认收起）→ 任务
//   底部：设置（行尾带连接状态）
//
// 折叠状态**不在这里**，在 stores/taskTree —— 移动端任务浏览器页与侧栏
// 共用同一份，否则用户在两个容器里要各展开一次同样的项目（SPEC §6.1.1）。
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useWebSocket } from '@/composables/useWebSocket'
import { useSessionStore } from '@/stores/session'
import { useTaskStore } from '@/stores/task'
import { useTaskTreeStore } from '@/stores/taskTree'
import { useNotificationStore } from '@/stores/notification'
import type { TaskGroup } from '@/types/task'
import { STATUS_DOT } from '@/utils/format'

const route = useRoute()
const router = useRouter()
const { connection } = useWebSocket()
const sessionStore = useSessionStore()
const taskStore = useTaskStore()
const tree = useTaskTreeStore()
const notify = useNotificationStore()

/** 当前会话 id（/sessions/:id），用于自动展开所在项目 */
const currentSessionId = computed(() => route.path.match(/^\/sessions\/([^/]+)/)?.[1] ?? null)

/** 项目是否展开：手动操作优先；默认收起，仅当前会话所在项目自动展开 */
function isProjectOpen(g: TaskGroup): boolean {
  if (tree.openProjects[g.key] !== undefined) return tree.openProjects[g.key]
  return !!currentSessionId.value && g.tasks.some((t) => t.id === currentSessionId.value)
}

const totalTasks = computed(() => taskStore.groupsByProject.reduce((n, g) => n + g.tasks.length, 0))

// ---------- 删除任务：内联确认（不用原生 confirm()，SPEC 已禁） ----------
// 240px 的侧栏放不下一排按钮，所以做成"点 × 后在行下方长出一条确认条"。
// 原生 confirm 的问题不只是丑：它是**模态**的，会盖住上下文，
// 而这个操作恰恰需要用户看着那一行来确认删的是不是它。
const pendingDelete = ref('')

async function doDelete(id: string) {
  pendingDelete.value = ''
  try {
    await taskStore.deleteTask(id)
  } catch (err) {
    notify.error(err instanceof Error ? err.message : '删除失败')
  }
}
</script>

<template>
  <aside class="flex h-full w-64 shrink-0 flex-col border-r border-border bg-surface">
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
        <!-- 没有「审批」项：SPEC §2.1 的侧栏是 3 项 + 1 分组，且已无独立审批中心。
             审批收敛为「仪表盘待审批组 + Session 内联决策」—— 决策必须发生在它的上下文里
             （批准一条 Bash 命令时要看得见它在改哪个项目的哪个文件），独立页做不到这点。
             待审批的**计数**没丢：它在仪表盘（首页）那一块，而不是挪到导航项上。 -->
      </nav>
      <button
        class="mt-2 w-full rounded-lg bg-accent px-3 py-2 text-sm font-medium text-white transition-opacity hover:opacity-90"
        @click="router.push('/tasks/new')"
      >
        ＋ 新建任务
      </button>
    </div>

    <!-- 中部：任务列表分组（默认展开）→ 项目（默认收起）→ 任务 -->
    <nav class="min-h-0 flex-1 overflow-y-auto px-3 py-2">
      <!-- 分组头：点它整体折叠 / 展开。**不带 data-nav** ——
           它不是导航目标，只是树的开关（否则高亮会落到一个不可达的页面上）。 -->
      <button
        class="flex w-full items-center gap-1.5 rounded-lg px-2 py-1.5 text-left text-xs font-semibold text-text-secondary transition-colors hover:bg-elevated/60 hover:text-text"
        :aria-expanded="tree.groupOpen"
        @click="tree.toggleGroup()"
      >
        <svg
          viewBox="0 0 24 24"
          class="h-3 w-3 shrink-0 transition-transform"
          :class="tree.groupOpen ? 'rotate-90' : ''"
          fill="none"
          stroke="currentColor"
          stroke-width="2.5"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <path d="M9 6l6 6-6 6" />
        </svg>
        任务列表
        <span class="ml-auto shrink-0 tabular-nums font-normal opacity-60">{{ totalTasks }}</span>
      </button>

      <div v-if="tree.groupOpen" class="mt-0.5">
        <section v-for="g in taskStore.groupsByProject" :key="g.key" class="mb-0.5">
          <button
            class="flex w-full items-center gap-1.5 rounded-lg px-2 py-1.5 text-left text-xs font-medium text-muted transition-colors hover:bg-elevated/60 hover:text-text"
            :title="g.projectPath"
            :aria-expanded="isProjectOpen(g)"
            @click="tree.toggleProject(g.key)"
          >
            <svg
              viewBox="0 0 24 24"
              class="h-3 w-3 shrink-0 transition-transform"
              :class="isProjectOpen(g) ? 'rotate-90' : ''"
              fill="none"
              stroke="currentColor"
              stroke-width="2.5"
              stroke-linecap="round"
              stroke-linejoin="round"
              aria-hidden="true"
            >
              <path d="M9 6l6 6-6 6" />
            </svg>
            <span class="truncate">{{ g.projectId || g.projectPath }}</span>
            <span class="ml-auto shrink-0 tabular-nums opacity-70">{{ g.tasks.length }}</span>
          </button>

          <div v-if="isProjectOpen(g)">
            <div v-for="t in g.tasks" :key="t.id" class="group/task relative">
              <RouterLink
                :to="`/sessions/${t.id}`"
                class="flex items-center gap-2 rounded-lg py-1.5 pl-5 pr-6 text-sm transition-colors"
                :class="route.path === `/sessions/${t.id}` ? 'bg-elevated font-medium text-text' : 'text-text/80 hover:bg-elevated/60 hover:text-text'"
                :title="t.prompt"
              >
                <span class="h-1.5 w-1.5 shrink-0 rounded-full" :class="STATUS_DOT[t.status]" />
                <!-- 只放名称，不放时间 / 改动量：240px 侧栏减去缩进只剩约 168px，
                     再插一个时间列会把 80% 的任务名截断（实测见 SPEC §6.1.2）。
                     一般化规则：容器宽度不足时先砍元数据，别砍主标签。 -->
                <span class="truncate">{{ t.title }}</span>
              </RouterLink>
              <button
                v-if="pendingDelete !== t.id"
                class="absolute right-1 top-1/2 hidden -translate-y-1/2 rounded px-1 text-xs leading-none text-muted transition-colors hover:bg-error/15 hover:text-error group-hover/task:block"
                title="删除任务"
                aria-label="删除任务"
                @click="pendingDelete = t.id"
              >
                ×
              </button>
              <!-- 内联确认条：长在这一行下面，用户看着那一行确认删的是不是它 -->
              <div
                v-if="pendingDelete === t.id"
                class="my-0.5 flex items-center gap-1.5 rounded-md border border-error/40 bg-error/5 px-2 py-1"
              >
                <span class="min-w-0 flex-1 truncate text-[11px] text-error">删除「{{ t.title }}」？</span>
                <button
                  class="shrink-0 rounded px-1 text-[11px] font-medium text-error hover:bg-error/15"
                  @click="doDelete(t.id)"
                >
                  删除
                </button>
                <button
                  class="shrink-0 rounded px-1 text-[11px] text-muted hover:bg-elevated"
                  @click="pendingDelete = ''"
                >
                  取消
                </button>
              </div>
            </div>
          </div>
        </section>
        <div v-if="!taskStore.groupsByProject.length" class="px-2 py-4 text-xs text-muted">
          暂无任务 — 点击「新建任务」开始
        </div>
      </div>
    </nav>

    <!-- 底部：设置（状态是这一项的**行尾元数据**）。
         曾经是"设置"与"连接状态"两行，其中连接状态只是条死信息 ——
         合成一项后，它顺着点进去就是连接的详情与重连：一条死信息变成了入口。 -->
    <div class="shrink-0 border-t border-border px-3 py-3">
      <RouterLink
        to="/settings"
        class="flex items-center gap-2 rounded-lg px-3 py-2 text-sm transition-colors"
        :class="route.path.startsWith('/settings') ? 'bg-elevated font-medium text-text' : 'text-muted hover:bg-elevated/60 hover:text-text'"
      >
        ⚙ 设置
        <span
          class="ml-auto flex shrink-0 items-center gap-1.5 text-[11px]"
          :class="connection === 'connected' ? 'text-success' : connection === 'initial' ? 'text-muted' : 'text-warning'"
        >
          <span class="status-breathe h-1.5 w-1.5 rounded-full bg-current" />
          {{ connection === 'connected' ? '连接正常' : sessionStore.connectionLabel }}
        </span>
      </RouterLink>
    </div>
  </aside>
</template>
