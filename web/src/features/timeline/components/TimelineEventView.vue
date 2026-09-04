<script setup lang="ts">
// 事件渲染器（方案 §18）：按前端稳定事件类型分发 UI。
// 事件类型与后端协议解耦（Normalizer 负责），此处只认 AgentEventType。
import type { AgentEvent } from '@/types/event'
import UserBubble from './UserBubble.vue'
import TextBubble from './TextBubble.vue'
import ThinkingBlock from './ThinkingBlock.vue'
import ToolCard from './ToolCard.vue'
import ToolResultCard from './ToolResultCard.vue'
import RewindCard from '@/features/feedback/components/RewindCard.vue'

defineProps<{ event: AgentEvent }>()
</script>

<template>
  <UserBubble v-if="event.type === 'user_message'" :text="event.payload.text ?? ''" />
  <TextBubble v-else-if="event.type === 'text_delta'" :text="event.payload.text ?? ''" />
  <ThinkingBlock
    v-else-if="event.type === 'thinking_delta'"
    :text="event.payload.text ?? ''"
  />
  <ToolCard
    v-else-if="event.type === 'tool_call'"
    :tool-name="event.payload.toolName ?? 'tool'"
    :input="event.payload.input"
  />
  <ToolResultCard
    v-else-if="event.type === 'tool_result'"
    :tool-name="event.payload.toolName ?? '结果'"
    :result="event.payload.result"
    :is-error="event.payload.isError"
  />
  <div v-else-if="event.type === 'status'" class="event-enter px-1 text-xs text-muted">
    {{ event.payload.text }}
  </div>
  <!-- 用户回退代码（Feedback P0）：黄色卡片，时间线留痕 -->
  <RewindCard v-else-if="event.type === 'rewind'" :event="event" />
  <div
    v-else-if="event.type === 'error'"
    class="event-enter rounded-lg border border-error/40 bg-error/10 px-3 py-2 text-sm text-error"
  >
    {{ event.payload.text || '执行出错' }}
  </div>
  <div
    v-else-if="event.type === 'completed'"
    class="event-enter rounded-lg border border-success/40 bg-success/10 px-3 py-2 text-sm text-success"
  >
    {{ event.payload.text || '已完成' }}
  </div>
</template>
