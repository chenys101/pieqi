<script setup lang="ts">
// Session Timeline（方案 §17/§39）：滚动容器 + 事件流 + 思考占位。
// 滚动策略：底部跟随 / 强制到底（切换会话、提交后）/ 翻历史不打断。
import { computed, watch } from 'vue'
import type { Ref } from 'vue'
import { useSessionStore } from '@/stores/session'
import { useTaskStore } from '@/stores/task'
import { useTimelineScroll } from '@/composables/useSession'
import TimelineEventView from '@/features/timeline/components/TimelineEventView.vue'
import ThinkingBadge from '@/features/timeline/components/ThinkingBadge.vue'
import TextBubble from '@/features/timeline/components/TextBubble.vue'

const props = defineProps<{
  taskId: string
  /** 强制滚动标记消费函数（由 useSession 提供） */
  consumeForceScroll: () => boolean
}>()

const sessionStore = useSessionStore()
const taskStore = useTaskStore()

const events = computed(() => sessionStore.events(props.taskId))
const task = computed(() => taskStore.byId(props.taskId))

/** 思考占位：提交后标记 / 冷启动（运行中且无事件无输出）兜底 */
const showThinking = computed(() => {
  if (!task.value) return false
  const active = task.value.status === 'running' || task.value.status === 'pending'
  if (!active) return false
  if (sessionStore.isThinking(task.value.id)) return true
  return events.value.length === 0 && !task.value.output
})

/** 旧任务兜底：无 events 但有 output */
const outputFallback = computed(() => events.value.length === 0 && !!task.value?.output)

const { el, onScroll, scrollToEnd } = useTimelineScroll(
  events as unknown as Ref<unknown[]>,
  props.consumeForceScroll,
)

// 切换会话：跳到最新
watch(
  () => props.taskId,
  () => scrollToEnd(),
  { immediate: true },
)
</script>

<template>
  <div
    ref="el"
    class="min-h-0 flex-1 overflow-y-auto px-3 py-4 md:px-4"
    data-testid="session-timeline"
    @scroll.passive="onScroll"
  >
    <div class="mx-auto flex max-w-3xl flex-col gap-2.5">
      <TimelineEventView v-for="event in events" :key="event.id" :event="event" />
      <!-- 旧任务兜底：直接展示累积输出 -->
      <TextBubble v-if="outputFallback" :text="task!.output!" />
      <ThinkingBadge v-if="showThinking" />
    </div>
  </div>
</template>
