<script setup lang="ts">
// 工具结果：默认折叠，失败红色标识（方案 §19）
import { computed, ref } from 'vue'

const props = defineProps<{ toolName: string; result?: string; isError?: boolean }>()
const collapsed = ref(true)
const preview = computed(() => (props.result ?? '').split('\n')[0]?.slice(0, 100))
</script>

<template>
  <div
    class="event-enter rounded-lg border bg-surface/60"
    :class="isError ? 'border-error/40' : 'border-border/50'"
  >
    <button
      class="flex w-full items-center gap-2 px-3 py-2 text-left text-xs hover:text-text"
      :class="isError ? 'text-error' : 'text-muted'"
      @click="collapsed = !collapsed"
    >
      <span>{{ isError ? '✗' : '↳' }}</span>
      <!-- 同 ToolCard：工具名可截断，否则长命令撑破视口（横向滚动条） -->
      <span class="min-w-0 truncate font-mono" :title="toolName">{{ toolName || '结果' }}</span>
      <!-- 「失败」必须 shrink-0：长命令把这一行挤满时，它会竖排成「失/败」两行 -->
      <span v-if="isError" class="shrink-0 whitespace-nowrap font-medium">失败</span>
      <span v-if="collapsed && preview" class="min-w-0 flex-1 truncate opacity-70">{{ preview }}</span>
      <span class="ml-auto shrink-0 opacity-70 transition-transform" :class="collapsed ? '-rotate-90' : ''">▾</span>
    </button>
    <div
      v-show="!collapsed"
      class="whitespace-pre-wrap break-words border-t border-border/40 px-3 py-2 font-mono text-xs leading-relaxed"
      :class="isError ? 'text-error/90' : 'text-muted'"
    >
      {{ result || '（空）' }}
    </div>
  </div>
</template>
