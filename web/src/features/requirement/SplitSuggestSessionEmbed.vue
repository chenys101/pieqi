<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import Button from '@/components/ui/Button.vue'
import { SessionTimeline } from '@/features/session'
import { adaptTask } from '@/services/api/client'
import * as tasksApi from '@/services/api/tasks'
import { useSessionStore } from '@/stores/session'
import { useTaskStore } from '@/stores/task'
import { onRealtimeResync } from '@/composables/useWebSocket'
import type { Task } from '@/types/task'

const props = defineProps<{
  taskId: string
  requirementId: string
}>()

const router = useRouter()
const taskStore = useTaskStore()
const sessionStore = useSessionStore()

const forceScroll = ref(false)
function consumeForceScroll() {
  const v = forceScroll.value
  forceScroll.value = false
  return v
}

const task = computed<Task | undefined>(() => taskStore.byId(props.taskId))

/** 与 SessionPage.loadDetail 同口径：全量 DTO（含 events）写入双 store。 */
async function loadDetail(id: string) {
  try {
    const dto = await tasksApi.getTaskDto(id)
    taskStore.upsertTask(adaptTask(dto))
    sessionStore.syncFromTask(dto)
  } catch {
    /* 嵌入区失败不挡打磨；全屏会话仍可点「新开页」 */
  }
}

watch(
  () => props.taskId,
  (id) => {
    if (!id) return
    forceScroll.value = true
    void loadDetail(id)
  },
  { immediate: true },
)

const unregisterResync = onRealtimeResync(() => {
  if (props.taskId) void loadDetail(props.taskId)
})
onBeforeUnmount(unregisterResync)

function openFull() {
  void router.push({
    path: `/sessions/${encodeURIComponent(props.taskId)}`,
    query: { from: 'requirement', requirement_id: props.requirementId },
  })
}
</script>

<template>
  <aside
    class="flex min-h-0 flex-col border-l border-border bg-surface-inset"
    data-testid="split-suggest-embed"
  >
    <div class="flex shrink-0 items-center gap-2 border-b border-border bg-surface px-2.5 py-2">
      <span
        class="h-1.5 w-1.5 shrink-0 rounded-full bg-success shadow-[0_0_0_3px_rgba(61,186,122,0.2)]"
        aria-hidden="true"
      />
      <strong class="min-w-0 flex-1 truncate text-xs">
        {{ task?.title || '[split]' }}
      </strong>
      <span class="shrink-0 font-mono text-[10.5px] text-muted">{{ task?.status || '…' }}</span>
      <Button size="sm" variant="ghost" data-testid="split-embed-open-full" @click="openFull">
        新开页
      </Button>
    </div>
    <div class="min-h-0 flex-1 overflow-hidden">
      <SessionTimeline :task-id="taskId" :consume-force-scroll="consumeForceScroll" />
    </div>
    <p class="shrink-0 border-t border-border px-2.5 py-1.5 text-[10.5px] text-muted">
      宽屏嵌入只读时间线；干预请点「新开页」。
    </p>
  </aside>
</template>
