<script setup lang="ts">
// PlanCard：ACP Agent Plan 清单（EventPlan）。
//
// 协议要求每次更新带完整 entries、客户端整表替换 —— 时间线上每条 plan 事件
// 就是一次快照。展开看全部条目与状态；折叠时只显示「N 项 · M 完成」。
import { computed, ref } from 'vue'
import type { AgentEvent } from '@/types/event'
import PlanList from './PlanList.vue'

const props = defineProps<{ event: AgentEvent }>()
const folded = ref(false)

const entries = computed(() => props.event.payload.plan?.entries ?? [])
const done = computed(() => entries.value.filter((e) => e.status === 'completed').length)
const headline = computed(
  () => props.event.payload.text || (entries.value.length ? `${entries.value.length} 项 · ${done.value} 完成` : '计划'),
)
</script>

<template>
  <div class="event-enter" data-testid="plan-card">
    <PlanList v-model:folded="folded" :entries="entries" :headline="headline" />
  </div>
</template>
