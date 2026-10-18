<script setup lang="ts">
// 多行文本输入（SPEC §4.4）。
//
// 与 Input 同规格（surface-subtle 底 / --radius-sm / 3px ring / text-tertiary placeholder），
// 差异只在：高度由 rows 决定、可纵向 resize；autoResize 时随内容长高且封顶（原型 composer
// 的 max-height:120px 行为 —— 输入条不能因为多写两行就把时间线顶走）。
import { nextTick, onMounted, ref, watch } from 'vue'

const props = withDefaults(
  defineProps<{
    modelValue?: string
    placeholder?: string
    disabled?: boolean
    invalid?: boolean
    rows?: number
    /** 随内容自动长高（不出现滚动条），上限由 maxHeight 控制 */
    autoResize?: boolean
    /**
     * autoResize 的高度上限（px）。
     *
     * `rows` 只在**没能**自动量高的那条路上兜底（见下方 resize 注释），
     * 所以它必须**小于**上限 —— 否则矮视口下选中的就是那个过高的兜底值。
     * 输入条场景传的是 `66`（两行），而默认 120 是给独立表单输入框的。
     */
    maxHeight?: number
    /** 允许用户拖拽改高。输入条场景传 false —— 拖高会把时间线顶走 */
    resizable?: boolean
    ariaLabel?: string
  }>(),
  { rows: 3, invalid: false, disabled: false, autoResize: false, maxHeight: 120, resizable: true },
)

const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

const el = ref<HTMLTextAreaElement | null>(null)

// 供需要**光标位置**的上层使用（PromptInput 的斜杠补全要读 selectionStart、回填后要
// setSelectionRange）。只交出元素本身 + 聚焦，不另包一层 API —— 包装只会在下次需要
// 新能力时再改一次组件（而这里已经是"内部元素"这个最小暴露面了）。
defineExpose({ el, focus: () => el.value?.focus() })

/**
 * 已量到的高度（内容为空时的**单行**高度）。
 *
 * 为什么要自己记住它，而不是每轮都 `height:'auto'` 再量：
 *   · 读回的那个值最多就是 maxHeight —— 客户端的 scrollHeight **包含**
 *     替换元素自己的内边距（textarea 的上下 py-2），所以拿它当 min-height
 *     套回同一个元素上会**再叠加一次内边距**，输入到第二行时高度直接跳两行；
 *   · 空框量出来的是"单行"，不是"rows 行"：空框保持 `rows` 的高度，内容一旦
 *     非空就由内容决定（一行文字就是一行高，不会被 rows 撑成两行空框）。
 * 记住这个**已知**值，再取 max(单行, 内容高) 并封顶 maxHeight，高度就只由内容决定。
 */
let minHeight = 0

// 先置 auto 再读 scrollHeight：否则高度只增不减（内容删掉后不会缩回去）
function resize() {
  const t = el.value
  if (!t) return
  t.style.height = 'auto'
  const content = t.scrollHeight
  // 空框 / 还没量到过：保持 auto，让高度退回 `rows`。
  // 空框时 scrollHeight 就是"单行"高度，记下来当**非空**时的底线 ——
  // 内容只有一行时它比 rows 矮，但不能矮过一行。
  if (!t.value || !minHeight) {
    minHeight = content
    return
  }
  t.style.height = Math.min(Math.max(content, minHeight), props.maxHeight) + 'px'
}

watch(
  () => props.modelValue,
  () => {
    if (props.autoResize) nextTick(resize)
  },
)

// 挂载时先量一次单行高度（顺带处理"有初值"的情况：有值就会直接按内容长高）。
// 不预设 minHeight —— 只有真的量过，后面才敢用它当底线。
onMounted(() => {
  if (props.autoResize) resize()
})

function onInput(e: Event) {
  emit('update:modelValue', (e.target as HTMLTextAreaElement).value)
  if (props.autoResize) resize()
}
</script>

<template>
  <textarea
    ref="el"
    :value="modelValue"
    :placeholder="placeholder"
    :rows="rows"
    :disabled="disabled"
    :aria-label="ariaLabel"
    :aria-invalid="invalid || undefined"
    class="w-full border bg-surface-subtle px-3 py-2 text-sm leading-[1.55] text-text transition-[border-color,box-shadow] placeholder:text-text-tertiary focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-accent/40 disabled:cursor-not-allowed disabled:bg-surface-muted disabled:text-text-disabled"
    :class="[
      'rounded-[var(--radius-sm)]',
      autoResize ? 'resize-none overflow-hidden' : resizable ? 'resize-y' : 'resize-none',
      invalid
        ? 'border-error focus-visible:border-error focus-visible:ring-error/30'
        : 'border-border hover:border-border-strong focus-visible:border-accent',
    ]"
    @input="onInput"
  />
</template>
