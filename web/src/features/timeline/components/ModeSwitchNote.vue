<script setup lang="ts">
// ModeSwitchNote：时间线里的模式切换记录（EventModeSwitch）。
import { computed } from 'vue'
import type { AgentEvent } from '@/types/event'
import { useTaskStore } from '@/stores/task'

const props = defineProps<{ event: AgentEvent }>()
const taskStore = useTaskStore()

const modes = computed(() => taskStore.byId(props.event.taskId)?.modes ?? [])
const info = computed(() => props.event.payload.modeSwitch)

function displayName(id: string): string {
  if (!id) return '（未设）'
  const hit = modes.value.find((m) => m.id === id)
  return hit?.name || id
}

const fromName = computed(() => displayName(info.value?.from ?? ''))
const toName = computed(() => displayName(info.value?.to ?? ''))
const text = computed(() => props.event.payload.text || '切换了模式')
</script>

<template>
  <div
    class="event-enter flex items-start gap-1.5 px-1 py-0.5 text-xs text-muted"
    data-testid="mode-switch-note"
  >
    <span class="mt-px shrink-0" aria-hidden="true">◎</span>
    <span class="min-w-0 break-words">
      <template v-if="info">
        模式：<span class="text-text-tertiary line-through">{{ fromName }}</span>
        <span class="mx-1" aria-hidden="true">→</span>
        <span class="text-text">{{ toName }}</span>
      </template>
      <template v-else>{{ text }}</template>
    </span>
  </div>
</template>
