<script setup lang="ts">
// 下拉选择（SPEC §4.4）：**复用 Input 外壳** —— 同底色 / 同 radius / 同 focus ring，
// chevron 自绘在右侧 12px。原生箭头与 Input 的视觉语言不一致，正是本组件要修的问题。
//
// 为什么仍是原生 <select> 而非自研弹层：原生元素自带键盘、屏幕阅读器与移动端选择器的
// 完整行为；自研 combobox 要自己实现 aria-activedescendant / 点击外部关闭 / 定位翻转，
// 风险大于收益。SPEC §4.4 的「菜单弹层 shadow-lg + radius-md」在当前方案下不可控 ——
// 这是为可访问性付的代价，接受（R8 的病因是"样式与 Input 不一致"，不是"缺弹层"）。
withDefaults(
  defineProps<{
    modelValue?: string | number
    disabled?: boolean
    invalid?: boolean
    size?: 'sm' | 'md'
    ariaLabel?: string
  }>(),
  { size: 'md', invalid: false, disabled: false },
)

const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

function onChange(e: Event) {
  emit('update:modelValue', (e.target as HTMLSelectElement).value)
}
</script>

<template>
  <div class="relative inline-flex w-full">
    <select
      :value="modelValue"
      :disabled="disabled"
      :aria-label="ariaLabel"
      :aria-invalid="invalid || undefined"
      class="w-full cursor-pointer appearance-none border bg-surface-subtle pr-8 text-text transition-[border-color,box-shadow] focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-accent/40 disabled:cursor-not-allowed disabled:bg-surface-muted disabled:text-text-disabled"
      :class="[
        size === 'sm' ? 'h-8 pl-2.5 text-[12.5px]' : 'h-9 pl-3 text-sm',
        'rounded-[var(--radius-sm)]',
        invalid
          ? 'border-error focus-visible:border-error focus-visible:ring-error/30'
          : 'border-border hover:border-border-strong focus-visible:border-accent',
      ]"
      @change="onChange"
    >
      <slot />
    </select>
    <!-- chevron 右侧 12px；pointer-events-none 是关键 —— 否则点它时点到的是 svg，select 不展开 -->
    <svg
      class="pointer-events-none absolute top-1/2 right-3 h-3.5 w-3.5 -translate-y-1/2 text-text-tertiary"
      viewBox="0 0 24 24"
      aria-hidden="true"
    >
      <path
        d="M6 9l6 6 6-6"
        stroke="currentColor"
        stroke-width="2"
        fill="none"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </svg>
  </div>
</template>
