<script setup lang="ts">
// ArtifactViewer：产物只读 sheet（md 渲染 / code·text 等宽原文 / pdf 内嵌）。
// html 不进这里——调用方应直接 window.open(artifact.open)。
// 头：文件名作大标题，全路径 muted 可折行；无编辑/保存。
import { computed } from 'vue'
import Drawer from '@/components/ui/Drawer.vue'
import FilePreview from './FilePreview.vue'
import { baseNameOf } from '../fileGroups'
import { previewKind } from '../filePreview'

const props = defineProps<{
  open: boolean
  taskId: string
  path: string | null
  kind?: string
}>()

const emit = defineEmits<{ close: [] }>()

const name = computed(() => (props.path ? baseNameOf(props.path) : ''))
const kindLabel = computed(() => {
  const k = props.kind || (props.path ? previewKind(props.path) : null) || ''
  if (k === 'markdown') return 'markdown'
  if (k === 'code') return 'code'
  if (k === 'text') return 'text'
  if (k === 'pdf') return 'pdf'
  return k || '只读'
})
</script>

<template>
  <Drawer :open="open && !!path" flush dismissable @close="emit('close')">
    <div class="flex h-full min-h-0 flex-col" data-testid="artifact-viewer">
      <header class="flex shrink-0 items-start justify-between gap-2 border-b border-border px-4 py-3">
        <div class="min-w-0 flex-1">
          <h2 class="truncate text-sm font-semibold text-text" :title="path ?? undefined">{{ name }}</h2>
          <p class="mt-0.5 break-all text-[11px] leading-snug text-muted" :title="path ?? undefined">
            {{ path }}
          </p>
          <p class="mt-1 text-[11px] text-muted">只读 · {{ kindLabel }}</p>
        </div>
        <button
          type="button"
          class="shrink-0 rounded px-1.5 text-lg leading-none text-muted hover:text-text"
          aria-label="关闭"
          @click="emit('close')"
        >
          ×
        </button>
      </header>
      <div class="min-h-0 flex-1 overflow-y-auto">
        <FilePreview v-if="path" :task-id="taskId" :path="path" />
      </div>
    </div>
  </Drawer>
</template>
