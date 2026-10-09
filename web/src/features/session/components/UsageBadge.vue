<script setup lang="ts">
// UsageBadge：上下文用量角标（agent 上报的 used/size）。
//
// **没数据就整块不渲染**（v-if 在调用方 / 这里早退）：与 diffStat 那些"老任务没有"的
// 字段不同，用量的缺失不是"旧数据"，而是"这个 agent 本来就不报"（claude 桥 / print）。
// 渲染成 0% 会把"未知"显示成"空的"，用户会据此以为上下文还空着。
//
// 为什么不显示百分比文字而只显示 "12.3k / 200k"：token 数是**可操作的**（还剩多少
// 余量直接决定还能不能塞下一轮），百分比只是它的派生。窄屏优先给前者。
import { computed } from 'vue'
import type { TaskUsage } from '@/types/task'
import { usageRatio } from '@/types/task'

const props = withDefaults(
  defineProps<{
    usage: TaskUsage
    /** 极窄：只显示比例条与百分比，不显示 token 数（移动端头部空间有限） */
    dense?: boolean
  }>(),
  { dense: false },
)

/** 已用比例（0~1）；Size 无效时 undefined → 不画条 */
const ratio = computed(() => usageRatio(props.usage))

/**
 * 高水位阈值：≥85% 转警示色。
 *
 * 为什么是 85 而不是 90：真到 90% 时下一轮往往就装不下了，而用户看到警示到实际
 * 触发压缩之间还有一条消息的余量 —— 留这一条余量，警示才来得及被看见。
 */
const nearFull = computed(() => (ratio.value ?? 0) >= 0.85)

/** 百分比文本（0 位小数：进度条已经给了精度，数字再精确就是噪声） */
const percentText = computed(() => (ratio.value === undefined ? '' : `${Math.round(ratio.value * 100)}%`))

/**
 * token 数紧凑写法：999 → "999"，12345 → "12.3k"，200000 → "200k"。
 *
 * 精度分档的判据是**这一位有没有用**：10 万以下保留一位小数（12.3k 与 12k
 * 差 300 token，在"还剩多少余量"的判断上不算噪声）；10 万以上那一位已经淹没在
 * 千位里，给出来只是让数字更长。
 */
function compactTokens(n: number): string {
  if (n < 1000) return String(n)
  if (n < 100000) return `${(n / 1000).toFixed(1)}k`
  return `${Math.round(n / 1000)}k`
}

const usedText = computed(() => compactTokens(props.usage.used))
const sizeText = computed(() => compactTokens(props.usage.size))

/** 成本文本；**只有 hasCost 才给值**（否则"没报成本"会被读成免费） */
const costText = computed(() => {
  if (!props.usage.hasCost || props.usage.costUsd === undefined) return ''
  return `$${props.usage.costUsd.toFixed(2)}`
})

/** 无障碍/悬浮提示：把完整数字与未取整的比例都放进来 */
const title = computed(() => {
  const pct = ratio.value === undefined ? '未知' : `${(ratio.value * 100).toFixed(1)}%`
  const cost = costText.value ? `，成本 ${costText.value}` : ''
  return `上下文用量：${props.usage.used} / ${props.usage.size} tokens（${pct}）${cost}`
})
</script>

<template>
  <span class="inline-flex shrink-0 items-center gap-1.5" :title="title">
    <!-- 进度条：宽 40px（dense 28px）。尺寸写死而不是 flex 拉伸 ——
         它旁边是标题，被拉伸会让进度条长度随标题长短跳变，看着像用量在变。 -->
    <span
      class="inline-block h-1 overflow-hidden rounded-full bg-border align-middle"
      :class="dense ? 'w-7' : 'w-10'"
      role="progressbar"
      :aria-valuenow="usage.used"
      :aria-valuemin="0"
      :aria-valuemax="usage.size"
      :aria-label="title"
    >
      <span
        class="block h-full rounded-full transition-[width] duration-300 ease-out"
        :class="nearFull ? 'bg-warning' : 'bg-accent'"
        :style="{ width: `${(ratio ?? 0) * 100}%` }"
      />
    </span>
    <span class="font-mono tabular-nums" :class="nearFull ? 'text-warning' : ''">
      <template v-if="dense">{{ percentText }}</template>
      <template v-else>{{ usedText }}/{{ sizeText }}</template>
    </span>
    <span v-if="costText && !dense" class="font-mono tabular-nums opacity-70">{{ costText }}</span>
  </span>
</template>
