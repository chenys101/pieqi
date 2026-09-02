<script setup lang="ts">
// 工具调用卡片（方案 §19）：🔧 工具名 + 参数（默认折叠）
import { computed, ref } from 'vue'
import { formatToolInput } from '@/utils/format'

const props = defineProps<{ toolName: string; input?: unknown }>()
const collapsed = ref(true)
const rows = computed(() => formatToolInput(props.input))
</script>

<template>
  <div class="event-enter rounded-lg border border-border/60 bg-surface/60">
    <button
      class="flex w-full items-center gap-2 px-3 py-2 text-left text-xs hover:text-text"
      @click="collapsed = !collapsed"
    >
      <span>🔧</span>
      <span class="font-mono font-semibold uppercase">{{ toolName }}</span>
      <!-- 首行参数摘要（折叠时的预览） -->
      <span v-if="collapsed && rows.length" class="min-w-0 flex-1 truncate text-muted">
        {{ rows[0].value }}
      </span>
      <span class="ml-auto shrink-0 text-muted transition-transform" :class="collapsed ? '-rotate-90' : ''">▾</span>
    </button>
    <div v-show="!collapsed" class="border-t border-border/40 px-3 py-2">
      <div v-for="(row, i) in rows" :key="i" class="flex gap-2 py-0.5 text-xs">
        <span class="shrink-0 font-mono text-muted">{{ row.key }}:</span>
        <span class="min-w-0 break-all font-mono">{{ row.value }}</span>
      </div>
      <div v-if="!rows.length" class="text-xs text-muted">（无参数）</div>
    </div>
  </div>
</template>
