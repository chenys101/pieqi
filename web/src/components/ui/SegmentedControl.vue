<script setup lang="ts">
// 分段单选（原型 .seg）：2 个互斥选项用 Switch，3 个及以上用这个。
//
// 状态以 `aria-pressed` 为**唯一真相**，视觉全由属性选择器驱动 ——
// 不额外维护一份"哪个选中"的 class，避免两份状态漂移（SPEC 约定）。
//
// 配色用「凹槽 + 选中块」而非「选中块变色」：
//   槽 = surface-inset（浅 #E9ECF1 / 深 #191C22）
//   选中块 = surface（浅 #FFF / 深 #14161B）
// 浅色下选中块靠 shadow-xs 浮起；深色下 --shadow-* 一律 none，
// 层次改由底色差承担 —— 这正是 SPEC §3.5 的规则，不需要写 dark: 特例。
export interface SegmentedOption {
  value: string
  label: string
}

defineProps<{
  modelValue: string
  options: SegmentedOption[]
  /** 无障碍名：这一组在选什么（如「主题」） */
  ariaLabel?: string
}>()

const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
</script>

<template>
  <div
    class="flex gap-0.5 rounded-md bg-surface-inset p-[3px]"
    role="group"
    :aria-label="ariaLabel"
  >
    <button
      v-for="opt in options"
      :key="opt.value"
      type="button"
      class="cursor-pointer rounded px-[11px] py-[5px] text-[12.5px] font-medium whitespace-nowrap text-text-secondary transition-colors hover:text-text aria-pressed:bg-surface aria-pressed:font-semibold aria-pressed:text-text aria-pressed:shadow-xs"
      :aria-pressed="modelValue === opt.value"
      @click="emit('update:modelValue', opt.value)"
    >
      {{ opt.label }}
    </button>
  </div>
</template>
