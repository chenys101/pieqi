<script setup lang="ts">
// 通用按钮：variant / size / loading（方案 §31）
import { computed } from 'vue'
import Spinner from './Spinner.vue'

const props = withDefaults(
  defineProps<{
    variant?: 'primary' | 'secondary' | 'danger' | 'ghost'
    size?: 'sm' | 'md'
    loading?: boolean
    disabled?: boolean
    type?: 'button' | 'submit'
    title?: string
  }>(),
  { variant: 'secondary', size: 'md', loading: false, disabled: false, type: 'button' },
)

const variantClass = computed(
  () =>
    ({
      primary: 'bg-accent text-white hover:opacity-90 border border-transparent',
      secondary: 'bg-surface text-text border border-border hover:bg-elevated',
      danger: 'bg-transparent text-error border border-error/40 hover:bg-error/10',
      ghost: 'bg-transparent text-muted border border-transparent hover:bg-elevated hover:text-text',
    })[props.variant],
)

const sizeClass = computed(
  () =>
    ({
      sm: 'text-xs px-2.5 py-1 gap-1',
      md: 'text-sm px-3.5 py-1.5 gap-1.5',
    })[props.size],
)
</script>

<template>
  <button
    :type="type"
    :title="title"
    :disabled="disabled || loading"
    class="inline-flex items-center justify-center rounded-md font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50"
    :class="[variantClass, sizeClass]"
  >
    <Spinner v-if="loading" class="h-3.5 w-3.5" />
    <slot />
  </button>
</template>
