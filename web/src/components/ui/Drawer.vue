<script setup lang="ts">
// Drawer：工作区工具面板容器 —— PC 右侧 dock（参与父级 flex，两栏排布）/ 移动端底部抽屉（覆盖式）。
// 与 Modal 的区别：弹窗是聚焦语义（遮罩 + 居中，做完即关）；Drawer 是面板语义（可与主视图对照、反复进出）。
// PC 形态无遮罩，不阻塞会话列交互；关闭时不在 flex 里占位，主列自然回宽。
import { onMounted, onUnmounted } from 'vue'
import { useResponsive } from '@/composables/useResponsive'

const props = withDefaults(
  defineProps<{
    open: boolean
    title?: string
    /** 是否可关闭（ESC / 移动端遮罩点击） */
    dismissable?: boolean
    /** PC 固定宽度 */
    width?: string
  }>(),
  { dismissable: true, width: '480px' },
)

const emit = defineEmits<{ close: [] }>()

const { isMobile } = useResponsive()

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape' && props.dismissable && props.open) emit('close')
}
onMounted(() => document.addEventListener('keydown', onKey))
onUnmounted(() => document.removeEventListener('keydown', onKey))
</script>

<template>
  <!-- PC：右侧 dock 面板，父级 flex 中占一列（两栏排布）；无遮罩，可与时间线对照 -->
  <Transition
    enter-active-class="transition-transform duration-200 ease-out"
    enter-from-class="translate-x-full"
    leave-active-class="transition-transform duration-150 ease-in"
    leave-to-class="translate-x-full"
  >
    <aside
      v-if="!isMobile && open"
      class="flex h-full shrink-0 flex-col border-l border-border bg-surface"
      :style="{ width }"
      role="complementary"
      aria-label="变更反馈面板"
    >
      <header v-if="title" class="flex shrink-0 items-center justify-between border-b border-border px-4 py-3">
        <h2 class="text-sm font-semibold">{{ title }}</h2>
        <button
          v-if="dismissable"
          class="rounded px-1.5 text-lg leading-none text-muted hover:text-text"
          aria-label="关闭"
          @click="emit('close')"
        >
          ×
        </button>
      </header>
      <div class="min-h-0 flex-1 overflow-y-auto p-4">
        <slot />
      </div>
    </aside>
  </Transition>

  <!-- 移动端：覆盖式底部抽屉（与 Modal 移动端形态一致：遮罩 + 贴底） -->
  <Teleport to="body">
    <Transition
      enter-active-class="transition-opacity duration-150"
      enter-from-class="opacity-0"
      leave-active-class="transition-opacity duration-100"
      leave-to-class="opacity-0"
    >
      <div
        v-if="isMobile && open"
        class="fixed inset-0 z-50 flex items-end justify-center bg-black/60 p-0 backdrop-blur-sm"
        @click.self="dismissable && emit('close')"
      >
        <div
          class="event-enter flex max-h-[90vh] w-full flex-col overflow-hidden rounded-t-xl border border-border bg-surface"
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
