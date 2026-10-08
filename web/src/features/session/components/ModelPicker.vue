<script setup lang="ts">
// ModelPicker：嵌在输入框里的模型选择器（触发器 + **自下而上**的弹层）。
//
// 为什么不用现成的 Select.vue（原生 <select>）：原生下拉在桌面端向下展开、
// 在移动端拉起的是系统选择器 —— 两者都无法做成"贴着输入框往上弹"，
// 也无法控制移动端弹层的高度与安全区。要的就是这个弹层形态，所以自己实现。
//
// 三处移动端考量（模板里逐条标注）：
//   1. 贴底弹出 + max-h-[min(60dvh,20rem)]：小屏上盖住输入框但不遮满整屏，还能往上滚；
//   2. env(safe-area-inset-bottom)：iPhone 底部横条/Home 指示器会压住最后一项；
//   3. 每个选项 min-h-11（44px）：满足触控最小命中区。
//
// 取值纪律：value 是 agent 下发的**不透明串**，原样回传，本组件不解析不拼接。
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import type { AgentModelDto } from '@/types/api'

const props = withDefaults(
  defineProps<{
    /** 会话当前模型（不透明值）。空 = 用 agent 默认。 */
    model?: string
    /** 可选清单。空 = 该 agent 不下发清单。 */
    models?: AgentModelDto[]
    /** 清单是否已尝试加载过（区分"还在读"与"确实没有"） */
    modelsLoaded?: boolean
    disabled?: boolean
  }>(),
  { model: '', models: () => [], modelsLoaded: false, disabled: false },
)

const emit = defineEmits<{ 'update:model': [value: string] }>()

const open = ref(false)
const root = ref<HTMLElement | null>(null)

const catalog = computed(() => props.models ?? [])
const hasCatalog = computed(() => catalog.value.length > 0)

/** 当前模型的人读名。查不到（清单没到 / 该值不在清单里）时退回中性的说法。 */
const currentName = computed(() => {
  if (!props.model) return 'Agent 默认'
  return catalog.value.find((m) => m.value === props.model)?.name ?? '当前模型'
})

/** 触发器上的短名：太长的名字会把输入框挤扁，截断并用 title 兜住全名。 */
const currentLabel = computed(() => {
  const n = currentName.value
  return n.length > 20 ? `${n.slice(0, 19)}…` : n
})

/** 按分组整理（dsh 按 provider 分组；无分组信息时归入空名组，渲染时平铺） */
const groups = computed(() => {
  const out: { name: string; options: AgentModelDto[] }[] = []
  for (const m of catalog.value) {
    const name = m.group || ''
    let g = out.find((x) => x.name === name)
    if (!g) {
      g = { name, options: [] }
      out.push(g)
    }
    g.options.push(m)
  }
  return out
})

function pick(value: string) {
  emit('update:model', value)
  open.value = false
}

function onDocPointer(e: MouseEvent) {
  if (!open.value) return
  if (root.value && !root.value.contains(e.target as Node)) open.value = false
}
function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape' && open.value) open.value = false
}
watch(open, (v) => {
  if (v) {
    document.addEventListener('mousedown', onDocPointer)
    document.addEventListener('keydown', onKey)
  } else {
    document.removeEventListener('mousedown', onDocPointer)
    document.removeEventListener('keydown', onKey)
  }
})
onBeforeUnmount(() => {
  document.removeEventListener('mousedown', onDocPointer)
  document.removeEventListener('keydown', onKey)
})

/**
 * 清单到达后自动收起弹层。
 *
 * 触发路径：进来时清单还在拉（models=[]），用户可能先点开弹层 —— 那一刻里面是空的。
 * 清单随后到达，若不关掉，弹层会**在用户没操作的情况下自己把内容换了**，
 * 看起来像是误触。空清单态另有分支（见模板 v-if），这里处理"开着时清单到了"。
 */
watch(hasCatalog, (has) => {
  if (has) open.value = false
})
</script>

