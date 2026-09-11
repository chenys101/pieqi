<script setup lang="ts">
// 本周概览（SPEC §6.2）：回顾性指标，不随历史增长。
//
// 与"状态统计"的区别值得写在这里因为它决定了取舍：概览回答**这周干得怎么样**，
// 不回答**现在各状态有几个**。后者由侧栏任务树的项目计数与状态点回答 ——
// 仪表盘之所以能删掉那三块，正是因为它们是后者。
//
// 四个指标只做三个：**「一次通过率」明确不做**。
// "是否一次通过"在产品语义上没定性（从未等过人？没有追加干预？只有一个 Turn？
// 三种定义各说各话），硬挑一个会得到「看着有数、无人知道它在说什么」的指标。
// 宁缺不滥 —— 比一个假指标更有用的是承认这块还没定义好。
import { computed } from 'vue'
import type { WeeklyInsight } from '../insight'
import { sparkHeight } from '../insight'

const props = defineProps<{ insight: WeeklyInsight }>()

/** 柱状图最高值（至少 1，避免全 0 时的 0/0） */
const maxDay = computed(() => Math.max(1, ...props.insight.days.map((d) => d.count)))

/** 千分位；原始摘要 TO 概览只关心量级，四位数以上用 k 缩写保持一行不下划线 */
function fmt(n: number): string {
  if (n >= 10_000) return (n / 1000).toFixed(0) + 'k'
  return n.toLocaleString()
}

const deltaText = computed(() => {
  const d = props.insight.deltaVsPrev
  if (d === null) return null
  if (d === 0) return '与上周持平'
  return d > 0 ? `+${d} 较上周` : `${d} 较上周`
})
</script>

<template>
  <section>
    <div class="mb-2 flex items-baseline gap-2">
      <h2 class="text-sm font-semibold text-text">本周概览</h2>
      <!-- 口径写在标题行而不是图里，"过去 7 天"是解读这三个数字的唯一依据 -->
      <span class="text-xs text-muted">过去 7 天 · 全部项目</span>
    </div>

    <div
      class="grid grid-cols-2 gap-px overflow-hidden rounded-lg border border-border bg-border md:grid-cols-[1.7fr_1fr_1.2fr]"
    >
      <!-- ① 完成任务：唯一带趋势的指标，占比也最大 -->
      <div class="col-span-2 bg-surface px-3.5 py-3 md:col-span-1">
        <div class="flex items-start justify-between gap-2">
          <div>
            <div class="text-2xl font-semibold tabular-nums text-text">{{ insight.completedCount }}</div>
            <div class="mt-0.5 text-xs text-muted">完成任务</div>
          </div>
          <span v-if="deltaText" class="shrink-0 text-xs tabular-nums text-text-secondary">{{ deltaText }}</span>
        </div>
        <!-- 7 天迷你柱状图：今天那根实色，其余淡色（一眼看出"今天到哪了"） -->
        <div class="mt-2 flex h-8 items-end gap-1" aria-hidden="true">
          <span
            v-for="d in insight.days"
            :key="d.at"
            class="min-h-[3px] flex-1 rounded-t-sm"
            :class="d.isToday ? 'bg-accent' : 'bg-accent/20'"
            :style="{ height: sparkHeight(d.count, maxDay) + '%' }"
          />
        </div>
      </div>

      <!-- ② 平均耗时 -->
      <div class="bg-surface px-3.5 py-3">
        <div class="text-2xl font-semibold tabular-nums text-text">
          <template v-if="insight.avgMinutes !== null">{{ insight.avgMinutes }}</template>
          <template v-else>—</template>
          <span v-if="insight.avgMinutes !== null" class="ml-0.5 text-sm font-normal text-muted">分钟</span>
        </div>
        <div class="mt-0.5 text-xs text-muted">平均耗时</div>
      </div>

      <!-- ③ 代码改动 -->
      <div class="bg-surface px-3.5 py-3">
        <div class="flex flex-wrap items-baseline gap-1.5">
          <span class="text-2xl font-semibold tabular-nums text-success">+{{ fmt(insight.diff.additions) }}</span>
          <span class="text-2xl font-semibold tabular-nums text-error">−{{ fmt(insight.diff.deletions) }}</span>
        </div>
        <div class="mt-0.5 text-xs text-muted">代码改动 · {{ insight.diff.files }} 个文件</div>
      </div>
    </div>
  </section>
</template>
