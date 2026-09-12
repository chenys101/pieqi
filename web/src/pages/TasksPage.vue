<script setup lang="ts">
// 任务浏览器页（SPEC §6.1.4 / §6.1.5）。
//
// 这一页是**侧栏任务树换了个容器**，不是"任务列表页面" ——
// 桌面端树常驻在侧栏，移动端侧栏被底栏替代（D5），
// 于是把同一棵树渲染成整屏页，由底栏「任务」进入。
// 因此：数据源唯一（`taskStore.groupsByProject`），折叠状态唯一（`stores/taskTree`），
// 两端不维护两份清单（§6.1.1）。
//
// 与旧版 TasksPage（状态 Tab + 卡片网格）的区别不只是排版：
// 旧版把全部任务平铺在主区，任务一多就无限拉长，
// 而"浏览要广度、阅读要深度"，两者抢同一块屏幕必然有一方受损（§6.1）。
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useTaskStore } from '@/stores/task'
import { useTaskTreeStore } from '@/stores/taskTree'
import { TaskBrowserGroup } from '@/features/task'
import type { BrowserGroup } from '@/features/task/types'
import EmptyState from '@/components/ui/EmptyState.vue'
import Spinner from '@/components/ui/Spinner.vue'
import Input from '@/components/ui/Input.vue'
import Select from '@/components/ui/Select.vue'
import type { TaskStatus } from '@/types/task'
import { STATUS_LABELS } from '@/utils/format'

const taskStore = useTaskStore()
const tree = useTaskTreeStore()
const router = useRouter()

const search = ref('')
const status = ref<TaskStatus | ''>('')

onMounted(() => void taskStore.loadTasks())

const filtering = computed(() => !!search.value.trim() || !!status.value)
const totalTasks = computed(() => taskStore.tasks.length)

/** 状态聚合的展示顺序：先说"要你做什么"，再说"跑成什么样" */
const AGG_ORDER: TaskStatus[] = ['waiting_input', 'running', 'failed', 'pending', 'completed']

/**
 * 是否展开。
 *
 * 优先级：**手动操作 > 筛选自动 > 默认收起**。
 *
 * 手动 > 自动 是必要的：原型里 `open = filtering ? list.length > 0 : openProjects[name]`，
 * 结果筛选期间点项目头**什么也不会发生**（写入的 openProjects 被 filtering 分支盖掉），
 * 一个能点的开关点了没反应，比不显示更糟。
 */
function isOpen(key: string, count: number): boolean {
  const manual = tree.openProjects[key]
  if (manual !== undefined) return manual
  return filtering.value && count > 0
}

const groups = computed<BrowserGroup[]>(() => {
  const q = search.value.trim().toLowerCase()
  return taskStore.groupsByProject.map((g) => {
    let list = g.tasks.filter((t) => {
      if (q && !t.title.toLowerCase().includes(q) && !g.projectId.toLowerCase().includes(q)) return false
      if (status.value && t.status !== status.value) return false
      return true
    })
    // 搜索词命中**项目名**时展示该项目全部任务 —— 否则"搜到了这个项目"却给一个空列表
    if (!list.length && q && (g.projectId || '').toLowerCase().includes(q)) list = g.tasks.slice()

    const counts = list.reduce<Partial<Record<TaskStatus, number>>>((m, t) => {
      m[t.status] = (m[t.status] ?? 0) + 1
      return m
    }, {})
    const agg = AGG_ORDER.filter((k) => counts[k]).map((k) => `${counts[k]} ${STATUS_LABELS[k]}`).join(' · ')

    return {
      key: g.key,
      name: g.projectId || g.projectPath,
      path: g.projectPath,
      tasks: list,
      open: isOpen(g.key, list.length),
      dim: filtering.value && !list.length,
      agg,
    }
  })
})

const shownTasks = computed(() => groups.value.reduce((n, g) => n + g.tasks.length, 0))

/** 有任何一个项目处于展开态时按钮变「全部收起」（与原型一致） */
const anyOpen = computed(() => groups.value.some((g) => g.open))

function toggle(key: string) {
  tree.toggleProject(key)
}

function toggleAll() {
  tree.setAllProjects(groups.value.map((g) => g.key), !anyOpen.value)
}

/**
 * 删除任务。
 *
 * 移动端原本完全没有删除路径：侧栏有删除（移动端侧栏不存在），
 * 而这一页按原型没有入口 —— 于是"删掉一个任务"在手机上做不到。
 * 补在这里而不是别处，是因为**人在浏览这一页时才想起要删**：
 * 入口离开了这个意图场景，就跟没有一样。
 */
