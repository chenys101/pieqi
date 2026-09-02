<script setup lang="ts">
// 思考块：默认折叠，点击展开（方案 §40，避免大量 thinking 淹没 Timeline）
import { ref } from 'vue'

defineProps<{ text: string }>()
const collapsed = ref(true)
</script>

<template>
  <div class="event-enter rounded-lg border border-border/50 bg-surface/60">
    <button
      class="flex w-full items-center gap-2 px-3 py-2 text-left text-xs text-muted hover:text-text"
      @click="collapsed = !collapsed"
    >
      <span>💭</span>
      <span>思考</span>
      <span class="ml-auto transition-transform" :class="collapsed ? '-rotate-90' : ''">▾</span>
    </button>
    <div
      v-show="!collapsed"
      class="whitespace-pre-wrap break-words border-t border-border/40 px-3 py-2 text-xs leading-relaxed text-muted"
    >
      {{ text }}
    </div>
  </div>
</template>
