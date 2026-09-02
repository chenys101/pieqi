<script setup lang="ts">
// 模态框：遮罩 / ESC 关闭 / 点击遮罩关闭（移动端自动贴底）
import { onMounted, onUnmounted } from 'vue'

const props = withDefaults(
  defineProps<{
    open: boolean
    title?: string
    /** 点击遮罩是否可关闭 */
    dismissable?: boolean
    maxWidth?: string
  }>(),
  { dismissable: true, maxWidth: 'max-w-lg' },
)

const emit = defineEmits<{ close: [] }>()

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape' && props.dismissable && props.open) emit('close')
}
onMounted(() => document.addEventListener('keydown', onKey))
onUnmounted(() => document.removeEventListener('keydown', onKey))
</script>

<template>
  <Teleport to="body">
    <Transition
      enter-active-class="transition-opacity duration-150"
      enter-from-class="opacity-0"
      leave-active-class="transition-opacity duration-100"
      leave-to-class="opacity-0"
    >
      <div
        v-if="open"
        class="fixed inset-0 z-50 flex items-end justify-center bg-black/60 p-0 backdrop-blur-sm md:items-center md:p-6"
        @click.self="dismissable && emit('close')"
      >
        <div
          class="event-enter flex max-h-[90vh] w-full flex-col overflow-hidden rounded-t-xl border border-border bg-surface md:rounded-xl"
          :class="maxWidth"
          role="dialog"
          aria-modal="true"
        >
          <div v-if="title" class="flex items-center justify-between border-b border-border px-4 py-3">
            <h2 class="text-sm font-semibold">{{ title }}</h2>
            <button
              v-if="dismissable"
              class="rounded px-1.5 text-lg leading-none text-muted hover:text-text"
              aria-label="关闭"
              @click="emit('close')"
            >
              ×
            </button>
          </div>
          <div class="min-h-0 flex-1 overflow-y-auto p-4">
            <slot />
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>
