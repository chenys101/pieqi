<script setup lang="ts">
// 多行文本输入（SPEC §4.4）。
//
// 与 Input 同规格（surface-subtle 底 / --radius-sm / 3px ring / text-tertiary placeholder），
// 差异只在：高度由 rows 决定、可纵向 resize；autoResize 时随内容长高且封顶（原型 composer
// 的 max-height:120px 行为 —— 输入条不能因为多写两行就把时间线顶走）。
import { nextTick, ref, watch } from 'vue'

const props = withDefaults(
  defineProps<{
    modelValue?: string
    placeholder?: string
    disabled?: boolean
    invalid?: boolean
    rows?: number
    /** 随内容自动长高（不出现滚动条），上限由 maxHeight 控制 */
    autoResize?: boolean
    /** autoResize 的高度上限（px） */
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

// 先置 auto 再读 scrollHeight：否则高度只增不减（内容删掉后不会缩回去）
function resize() {
  const t = el.value
  if (!t) return
  t.style.height = 'auto'
  t.style.height = Math.min(t.scrollHeight, props.maxHeight) + 'px'
}

watch(
  () => props.modelValue,
  () => {
    if (props.autoResize) nextTick(resize)
  },
)

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
    :rows="autoResize ? undefined : rows"
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
