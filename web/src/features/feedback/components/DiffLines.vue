<script setup lang="ts">
// DiffLines：unified diff 文本的纯展示组件（回顾性 / 前瞻性 Diff 共用）。
// 按行前缀着色：+ 增 / - 删 / @@ hunk / 头部元信息弱化。
import { computed } from 'vue'

const props = defineProps<{
  diff: string
  truncated?: boolean
  binary?: boolean
}>()

interface DiffLine {
  kind: 'add' | 'del' | 'hunk' | 'meta' | 'ctx'
  text: string
}

/** diff 文本 → 渲染行（按 unified diff 语义着色） */
const lines = computed<DiffLine[]>(() =>
  (props.diff ?? '')
    .split('\n')
    .map((text) => {
      if (text.startsWith('@@')) return { kind: 'hunk', text }
      // +++/--- 与 diff --git/index 头部按元信息弱化
      if (text.startsWith('+++') || text.startsWith('---') || text.startsWith('diff ') || text.startsWith('index ')) {
        return { kind: 'meta', text }
      }
      if (text.startsWith('+')) return { kind: 'add', text }
      if (text.startsWith('-')) return { kind: 'del', text }
      return { kind: 'ctx', text }
    }),
)

const lineClass: Record<DiffLine['kind'], string> = {
  add: 'bg-success/10 text-success',
  del: 'bg-error/10 text-error',
  hunk: 'text-accent',
  meta: 'text-muted/70',
  ctx: 'text-muted',
}
</script>

<template>
  <div class="text-xs">
    <div v-if="binary" class="px-3 py-2 text-muted">二进制文件（不支持文本 diff）</div>
    <div v-else-if="!diff.trim()" class="px-3 py-2 text-muted">无差异</div>
    <div v-else class="overflow-x-auto">
      <div
        v-for="(line, i) in lines"
        :key="i"
        class="whitespace-pre px-2 font-mono leading-5"
        :class="lineClass[line.kind]"
      >{{ line.text }}</div>
      <div v-if="truncated" class="px-2 py-1 text-muted">… diff 过长已截断</div>
    </div>
  </div>
</template>
