<script setup lang="ts">
// 输入框 + 斜杠补全（V1 autocomplete.js 的 Vue 化，方案 §21）：
// 输入 / 后弹出 Commands + Skills 分组菜单，↑↓ 选择、回车插入、Esc 关闭。
//
// 外壳交给 T1 的 Textarea（R8：全站输入控件共用一套外壳，见 SPEC §4.4）——
// 本组件只负责补全逻辑，不再自己写一份输入框样式。
// 斜杠补全依赖**真实光标位置**，故用 Textarea 暴露的内部元素（`defineExpose({ el })`）。
import { computed, nextTick, ref, watch } from 'vue'
import Textarea from '@/components/ui/Textarea.vue'
import { useAppStore } from '@/stores/app'

const props = withDefaults(
  defineProps<{
    modelValue: string
    placeholder?: string
    rows?: number
    disabled?: boolean
    ariaLabel?: string
  }>(),
  { placeholder: '', rows: 3, disabled: false, ariaLabel: '输入指令' },
)
const emit = defineEmits<{ 'update:modelValue': [value: string]; submit: [] }>()

const appStore = useAppStore()

// 只声明用得到的那一项：Textarea 通过 defineExpose 交出内部 textarea
const taRef = ref<{ el: HTMLTextAreaElement | null } | null>(null)
const menuOpen = ref(false)
const activeIndex = ref(-1)
const currentQuery = ref<string | null>(null)

/** 光标类操作（selectionStart / setSelectionRange / focus）必须打在真实元素上 */
const el = () => taRef.value?.el ?? null

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

/**
 * 检测光标前最近的 /（前须为行首/空格），返回查询词；无匹配置 null 关闭菜单。
 *
 * 入参是**刚输入的值**而不是 `props.modelValue` —— props 要等父组件重渲染才更新，
 * 而 `selectionStart` 是当下的；两者混用会让识别一直慢一个字符（`/ab` 时菜单还停在 `/a`）。
 */
function detectQuery(value: string) {
  const t = el()
  if (!t) return
  const before = value.slice(0, t.selectionStart ?? value.length)
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

function onInput(val: string) {
  emit('update:modelValue', val)
  detectQuery(val)
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
  const t = el()
  if (!t) return
  const caret = t.selectionStart ?? props.modelValue.length
  const before = props.modelValue.slice(0, caret)
  const slash = before.lastIndexOf('/')
  const after = props.modelValue.slice(caret)
  const next = props.modelValue.slice(0, slash) + '/' + item.name + ' ' + after
  emit('update:modelValue', next)
  menuOpen.value = false
  currentQuery.value = null
  nextTick(() => {
    const pos = slash + item.name.length + 2
    t.focus()
    t.setSelectionRange(pos, pos)
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
    <Textarea
      ref="taRef"
      :model-value="modelValue"
      :rows="rows"
      :placeholder="placeholder"
      :disabled="disabled"
      :resizable="false"
      :aria-label="ariaLabel"
      @update:model-value="onInput"
      @keydown="onKeydown"
      @blur="onBlur"
    />
    <!-- 斜杠补全菜单（贴输入框上方） -->
    <div
      v-if="menuOpen"
      class="absolute inset-x-0 bottom-full z-20 mb-1 max-h-56 overflow-y-auto rounded-[var(--radius-md)] border border-border bg-elevated shadow-xl"
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
