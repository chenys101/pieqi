<script setup lang="ts">
// RewindCard：时间线里的 rewind 事件卡片（Timeline 永不删除，回退以事件留痕）。
// 展示「回退到 Turn #N 之前」+ 恢复的文件数；可展开查看恢复路径清单。
import { computed, ref } from 'vue'
import type { AgentEvent } from '@/types/event'

const props = defineProps<{ event: AgentEvent }>()
const expanded = ref(false)

/** 结构化载荷缺失时兜底用文本 */
const rewind = computed(() => props.event.payload.rewind)
const restored = computed(() => rewind.value?.restored ?? [])
</script>

<template>
  <div class="event-enter rounded-lg border border-warning/40 bg-warning/10 px-3 py-2 text-sm">
    <button class="flex w-full items-center gap-2 text-left" @click="expanded = !expanded">
      <span>↩</span>
      <span class="text-warning">
        {{ event.payload.text || `已回退到 Turn #${rewind?.toTurn ?? '?'} 之前` }}
      </span>
      <span v-if="restored.length" class="text-xs text-muted">恢复 {{ restored.length }} 个文件</span>
      <span v-if="restored.length" class="ml-auto shrink-0 text-muted transition-transform" :class="expanded ? '' : '-rotate-90'">▾</span>
    </button>
    <!-- 恢复路径清单（折叠） -->
    <div v-if="expanded && restored.length" class="mt-1.5 border-t border-warning/20 pt-1.5">
      <div v-for="p in restored" :key="p" class="truncate font-mono text-xs text-muted" :title="p">{{ p }}</div>
    </div>
  </div>
</template>
