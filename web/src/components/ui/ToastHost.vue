<script setup lang="ts">
// Toast 宿主：右下角（桌面）/ 顶部（移动）
import { useNotificationStore } from '@/stores/notification'

const store = useNotificationStore()

const kindClass = {
  error: 'border-error/40 bg-error/10 text-error',
  success: 'border-success/40 bg-success/10 text-success',
  info: 'border-info/40 bg-info/10 text-info',
} as const
</script>

<template>
  <Teleport to="body">
    <div class="pointer-events-none fixed inset-x-0 top-3 z-[60] flex flex-col items-center gap-2 px-4 md:inset-x-auto md:bottom-4 md:right-4 md:top-auto md:items-end">
      <TransitionGroup
        enter-active-class="event-enter"
        leave-active-class="transition-opacity duration-200"
        leave-to-class="opacity-0"
      >
        <div
          v-for="t in store.toasts"
          :key="t.id"
          class="pointer-events-auto flex max-w-sm items-start gap-2 rounded-lg border px-3.5 py-2.5 text-sm shadow-lg"
          :class="kindClass[t.kind]"
          role="status"
          @click="store.dismiss(t.id)"
        >
          <span class="flex-1 break-all">{{ t.message }}</span>
        </div>
      </TransitionGroup>
    </div>
  </Teleport>
</template>
