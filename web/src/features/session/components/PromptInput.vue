<script setup lang="ts">
// 输入框 + 斜杠补全（V1 autocomplete.js 的 Vue 化，方案 §21）：
// 输入 / 后弹出 Commands + Skills 分组菜单，↑↓ 选择、回车插入、Esc 关闭。
//
// 外壳交给 T1 的 Textarea（R8：全站输入控件共用一套外壳，见 SPEC §4.4）——
// 本组件只负责补全逻辑，不再自己写一份输入框样式。
// 斜杠补全依赖**真实光标位置**，故用 Textarea 暴露的内部元素（`defineExpose({ el })`）。
//
// 结构：外层是「框」（边线 / 底色 / 焦点环 / 圆角），框内**上下两段**：textarea + 动作行。
// 动作行不再浮在 textarea 上 —— 为什么以及踩过什么，见下方 `hasFooter` 上方的长注释。
import { computed, nextTick, ref, useSlots, watch } from 'vue'
import Textarea from '@/components/ui/Textarea.vue'
import { useAppStore } from '@/stores/app'

const props = withDefaults(
  defineProps<{
    modelValue: string
    placeholder?: string
    /**
     * 空框时的行数。会传 `autoResize` 的输入条一律传 **1**：
     * 高度应当由内容决定（见下方 maxHeight），`rows` 只是量不到内容时的兜底。
     */
    rows?: number
    disabled?: boolean
    ariaLabel?: string
    /**
     * 内容超过这个高度后不再长高，改为在框内滚动（不出现内层滚动条：
     * Textarea 的 autoResize 形态是 `overflow-hidden`，长文案靠 ↑↓ 翻）。
     *
     * 判据是**输入区整块必须装得进矮视口**：375×667 的 iPhone（浏览器工具栏
     * 占掉约 110px）主区只有 ~141px，输入区若固定得太高就会把发送按钮
     * 顶到移动端底栏下面 —— 实测「发送」被 `BottomNav` 的 RouterLink 拦住点击
     * （docs/testing/ui-e2e-next.md 的 P0）。
     *
     * 口径 = **文字区**高度（不含 padding）。66 ≈ 三行；框的总高还要加上动作行
     * （它已在 textarea 下方，不再占用文字区）。从两行（44）放宽到三行，是因为
     * 动作行挪到下方后整个框多出 ~36px，而矮视口下仍有时间线可看。
     */
    maxHeight?: number
    /** 校验失败态：框的边线与焦点环换成 error —— 边线归外层框（见模板） */
    invalid?: boolean
  }>(),
  { placeholder: '', rows: 3, disabled: false, ariaLabel: '输入指令', maxHeight: undefined, invalid: false },
)
const emit = defineEmits<{
  'update:modelValue': [value: string]
  submit: []
  /**
   * 用户在输入框里粘贴。父组件据此接住"截图直接贴进来"的图片
   * （见 ImagePicker 的 `open()` 与 imageAttach.clipboardImageFiles）。
   *
   * 传 `DataTransfer | null` 而不是先探测有没有图：探测逻辑与"怎么读图"是同一件事，
   * 放在这里等于把 imageAttach 的判据抄一份，两处必然漂移。
   */
  paste: [data: DataTransfer | null]
}>()

const appStore = useAppStore()

// 动作行（发送/中止/创建/模型选择器）**排在输入框下方**，与 textarea 同一个"框"里，
// 框画成一个圆角边线与底色，textarea 自己去掉边线（PC 与移动端同一套 DOM）。
//
// #lead 是动作行的**左侧**插槽（加图 + 模型选择器），#actions 在右侧。
//
// ★★ 为什么不再"把动作行盖在 textarea 右下角"（2026-10-19 重新设计）
//
//   旧形态是：动作行 `absolute bottom-2.5` 浮在 textarea 上，textarea 垫
//   `padding-bottom: 70px` 给它让位。这条路走不通 —— 不是数值没调对，是机制不对：
//
//     · textarea 的 `scrollHeight` **包含** padding，所以让位 padding 会同时缩小
//       `clientHeight`（可视区）：它是"从可视区里扣掉"，不是"在内容之后留白"。
//       70px 的让位 + 上下各 8px 内边距，在 `rows=1` 的盒子里就把可视区压到 2px；
//     · `scroll-padding-bottom` 对 textarea 的 maxScroll **没有影响**（三种写法都试过）；
//     · 于是"输入框装得下几行"取决于"让位区占多少"，两边的数字必须手算对齐 ——
//       而模型选择器的触发器宽度会随所选模型的名字变（105px ↔ 158px+），
//       发送按钮又用 `after:-inset-1` 把命中区外扩 4px，任何一项改动都会
//       让动作行重新压到文字上。用户反馈的"超过输入框内容时覆盖模型选择按钮"
//       就是这条链的末端表现。
//
//   现在的形态把这三件事解耦：文字区高度 = `maxHeight`（上限），动作行高度 = 内容
//   自己撑开（32/44/48px 都行，永不重叠），框的总高 = 两者之和。动作行里的控件再多、
//   名字再宽，最多把动作行挤高（`flex-wrap` 换行）或截断触发器文字，**碰不到文字区**。
//
//   代价：同一个框里 textarea 与动作行之间要"看起来是同一块"，所以 textarea 用
//   `border-transparent` 把边线交给外层、`focus-within` 的高亮也画在外层。
const slots = useSlots()
const hasActions = computed(() => !!slots.actions)
const hasLead = computed(() => !!slots.lead)
const hasFooter = computed(() => hasActions.value || hasLead.value)

