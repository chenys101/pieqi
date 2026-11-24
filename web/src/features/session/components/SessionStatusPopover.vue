<script setup lang="ts">
// 移动端「任务状态」详情浮层。
//
// 为什么从 SessionHeader 里拆出来：浮层内容（状态 / 时间 / Agent / 项目）与头部的
// 紧凑图标簇是两件事，混在一个 400 行组件里改一处容易碰另一处。
// 定位由父级（header）决定：它必须挂在 header 上右对齐，见 SessionHeader 的注释。
import { computed } from 'vue'
import type { Task } from '@/types/task'
import { STATUS_DOT, STATUS_LABELS } from '@/utils/format'
import { timeAgo } from '@/utils/date'

const props = defineProps<{
  task: Task
  agentLabel: string
  agentTitle: string
}>()

const statusLabel = computed(() => STATUS_LABELS[props.task.status])
const statusDotClass = computed(() => STATUS_DOT[props.task.status])
const statusDotRing = computed(() => (props.task.status === 'waiting_input' ? 'ring-4 ring-warning/15' : ''))
</script>

<template>
  <div
    class="absolute right-3 top-full z-50 mt-1.5 w-56 max-w-[calc(100vw-24px)] rounded-lg border border-border bg-surface p-3 shadow-md"
    data-testid="session-status-pop"
    @click.stop
  >
    <div class="flex items-center gap-2 text-xs font-semibold">
      <span class="block h-2 w-2 rounded-full" :class="[statusDotClass, statusDotRing]" />
      {{ statusLabel }}
    </div>
    <div class="mt-0.5 pl-4 text-[10px] text-text-tertiary">
      {{ timeAgo(task.updatedAt || task.createdAt) }}前
    </div>
    <div
      v-if="agentLabel"
      class="mt-2 flex items-center justify-between gap-3 border-t border-border-subtle pt-1.5 text-[10px]"
      data-testid="session-status-agent"
    >
      <span class="shrink-0 text-text-tertiary">Agent</span>
      <span class="min-w-0 truncate text-text" :title="agentTitle">{{ agentLabel }}</span>
    </div>
    <!-- 项目名可能是长路径的最后一段：截断时给出完整值（移动端没有 hover，
         但 PWA/桌面仍能看全；截断只是最后手段，不再是"被外侧裁掉"） -->
    <div
      v-if="task.project"
      class="mt-1 flex items-center justify-between gap-3 text-[10px]"
      data-testid="session-status-project"
    >
      <span class="shrink-0 text-text-tertiary">项目</span>
      <span class="min-w-0 truncate text-text" :title="task.projectPath || task.project">{{ task.project }}</span>
    </div>
  </div>
</template>
