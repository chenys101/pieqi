<script setup lang="ts">
// 单行文本输入（SPEC §4.4 / §3.4 / §7）。
//
// 规格三条来源，缺一不可：
//   1. SPEC §4.4 — h 36px、border + --radius-sm、focus 时 border-accent + ring、error 态 border-danger
//   2. SPEC §7  — :focus-visible 3px ring，禁止裸删 outline（用 outline-none + ring 替代，不是"没有焦点态"）
//   3. 原型 .dlg-input（合规参考实现）—— 底色 --surface-subtle、placeholder --text-tertiary(4.8:1)、
//      focus = border-accent + ring
//
// ⚠️ 底色用 surface-subtle 而非 background：background 映射的是 **canvas（页面底色）**，
//    深色下 canvas(#0C0D11) 比 surface(#14161B) 更暗 → 输入框会「凹陷」；而 surface-subtle
//    在两个主题下都相对 surface 浮起。同一个 token 用错，两个主题会表达相反的方向。
//
// ⚠️ 半径写 var(--radius-sm) 而非 rounded-md：两者当前都等于 6px，但 Tailwind 名与 SPEC token
//    错位一格，直接锚到 token 才不会被 config 变更带偏（见 tokens.css 注释）。
import { ref } from 'vue'

withDefaults(
  defineProps<{
    modelValue?: string | number
    type?: string
    placeholder?: string
    disabled?: boolean
    /** error 态：同时驱动 aria-invalid 与边框/环色；错误文案由调用方在其下方渲染（SPEC §4.4 的 12px caption） */
    invalid?: boolean
    /** sm = 32px 行内紧凑（原型 .dlg-input.is-line）；md = 36px 默认 */
    size?: 'sm' | 'md'
    autocomplete?: string
    /** 无障碍名：没有可见 label 时必须给 */
    ariaLabel?: string
  }>(),
  { type: 'text', size: 'md', invalid: false, disabled: false },
)

const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

function onInput(e: Event) {
  emit('update:modelValue', (e.target as HTMLInputElement).value)
}

const el = ref<HTMLInputElement | null>(null)

// 与 Textarea 同款最小暴露面（内部元素 + 聚焦）：RobotCreateModal 校验失败时要把焦点
// 送回具体字段——组件实例 ref 只能拿到 expose 面，拿不到原生元素。
defineExpose({ el, focus: () => el.value?.focus() })
</script>

<template>
  <input
    ref="el"
    :type="type"
    :value="modelValue"
    :placeholder="placeholder"
    :disabled="disabled"
    :autocomplete="autocomplete"
    :aria-label="ariaLabel"
    :aria-invalid="invalid || undefined"
    class="w-full border bg-surface-subtle text-text transition-[border-color,box-shadow] placeholder:text-text-tertiary focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-accent/40 disabled:cursor-not-allowed disabled:bg-surface-muted disabled:text-text-disabled"
    :class="[
      size === 'sm' ? 'h-8 px-2.5 text-[12.5px]' : 'h-9 px-3 text-sm',
      'rounded-[var(--radius-sm)]',
      invalid
        ? 'border-error focus-visible:border-error focus-visible:ring-error/30'
        : 'border-border hover:border-border-strong focus-visible:border-accent',
    ]"
    @input="onInput"
  />
</template>
