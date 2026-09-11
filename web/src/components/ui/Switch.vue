<script setup lang="ts">
// 开关（SPEC §4.4 / §5.4）。
//
// 状态以 **aria-checked 为唯一真相**，视觉全由属性选择器驱动 ——
// 不额外维护 class 状态：两套状态一定会漂移，而漂移时你无从判断哪个对。
//
// 点击即切换，没有中间态；禁用时不给任何反馈（这不是「暂不可用」，是「不由你改」）。
withDefaults(
  defineProps<{
    modelValue?: boolean
    /** 无障碍名：开关自身没有可见文字，必须由调用方给出 */
    label: string
    disabled?: boolean
  }>(),
  { modelValue: false, disabled: false },
)

const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()
</script>

<template>
  <button
    type="button"
    role="switch"
    :aria-checked="modelValue"
    :aria-label="label"
    :disabled="disabled"
    class="relative h-[22px] w-[38px] shrink-0 cursor-pointer rounded-full border border-border-strong bg-surface-muted transition-colors focus-visible:ring-2 focus-visible:ring-accent/40 focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-50 [&[aria-checked=true]]:border-accent [&[aria-checked=true]]:bg-accent [&[aria-checked=true]>span]:translate-x-4 [&[aria-checked=true]>span]:border-transparent [&[aria-checked=true]>span]:bg-white"
    @click="emit('update:modelValue', !modelValue)"
  >
    <span
      class="absolute top-[2px] left-[2px] h-4 w-4 rounded-full border border-border-strong bg-surface transition-transform"
    />
  </button>
</template>