async function removeTask(id: string) {
  try {
    await taskStore.deleteTask(id)
  } catch (err) {
    // 失败要留在这一页如实告知 —— 静默失败会让用户以为删掉了，下次回来它还在
    taskStore.error = err instanceof Error ? err.message : '删除失败'
  }
}

function clearFilter() {
  search.value = ''
  status.value = ''
}
</script>

<template>
  <div class="h-full overflow-y-auto">
    <div class="mx-auto w-full max-w-3xl px-4 pb-10 pt-4 md:px-6">
      <!-- 头部（§6.1.5）：标题与副文案占一行，操作换到第二行；
           「新建任务」占满整行 —— 它补上了底栏没有的新建入口，也满足拇指点击区。
           flex-wrap 在宽屏自然收敛成一行，所以桌面深链进来也不难看。 -->
      <div class="flex flex-wrap items-center gap-x-3 gap-y-2.5">
        <div class="min-w-0 flex-1">
          <h1 class="text-base font-semibold">任务</h1>
          <p class="mt-0.5 truncate text-xs text-muted">
            {{ groups.length }} 个项目 · {{ totalTasks }} 个任务 · 点项目展开，按最近更新排序
          </p>
        </div>
        <div class="flex w-full items-center gap-2 md:w-auto">
          <button
            class="flex flex-1 items-center justify-center gap-1 rounded-lg bg-accent px-3 py-2 text-xs font-medium text-white transition-opacity hover:opacity-90 md:flex-none"
            @click="router.push('/tasks/new')"
          >
            <svg viewBox="0 0 24 24" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" aria-hidden="true">
              <path d="M12 5v14M5 12h14" />
            </svg>
            新建任务
          </button>
        </div>
      </div>

      <div v-if="taskStore.loading && !taskStore.tasks.length" class="flex justify-center py-12">
        <Spinner class="h-6 w-6 text-muted" />
      </div>

      <div v-else-if="taskStore.error" class="mt-4 rounded-lg border border-error/40 bg-error/10 p-4 text-sm text-error">
        加载失败：{{ taskStore.error }}
      </div>

      <EmptyState
        v-else-if="!taskStore.tasks.length"
        title="还没有任务 — 新建一个开始"
        hint="任务会按所属项目分组，项目收起时只占一行"
      />

      <template v-else>
        <!-- 工具栏：整屏空间充足，所以搜索与状态筛选都保留（侧栏没有，那里放不下） -->
        <div class="mt-4 flex flex-wrap items-center gap-2">
          <label class="relative block min-w-0 flex-1">
            <svg viewBox="0 0 24 24" class="pointer-events-none absolute top-1/2 left-2.5 z-10 h-3.5 w-3.5 -translate-y-1/2 text-muted" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
              <circle cx="11" cy="11" r="8" />
              <path d="M21 21l-4.35-4.35" />
            </svg>
            <Input
              v-model="search"
              type="search"
              size="sm"
              class="pl-8"
              placeholder="搜索项目或任务名…"
              aria-label="搜索项目或任务"
            />
          </label>
          <Select
            size="sm"
            class="w-full sm:w-auto"
            aria-label="按状态筛选"
            :model-value="status"
            @update:model-value="(v) => (status = v as TaskStatus | '')"
          >
            <option value="">全部状态</option>
            <option v-for="(label, k) in STATUS_LABELS" :key="k" :value="k">{{ label }}</option>
          </Select>
        </div>

        <div class="mt-2.5 flex items-center gap-2.5 text-xs text-text-tertiary">
          <span class="min-w-0 flex-1">
            显示 {{ groups.length }} 个项目 · {{ shownTasks }} / {{ totalTasks }} 个任务 · 按最近更新排序
          </span>
          <!-- 筛选时已自动展开，再给「展开全部」会与自动行为语义打架 → 藏起来（§6.1.4） -->
          <button
            v-if="!filtering && groups.length"
            class="shrink-0 font-semibold text-accent hover:underline"
            @click="toggleAll"
          >
            {{ anyOpen ? '全部收起' : '展开全部' }}
          </button>
          <button v-if="filtering" class="shrink-0 font-semibold text-accent hover:underline" @click="clearFilter">
            清除筛选
          </button>
        </div>

        <div v-if="groups.length" class="mt-2.5 space-y-2.5">
          <TaskBrowserGroup v-for="g in groups" :key="g.key" :group="g" @toggle="toggle(g.key)" @remove="removeTask" />
        </div>

        <div v-if="!shownTasks" class="py-11 text-center">
          <div class="text-[13.5px] font-medium">没有匹配的任务</div>
          <div class="mt-1 text-xs text-text-tertiary">换个关键词，或清除筛选条件</div>
        </div>
      </template>
    </div>
  </div>
</template>
