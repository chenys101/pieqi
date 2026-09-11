<script setup lang="ts">
// Dashboard（SPEC §6.2）：**只有两个区块 —— 待审批 + 本周概览**。
//
// 这里是全产品唯一一处"行动面"，判据是：**只留需要你做决定的内容**。
// 已从本页移除的三块各有去处，不是删掉：
//   状态统计 chip 行 → 任务浏览在侧栏树，状态由任务行的状态点表达
//   「运行中」        → 状态点承载（这里没有用户此刻要做的事）
//   「需要注意」      → 归入任务树，失败任务在项目里带红色状态点
// "运行中"和"失败任务"是**状态**不是**待办** —— 不需要你在此刻做决定。
import { computed, onMounted } from 'vue'
import { useTaskStore } from '@/stores/task'
import { useApprovalStore } from '@/stores/approval'
import { ApprovalCard } from '@/features/approval'
import { InsightSection } from '@/features/dashboard'
import { computeWeeklyInsight } from '@/features/dashboard/insight'
import { riskOf, riskWeight } from '@/types/approval'
import { timeAgo } from '@/utils/date'

const taskStore = useTaskStore()
const approvalStore = useApprovalStore()

onMounted(() => {
  // 首屏兜底（providers 已拉过，重进页面时刷新）
  if (!taskStore.tasks.length && !taskStore.loading) void taskStore.loadTasks()
})

const insight = computed(() => computeWeeklyInsight(taskStore.tasks))

/**
 * 排序：风险降序（L3 在前），**同风险按等待时间升序**。
 *
 * 时间方向值得说明：等得最久的排最前 —— 它最有可能是被遗忘的那个，
 * 而不是"最新的更紧急"。升序而不是降序，是因为这一区存在的意义就是催办。
 */
const pendingSorted = computed(() =>
  [...approvalStore.pending].sort((a, b) => {
    const byRisk = riskWeight(b.risk) - riskWeight(a.risk)
    if (byRisk !== 0) return byRisk
    return a.createdAt < b.createdAt ? -1 : a.createdAt > b.createdAt ? 1 : 0
  }),
)

/** 是否有破坏性操作在等 —— 决定区块标题的强度（标题跟着最严重的那条走） */
const hasL3 = computed(() => pendingSorted.value.some((a) => riskOf(a.risk) === 'L3'))

/** 等最久的那条已等多久 —— "最早等待 N 分钟"是决定先处理哪张卡的依据 */
const oldestWait = computed(() => {
  const list = pendingSorted.value
  if (!list.length) return ''
  const earliest = list.reduce((min, a) => (a.createdAt < min ? a.createdAt : min), list[0].createdAt)
  return timeAgo(earliest)
})

/** 两块都没有内容时的整页空态（全新安装 / 还没跑过任务） */
const empty = computed(() => !approvalStore.pending.length && !insight.value.hasData)
</script>

<template>
  <div class="h-full overflow-y-auto">
    <div class="mx-auto max-w-4xl space-y-5 px-4 py-5 md:px-6">
      <header>
        <h1 class="text-lg font-semibold text-text">仪表盘</h1>
        <p class="mt-0.5 text-xs text-muted">
          <template v-if="approvalStore.pending.length">
            {{ approvalStore.pending.length }} 项待你决策
          </template>
          <template v-else>没有需要你处理的事项</template>
          <template v-if="insight.completedCount"> · 本周 {{ insight.completedCount }} 个任务已完成</template>
        </p>
      </header>

      <!-- ① 待审批：唯一的行动区块。有审批才出现在 DOM 里 —— 处理完自动收起，
           不留一个写着"0 项"的空盒子（SPEC §6.2：空态占据一行，不留白空缺） -->
      <section v-if="approvalStore.pending.length">
        <div class="mb-2 flex items-baseline gap-2">
          <!-- 标题强度跟着**最严重的那条**走：有 L3 就转红。
               固定用黄色会让"有破坏性操作在等"这个事实被淹没 ——
               区块标题是扫视时的第一落点，它必须反映最坏情况。 -->
          <h2 class="flex items-center gap-1.5 text-sm font-semibold" :class="hasL3 ? 'text-error' : 'text-warning'">
            <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
              <path d="M12 9v4m0 4h.01M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0Z" />
            </svg>
            待审批<template v-if="hasL3"> · 含破坏性操作</template>
          </h2>
          <span v-if="oldestWait" class="text-xs text-muted">
            {{ approvalStore.pending.length }} 项 · 最早等待 {{ oldestWait }}
          </span>
        </div>
        <div class="grid gap-3 md:grid-cols-2">
          <ApprovalCard v-for="a in pendingSorted" :key="a.id" :approval="a" />
        </div>
      </section>

      <div v-else class="rounded-lg border border-border bg-surface px-3.5 py-3 text-sm text-text-secondary">
        ✓ 没有需要你处理的事项
      </div>

      <!-- ② 本周概览 -->
      <InsightSection :insight="insight" />

      <!-- 整页空态：既没待办也没历史。写清楚"下一步做什么"，
           否则新用户看到的是一个没有出口的空白主区 -->
      <div v-if="empty" class="rounded-lg border border-dashed border-border bg-surface-subtle px-4 py-8 text-center">
        <p class="text-sm text-text-secondary">还没有任务</p>
        <p class="mt-1 text-xs text-muted">在左侧「新建任务」开始 —— 完成后这里会显示本周的完成情况</p>
      </div>
    </div>
  </div>
</template>
