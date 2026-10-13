<script setup lang="ts">
// 事件渲染器（方案 §18）：按前端稳定事件类型分发 UI。
// 事件类型与后端协议解耦（Normalizer 负责），此处只认 AgentEventType。
import type { AgentEvent } from '@/types/event'
import { computed } from 'vue'
import { formatClock } from '@/utils/date'
import UserBubble from './UserBubble.vue'
import TextBubble from './TextBubble.vue'
import ThinkingBlock from './ThinkingBlock.vue'
import ToolCard from './ToolCard.vue'
import ToolResultCard from './ToolResultCard.vue'
import RewindCard from '@/features/feedback/components/RewindCard.vue'
import ModelSwitchNote from './ModelSwitchNote.vue'

const props = defineProps<{ event: AgentEvent }>()

/**
 * 提示词与结论的「时:分」标记。
 *
 * 只给这两类事件加（thinking / tool 那些"过程"事件不加）：一是用户要的就是
 * "这轮什么时候问的、什么时候答的"，二是过程事件一轮里可以有几十条，
 * 每条挂一个时间戳会把时间线搅成一片数字噪声。
 *
 * 取**事件自身的 timestamp**（后端落库里事件的 At），不是渲染时刻 ——
 * 刷新页面后必须完全一致，否则这个信息就没法和后端日志对账。
 */
const clock = computed(() => formatClock(props.event.timestamp))
</script>

<template>
  <div v-if="event.type === 'user_message'" class="event-enter flex items-end justify-end gap-1.5">
    <!-- 时间在气泡外侧左方（气泡右对齐，时间贴它左边才不推挤对齐线），
         极小字号 + 单行 + 不换行：移动端最窄也就多占 ~34px 一行内空间。 -->
    <span
      v-if="clock"
      class="shrink-0 pb-1 font-mono text-[10px] leading-none text-text-tertiary tabular-nums"
      :title="`发送于 ${clock}`"
      data-testid="prompt-at"
    >{{ clock }}</span>
    <UserBubble :text="event.payload.text ?? ''" />
  </div>

  <div v-else-if="event.type === 'text_delta'" class="event-enter flex items-start gap-1.5">
    <!-- min-w-0 flex-1：让气泡吃掉行内剩余宽度（否则它按内容宽度收缩、
         时间戳跟着左右横跳）。min-w-0 是 flex 子项能真正收缩的必要条件。 -->
    <TextBubble class="min-w-0 flex-1" :text="event.payload.text ?? ''" />
    <span
      v-if="clock"
      class="shrink-0 pt-1 font-mono text-[10px] leading-none text-text-tertiary tabular-nums"
      :title="`产出结论于 ${clock}`"
      data-testid="result-at"
    >{{ clock }}</span>
  </div>

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
  <!-- 会话内切换模型：灰色单行留痕（不抢戏，但可回溯"这一轮跑在哪个模型上"） -->
  <ModelSwitchNote v-else-if="event.type === 'model_switch'" :event="event" />
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
