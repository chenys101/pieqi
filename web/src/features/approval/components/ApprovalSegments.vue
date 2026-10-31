<script setup lang="ts">
// 复合 shell 分段风险条：标出「哪一段在拖后腿」。
// 整卡 Risk 仍是最严段；本组件不参与放行，只降低决策成本。
import { computed } from 'vue'
import { RISK_LABELS, riskOf, type ApprovalSegment, type RiskLevel } from '@/types/approval'

const props = defineProps<{ segments?: ApprovalSegment[] }>()

const rows = computed(() => {
  const list = props.segments ?? []
  if (list.length < 2) return []
  return list.map((s) => {
    const risk = riskOf(s.risk)
    return { ...s, risk, attention: risk === 'L2' || risk === 'L3' }
  })
})

const BADGE: Record<RiskLevel, string> = {
  L0: 'bg-elevated text-muted',
  L1: 'bg-info/15 text-info',
  L2: 'bg-warning/15 text-warning',
  L3: 'bg-error/15 text-error',
}
</script>

<template>
  <ul v-if="rows.length" class="mt-2 space-y-1" data-testid="approval-segments">
    <li
      v-for="(row, i) in rows"
      :key="i"
      class="flex items-start gap-2 rounded border px-2 py-1.5 font-mono text-[11px]"
      :class="
        row.attention
          ? 'border-warning/40 bg-warning/5'
          : 'border-border/50 bg-background text-muted'
      "
    >
      <span
        class="mt-0.5 shrink-0 rounded px-1 py-0.5 text-[10px] font-sans font-medium"
        :class="BADGE[row.risk]"
      >
        {{ row.risk }} · {{ RISK_LABELS[row.risk] }}
      </span>
      <span class="min-w-0 flex-1 break-all whitespace-pre-wrap" :class="row.attention ? 'text-text' : ''">
        {{ row.text }}
      </span>
    </li>
  </ul>
</template>
