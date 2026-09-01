<script setup lang="ts">
// 输入框 + 斜杠补全（V1 autocomplete.js 的 Vue 化，方案 §21）：
// 输入 / 后弹出 Commands + Skills 分组菜单，↑↓ 选择、回车插入、Esc 关闭。
import { computed, nextTick, ref, watch } from 'vue'
import { useAppStore } from '@/stores/app'

const props = withDefaults(
  defineProps<{
    modelValue: string
    placeholder?: string
    rows?: number
    disabled?: boolean
  }>(),
  { placeholder: '', rows: 3, disabled: false },
)
const emit = defineEmits<{ 'update:modelValue': [value: string]; submit: [] }>()

const appStore = useAppStore()
const inputEl = ref<HTMLTextAreaElement | null>(null)
const menuOpen = ref(false)
const activeIndex = ref(-1)

interface MatchItem {
  name: string
  description: string
  group: '命令' | 'Skills'
}

// 当前 / 查询词匹配的候选（命令 + Skills 各取前 8）
const matches = computed<MatchItem[]>(() => {
  const query = currentQuery.value
  if (query === null) return []
  const cmds = appStore.completions.commands
    .filter((c) => c.name.toLowerCase().includes(query))
    .slice(0, 8)
    .map((c) => ({ name: c.name, description: c.description, group: '命令' as const }))
  const skills = appStore.completions.skills
    .filter((s) => s.name.toLowerCase().includes(query))
    .slice(0, 8)
    .map((s) => ({ name: s.name, description: s.description, group: 'Skills' as const }))
  return [...cmds, ...skills]
})

const currentQuery = ref<string | null>(null)

/** 检测光标前最近的 /（前须为行首/空格），返回查询词；无匹配置 null 关闭菜单 */
function detectQuery() {
  const el = inputEl.value
  if (!el) return
  const before = props.modelValue.slice(0, el.selectionStart ?? props.modelValue.length)
  const slash = before.lastIndexOf('/')
  if (slash < 0 || (slash > 0 && ![' ', '\n'].includes(before[slash - 1] ?? ''))) {
    currentQuery.value = null
    return
  }
  const query = before.slice(slash + 1).toLowerCase()
  if (query.includes(' ')) {
    currentQuery.value = null
    return
  }
  currentQuery.value = query
}

function onInput(e: Event) {
  const val = (e.target as HTMLTextAreaElement).value
  emit('update:modelValue', val)
  detectQuery()
  activeIndex.value = -1
  nextTick(() => {
    menuOpen.value = currentQuery.value !== null && matches.value.length > 0
  })
}

function onKeydown(e: KeyboardEvent) {
  // Ctrl/Cmd + Enter：提交
  if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
    e.preventDefault()
    emit('submit')
    return
  }
  if (!menuOpen.value || matches.value.length === 0) return
  if (e.key === 'ArrowDown') {
    e.preventDefault()
    activeIndex.value = (activeIndex.value + 1) % matches.value.length
  } else if (e.key === 'ArrowUp') {
    e.preventDefault()
    activeIndex.value = (activeIndex.value - 1 + matches.value.length) % matches.value.length
  } else if (e.key === 'Enter' && activeIndex.value >= 0) {
    e.preventDefault()
    insert(matches.value[activeIndex.value])
  } else if (e.key === 'Escape') {
    menuOpen.value = false
  }
}

/** 把 /name 插入到光标前最近的 / 处，光标停在 name 后留空格 */
function insert(item: MatchItem) {
  const el = inputEl.value
  if (!el) return
  const caret = el.selectionStart ?? props.modelValue.length
  const before = props.modelValue.slice(0, caret)
  const slash = before.lastIndexOf('/')
  const after = props.modelValue.slice(caret)
  const next = props.modelValue.slice(0, slash) + '/' + item.name + ' ' + after
  emit('update:modelValue', next)
  menuOpen.value = false
  currentQuery.value = null
  nextTick(() => {
    const pos = slash + item.name.length + 2
    el.focus()
    el.setSelectionRange(pos, pos)
  })
}

// 分组渲染：保持「命令 → Skills」顺序，组内按 matches 顺序
const groups = computed(() => {
  const m = new Map<string, MatchItem[]>()
  for (const item of matches.value) {
    if (!m.has(item.group)) m.set(item.group, [])
    m.get(item.group)!.push(item)
  }
  return [...m.entries()]
})

// 失焦短暂延迟后关闭（让 click 先触发）
function onBlur() {
  setTimeout(() => (menuOpen.value = false), 150)
}

watch(matches, (m) => {
  if (m.length === 0) menuOpen.value = false
})
</script>

<template>
  <div class="relative">
    <textarea
      ref="inputEl"
      :value="modelValue"
      :placeholder="placeholder"
      :rows="rows"
      :disabled="disabled"
      class="w-full resize-none rounded-lg border border-border bg-background px-3 py-2.5 text-sm outline-none transition-colors placeholder:text-muted/60 focus:border-accent/60 disabled:opacity-50"
      @input="onInput"
      @keydown="onKeydown"
      @blur="onBlur"
    />
    <!-- 斜杠补全菜单（贴输入框上方） -->
    <div
      v-if="menuOpen"
      class="absolute inset-x-0 bottom-full z-20 mb-1 max-h-56 overflow-y-auto rounded-lg border border-border bg-elevated shadow-xl"
    >
      <template v-for="[group, items] in groups" :key="group">
        <div class="border-b border-border/50 px-3 py-1 text-xs text-muted last:border-b-0">{{ group }}</div>
        <button
          v-for="item in items"
          :key="item.name"
          type="button"
          class="block w-full px-3 py-1.5 text-left hover:bg-surface"
          :class="{ 'bg-surface': items.indexOf(item) === activeIndex }"
          @mousedown.prevent="insert(item)"
        >
          <div class="font-mono text-sm">/{{ item.name }}</div>
          <div class="truncate text-xs text-muted">{{ (item.description || '').slice(0, 60) }}</div>
        </button>
      </template>
    </div>
  </div>
</template>