<template>
  <!-- pointer-events-auto：触发器与弹层都嵌在 PromptInput 的动作行里，
       而那一行整条是 pointer-events-none（好让让出的 padding 仍能点进 textarea）。
       不在这里显式打开，触发器和弹层都点不动。 -->
  <div ref="root" class="pointer-events-auto relative" data-testid="model-picker">
    <button
      type="button"
      class="flex min-h-8 max-w-[10rem] items-center gap-1.5 rounded-[var(--radius-sm)] px-2 py-1 text-xs transition-colors hover:bg-elevated focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-accent/40 disabled:cursor-not-allowed disabled:opacity-50"
      :class="open ? 'bg-elevated text-text' : 'text-muted'"
      :disabled="disabled"
      :aria-label="`模型：${currentName}，点击切换`"
      :aria-expanded="open"
      aria-haspopup="listbox"
      data-testid="model-picker-trigger"
      @click="open = !open"
    >
      <span class="shrink-0">◈</span>
      <span class="min-w-0 truncate" :title="currentName">{{ currentLabel }}</span>
      <!-- 展开时箭头朝上：弹层在触发器上方，朝下会指错方向 -->
      <span class="shrink-0 text-[9px] transition-transform" :class="open ? 'rotate-180' : ''">▾</span>
    </button>

    <!-- 弹层：贴输入框上沿向上展开（bottom-full）。 -->
    <div
      v-if="open"
      class="absolute bottom-full left-0 z-30 mb-1.5 w-[min(21rem,calc(100vw-1.5rem))] overflow-hidden rounded-[var(--radius-md)] border border-border bg-elevated shadow-xl"
      data-testid="model-picker-popup"
    >
      <!-- 空清单：说明为什么没有可选项，而不是给一个空壳弹层 -->
      <p
        v-if="!hasCatalog"
        class="px-3 py-2.5 text-xs leading-relaxed text-muted"
        data-testid="model-picker-empty"
      >
        {{ modelsLoaded ? '该 Agent 不提供可选模型，沿用其默认模型。' : '正在读取可选模型…' }}
      </p>

      <template v-else>
        <div class="flex items-center justify-between gap-2 border-b border-border px-3 py-2">
          <span class="shrink-0 text-xs font-medium text-muted">模型</span>
          <span class="min-w-0 truncate text-xs text-muted" :title="currentName">当前：{{ currentName }}</span>
        </div>

        <!-- 移动端 ①：限高 60dvh 并内部滚动；③：每项 min-h-11（44px 触控命中区）。
             ②：pb-[env(safe-area-inset-bottom)] 避让 Home 指示器。 -->
        <div
          role="listbox"
          class="max-h-[min(60dvh,20rem)] overflow-y-auto overscroll-contain pb-[env(safe-area-inset-bottom)]"
        >
          <!-- 「Agent 默认」= 清空选择，交还 agent 自己的路由 -->
          <button
            type="button"
            role="option"
            :aria-selected="model === ''"
            class="flex min-h-11 w-full items-center gap-2 px-3 py-2 text-left text-sm transition-colors hover:bg-surface"
            :class="model === '' ? 'bg-surface' : ''"
            data-testid="model-picker-default"
            @click="pick('')"
          >
            <span class="w-3.5 shrink-0 text-accent">{{ model === '' ? '✓' : '' }}</span>
            <span class="min-w-0 flex-1 break-words">Agent 默认</span>
          </button>

          <template v-for="g in groups" :key="g.name">
            <div v-if="g.name" class="border-t border-border/50 px-3 py-1 text-xs text-muted">{{ g.name }}</div>
            <button
              v-for="m in g.options"
              :key="m.value"
              type="button"
              role="option"
              :aria-selected="m.value === model"
              class="flex min-h-11 w-full items-center gap-2 px-3 py-2 text-left transition-colors hover:bg-surface"
              :class="m.value === model ? 'bg-surface' : ''"
              @click="pick(m.value)"
            >
              <span class="w-3.5 shrink-0 text-accent">{{ m.value === model ? '✓' : '' }}</span>
              <!-- min-w-0 + break-words：模型名含 "/" 且可能很长，不能横向溢出弹层 -->
              <span class="min-w-0 flex-1">
                <span class="block break-words text-sm">{{ m.name }}</span>
                <span v-if="m.description" class="block break-words text-xs text-muted">{{ m.description }}</span>
              </span>
            </button>
          </template>
        </div>
      </template>
    </div>
  </div>
</template>