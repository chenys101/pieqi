<script setup lang="ts">
// 任务状态徽章：状态 → 语义色 + 中文文案（方案 §29）
import { computed } from 'vue'
import Badge from '@/components/ui/Badge.vue'
import type { TaskStatus } from '@/types/task'
import { STATUS_LABELS, STATUS_TONES } from '@/utils/format'

const props = defineProps<{ status: TaskStatus }>()
const tone = computed(() => STATUS_TONES[props.status])
</script>

<template>
  <Badge :tone="tone">
    <span
      v-if="status === 'running'"
      class="status-breathe -ml-0.5 h-1.5 w-1.5 rounded-full bg-current"
    />
    {{ STATUS_LABELS[status] }}
  </Badge>
</template>
