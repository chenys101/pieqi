<script setup lang="ts">
// ArtifactList：本轮 / 全部产物的文件行（文件名 + 打开/查看；长路径不进主文案）。
import { computed, ref } from 'vue'
import type { ArtifactDto } from '@/types/api'
import { baseNameOf, groupByFolder, tailDirLabel } from '@/utils/fileGroups'
import { artifactOpenLabel, artifactOpensInApp } from '@/utils/filePreview'

const props = defineProps<{
  items: ArtifactDto[]
  /** 无标题时不渲染「本轮产物」类抬头（汇总展开列表用） */
  title?: string
}>()

const emit = defineEmits<{
  open: [artifact: ArtifactDto]
  view: [artifact: ArtifactDto]
}>()

const groups = computed(() => {
  const g = groupByFolder(props.items.map((a) => ({ ...a, path: a.path })))
  return g.map((x) => ({
    ...x,
    label: tailDirLabel(x.dir),
    fullLabel: x.dir || '根目录',
  }))
})

/** 多目录才套分组头；单目录只在列表上放一行 muted 副标题 */
const multiDir = computed(() => groups.value.length > 1)
const singleDirLabel = computed(() => {
  if (multiDir.value || groups.value.length === 0) return ''
  const d = groups.value[0].dir
  return d ? tailDirLabel(d) : ''
})
const singleDirFull = computed(() => groups.value[0]?.fullLabel ?? '')

const openDirs = ref<Record<string, boolean>>({})
function isOpen(dir: string) {
  // 多目录默认收起；点开才看文件 —— 收起态自带「值不值得展开」
  return openDirs.value[dir] === true
}
function toggle(dir: string) {
  openDirs.value = { ...openDirs.value, [dir]: !isOpen(dir) }
}

function onAction(a: ArtifactDto) {
  if (a.kind === 'html' || a.kind === 'image') {
    emit('open', a)
    return
  }
  if (artifactOpensInApp(a.kind)) {
    emit('view', a)
    return
  }
  emit('open', a)
}

function labelOf(a: ArtifactDto) {
  return artifactOpenLabel(a.kind) ?? '查看'
}
</script>

<template>
  <div v-if="items.length" class="rounded-lg border border-border/60 bg-surface" data-testid="artifact-list">
    <div v-if="title" class="border-b border-border/40 px-2.5 py-1.5 text-[11px] font-medium text-muted">
      {{ title }}
      <span class="ml-1 font-mono text-text-tertiary">· {{ items.length }}</span>
    </div>
    <p
      v-if="!multiDir && singleDirLabel"
      class="truncate px-2.5 pt-1.5 text-[10px] text-muted"
      :title="singleDirFull"
    >
      {{ singleDirLabel }}
    </p>
    <template v-if="multiDir">
      <div v-for="g in groups" :key="g.dir || '.'" class="border-t border-border/30 first:border-t-0">
        <button
          type="button"
          class="flex w-full items-center gap-2 px-2.5 py-1.5 text-left text-[11px] text-muted hover:bg-surface-muted"
          :aria-expanded="isOpen(g.dir)"
          :title="g.fullLabel"
          @click="toggle(g.dir)"
        >
          <span class="min-w-0 flex-1 truncate font-mono">{{ g.label }}</span>
          <span class="shrink-0 font-mono text-text-tertiary">{{ g.files.length }}</span>
          <span class="shrink-0 text-text-tertiary">{{ isOpen(g.dir) ? '▾' : '▸' }}</span>
        </button>
        <ul v-if="isOpen(g.dir)" class="pb-1">
          <li
            v-for="a in g.files"
            :key="a.path"
            class="flex items-center gap-2 px-2.5 py-1"
            :title="a.path"
          >
            <span class="min-w-0 flex-1 truncate text-xs text-text">{{ baseNameOf(a.path) }}</span>
            <button
              type="button"
              class="shrink-0 text-xs text-accent hover:underline"
              @click="onAction(a)"
            >
              {{ labelOf(a) }}
            </button>
          </li>
        </ul>
      </div>
    </template>
    <ul v-else class="py-1">
      <li
        v-for="a in items"
        :key="a.path"
        class="flex items-center gap-2 px-2.5 py-1"
        :title="a.path"
      >
        <span class="min-w-0 flex-1 truncate text-xs text-text">{{ baseNameOf(a.path) }}</span>
        <button
          type="button"
          class="shrink-0 text-xs text-accent hover:underline"
          @click="onAction(a)"
        >
          {{ labelOf(a) }}
        </button>
      </li>
    </ul>
  </div>
</template>