/**
 * 高度随内容长高，封顶在 `maxHeight`（文字区口径）。
 *
 * 判据：**传了 maxHeight 才自动长高**。不传 = 固定 rows 的表单输入框
 * （独立表单里长高会把下面的字段往下推，那才是不该长高的场景）。
 * 输入条一律传 maxHeight（见 InterveneInput）。
 */
const autoResize = computed(() => props.maxHeight !== undefined)

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

/**
 * 粘贴：图片交给父组件（ImagePicker），文本**原样放行**给浏览器默认行为。
 *
 * ★ 这里不判断"有没有图"，一律转发 —— 判据只有一份，在 imageAttach 里。
 *   本组件多一层判断就多一处会漂移的真相（"这里有图、那里没有"最容易出现）。
 *
 * ★ 绝不 preventDefault：那会连**普通文本粘贴**一起废掉。默认行为必须留着，
 *   父组件只是在旁边把图捡走。
 *
 * ★ 图片与文本**可以同时成立**（截图工具常顺手塞一段路径文本），此时两张都要：
 *   保留默认文本粘贴、同时把图加进待发列表。父组件不要为了"避免重复"去删文本 ——
 *   用户没要求删。重复一段路径文本远好过悄悄吞掉用户粘的东西。
 */
function onPaste(e: ClipboardEvent) {
  emit('paste', e.clipboardData)
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
    <!-- 「框」：textarea 在**上方**、动作行在**下方**顺序排布，共用一条边线与底色。
         焦点高亮画在这一层（textarea 自己的边线是透明的），所以看起来仍是一个整体。
         两条必须同时成立的定位约束（2026-10-19 实测踩到，坏了都不会报错）：
           · `overflow-visible` —— `overflow-hidden` 会把模型选择器的弹层**整块裁掉**，
             用户点「模型」什么也看不到。动作行的长模型名靠 `min-w-0` + `truncate`
             自己省略，不依赖裁切；
           · 本层**不要加 `relative`** —— 选择器根 div 自己就是 `relative`，弹层
             `absolute bottom-full` 相对**它**定位；外面再加一层定位容器只会让定位
             基准变复杂（实测把弹层带到输入框下方）。`overflow-visible` 之下弹层
             本来就能正常溢出到上方。 -->
    <div
      class="flex w-full min-w-0 flex-wrap overflow-visible rounded-[var(--radius-sm)] border bg-surface-subtle transition-[border-color,box-shadow] focus-within:border-accent focus-within:ring-[3px] focus-within:ring-accent/40"
      :class="
        invalid
          ? 'border-error focus-within:border-error focus-within:ring-error/30'
          : 'border-border hover:border-border-strong'
      "
      data-testid="composer-frame"
    >
      <!-- textarea 去掉自己的边线与底色（边框用 transparent 而不是 border-0：
           宽度不变，内容换行位置与"有边线"时逐像素一致，视觉基线不会整体位移）。
           圆角归外层，内层不要圆角。 -->
      <Textarea
        ref="taRef"
        :model-value="modelValue"
        :rows="rows"
        :auto-resize="autoResize"
        :max-height="maxHeight"
        :placeholder="placeholder"
        :disabled="disabled"
        :resizable="false"
        :aria-label="ariaLabel"
        class="rounded-none border-transparent bg-transparent hover:border-transparent focus-visible:ring-0"
        @update:model-value="onInput"
        @keydown="onKeydown"
        @paste="onPaste"
        @blur="onBlur"
      />
      <!-- 动作行：在 textarea **下面**的正常流里，不再浮在文字上 ⇒ 不可能压住内容。
           flex-wrap：窄屏下模型名很长时宁可让它换到下一行，也不许把触发器压扁/截断到不可读。

           ★ 间距（数字是 headless Chrome 实测的 getBoundingClientRect）：
             按钮外框到框的右沿、下沿都应是 12px。
               · 右沿 = 行的 px-3(12px)，行宽 = 框宽。
               · 下沿 = 行的 pb-3(12px)（行在流里，不再有"溢出被吃掉"的问题）。
             改行内边距或按钮尺寸后仍要**重新量**（同 ui-change-verification 的纪律）。 -->
      <!-- 动作行分两组，而不是把插槽内容平铺后 `justify-between`。
           ⚠️ 插槽渲染出来的是**多个兄弟节点**：`#lead` 里有「加图 / 模型」两个根节点，
           直接平铺时 justify-between 会把它们当成三个独立端点，模型选择器被推到
           整行中间、与加图按钮之间空出几百像素（PC 详情页实测就是这样）。
           分组后只有「左组 / 右组」两个端点：左组内部按控件自然排列，右组固定贴右。 -->
      <div
        v-if="hasFooter"
        class="flex w-full min-w-0 items-center gap-2 px-3 pb-3 pt-0.5"
        data-testid="composer-actions"
      >
        <div class="flex min-w-0 flex-1 items-center gap-1.5" data-testid="composer-actions-lead">
          <slot name="lead" />
        </div>
        <div v-if="hasActions" class="ml-auto flex shrink-0 items-center gap-1.5" data-testid="composer-actions-end">
          <slot name="actions" />
        </div>
      </div>
    </div>
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
