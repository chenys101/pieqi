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
//
// 焦点纪律：触发器与弹层里所有可点元素都 `@mousedown.prevent`。
// 它们现在排在输入框**下方**的动作行里（见 PromptInput），已经不是"盖在文字上"的浮层，
// 但用户的操作顺序没变：先写一半、点一下换个模型、接着写。不拦 mousedown 的话，
// 点触发器会把 input 的焦点抢走 —— 接着敲的字谁也不收（实测：点开再关掉，
// `document.activeElement` 停在触发器上，输入框里一个字都没进去）。
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

/**
 * 触发器上的短名：太长的名字会把输入框挤扁，截断并用 title 兜住全名。
 *
 * 阈值从 20 收到 14：动作行里现在还有加图按钮与发送按钮（新建任务页三个控件同行），
 * 20 个字符的名字在 375 宽下会把行挤到换行（实测：`DeepSeek-V4.1-Flash` 19 字符时
 * 新建任务页的动作行从 62px 涨到 104px）。14 字符约 100px，三个控件在同一行放得下；
 * 鼠标悬停有全名，弹层头上也有「当前：全名」。
 */
const TRIGGER_MAX_NAME = 14
const currentLabel = computed(() => {
  const n = currentName.value
  return n.length > TRIGGER_MAX_NAME ? `${n.slice(0, TRIGGER_MAX_NAME - 1)}…` : n
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
  <!-- ⚠️ testid 分两层，别再混用：
         · 这层 div 是「选择器整体」（`model-picker`）—— 量位置、判断"用户能不能点到"
           都以它为准（它是 `pointer-events-auto` 的那个盒子）；
         · 里面的 `<button>` 是「触发器」（`model-picker-trigger`）。
       ModelPicker 通过 `<Button>` 渲染触发器，而 Button 是多根组件 —— 非 props 属性只能
       落到 Button 的根节点，也就是 `<button>` 本身，于是这个 testid **不会**出现在这层
       div 上（2026-10-19 曾误以为在 div 上，写出过"被自己拦截"的假红）。 -->
  <!-- `min-w-0`：可压缩（理由见下面按钮的注释），否则窄屏时它撑住宽度、把动作行顶成两行 -->
  <div ref="root" class="pointer-events-auto relative min-w-0" data-testid="model-picker">
    <!-- ⚠️ 触发器按钮的 `min-w-0` 必须显式给：按钮默认 `min-width:auto`，遇到长名字时
         **不肯缩**，于是 flex 行宁可换行也不压缩它 —— 375 宽下新建任务页（加图按钮 +
         选择器 + 发送按钮同行）会把行顶成两行、输入条白白高出一截（实测 62px → 104px）。
         配合 `max-w-[10rem]` 与按钮内的 `truncate`，窄屏时文字自己省略、行不换。 -->
    <button
      type="button"
      class="flex min-h-8 min-w-0 max-w-[10rem] flex-1 items-center gap-1.5 rounded-[var(--radius-sm)] px-2 py-1 text-xs transition-colors hover:bg-elevated focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-accent/40 disabled:cursor-not-allowed disabled:opacity-50"
      :class="open ? 'bg-elevated text-text' : 'text-muted'"
      :disabled="disabled"
      :aria-label="`模型：${currentName}，点击切换`"
      :aria-expanded="open"
      aria-haspopup="listbox"
      data-testid="model-picker-trigger"
      @click="open = !open"
      @mousedown.prevent
    >
      <!-- ⚠️「◈」与「▾」必须 `shrink-0`、只有文字 `truncate`：动作行变窄时若让
           箭头先被压没，用户就看不到"这里能展开"。文字截断有 title 兜住全名。 -->
      <span class="shrink-0">◈</span>
      <span class="min-w-0 truncate" :title="currentName">{{ currentLabel }}</span>
      <!-- 展开时箭头朝上：弹层在触发器上方，朝下会指错方向 -->
      <span class="shrink-0 text-[9px] transition-transform" :class="open ? 'rotate-180' : ''">▾</span>
    </button>

    <!-- 弹层：贴输入框上沿向上展开（bottom-full）。
         ⚠️ 动作行现在在输入框**下方**，选择器离视口底部只有 ~50px —— 弹层向上展开会盖住
         时间线，而时间线是**滚动容器**（自己的层叠上下文）：只给 z-30 是压不过它的，
         实测 Playwright 点选项时报 "session-timeline intercepts pointer events"，
         真机上则是"点一下弹层，点到的是后面的消息"。所以 z 要高于时间线所在的层级。 -->
    <div
      v-if="open"
      class="absolute bottom-full left-0 z-50 mb-1.5 w-[min(21rem,calc(100vw-1.5rem))] overflow-hidden rounded-[var(--radius-md)] border border-border bg-elevated shadow-xl"
      data-testid="model-picker-popup"
    >
      <!-- 空清单：说明为什么没有可选项，而不是给一个空壳弹层 -->
      <p
        v-if="!hasCatalog"
        class="px-3 py-2.5 text-xs leading-relaxed text-muted"
        data-testid="model-picker-empty"
        @mousedown.prevent
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
            @mousedown.prevent
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
              @mousedown.prevent
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