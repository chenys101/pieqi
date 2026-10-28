<script setup lang="ts">
// ArtifactsBar：会话顶「全部产物 · N」——默认收起，不占路径宽度；展开后是分组列表。
import { ref } from 'vue'
import type { ArtifactDto } from '@/types/api'
import ArtifactList from './ArtifactList.vue'

defineProps<{
  items: ArtifactDto[]
}>()

const emit = defineEmits<{
  open: [artifact: ArtifactDto]
  view: [artifact: ArtifactDto]
}>()

const expanded = ref(false)
</script>

<template>
  <div v-if="items.length" class="shrink-0 border-b border-border/50 bg-surface-subtle" data-testid="artifacts-bar">
    <button
      type="button"
      class="flex w-full items-center gap-2 px-3 py-2 text-left text-xs text-muted hover:bg-surface-muted md:px-4"
      :aria-expanded="expanded"
      @click="expanded = !expanded"
    >
      <span class="font-medium text-text-secondary">全部产物</span>
      <span class="font-mono text-text-tertiary">· {{ items.length }}</span>
      <span class="ml-auto shrink-0 text-text-tertiary">{{ expanded ? '收起' : '展开' }}</span>
    </button>
    <div v-if="expanded" class="px-3 pb-2 md:px-4">
      <ArtifactList :items="items" @open="emit('open', $event)" @view="emit('view', $event)" />
    </div>
  </div>
</template>
