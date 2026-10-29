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
import EventImages from './EventImages.vue'
import RewindCard from './RewindCard.vue'
import ModelSwitchNote from './ModelSwitchNote.vue'
import ModeSwitchNote from './ModeSwitchNote.vue'
import PlanCard from './PlanCard.vue'

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
 *
 * 位置（2026-10 定案）：**在内容下方**，不再贴在气泡左侧。理由有三条，
 * 每条都能独立成立：
 *   1. 归属无歧义：贴在左侧时它落在**上一条**内容的右侧（一轮里正文被工具调用
 *      切成多段，时间戳是给"这条气泡"的，视觉上却像上一条的落款）；
 *      放在下方只可能属于正上方那块内容。
 *   2. 不抢内容宽度：移动端 390px 下气泡最多只剩 ~85% 宽，左侧时间戳还要再吃掉
 *      ~34px；下方是独立一行，气泡能占满内容区。
 *   3. 两个方向对称：用户气泡（右对齐）与正文气泡（左对齐）现在用的是同一个
 *      结构，不再需要为对齐各写一套 flex 摆放。
 */
const clock = computed(() => formatClock(props.event.timestamp))
</script>

<template>
  <!-- ⚠️ 两种气泡都用「flex-col + items-end/items-start」：块级子项在交叉轴上
       本来就按 align-items 摆放，所以下方那行时间不用再写任何 margin/self-* 去贴边，
       气泡右对齐时时间也自然贴右 —— 手写 ml-auto/mr-auto 会在改对齐时忘记同步。 -->
  <div v-if="event.type === 'user_message'" class="event-enter flex flex-col items-end gap-0.5">
    <UserBubble :text="event.payload.text ?? ''" />
    <!-- 时间在气泡下方（右对齐 → 贴气泡右下）。极小字号 + 单行 + 单行高，
         高度只加 ~12px，换掉的是原来那一行被挤掉的气泡宽度。 -->
    <span
      v-if="clock"
      class="shrink-0 font-mono text-[10px] leading-none text-text-tertiary tabular-nums"
      :title="`发送于 ${clock}`"
      data-testid="prompt-at"
    >{{ clock }}</span>
  </div>

  <div v-else-if="event.type === 'text_delta'" class="event-enter flex flex-col items-start gap-0.5">
    <TextBubble :text="event.payload.text ?? ''" :task-id="event.taskId" />
    <span
      v-if="clock"
      class="shrink-0 font-mono text-[10px] leading-none text-text-tertiary tabular-nums"
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
  <!-- tool_result：有图时**图在上、文字卡在其下**。
       为什么不是把图塞进 ToolResultCard 内部：那张卡默认折叠，图片若跟着折叠，
       用户得先点开才知道"这里有图"—— 而图本身就是要看的东西，不是附注。
       文字（`<type>image</type><content>image/png 489x561…</content>` 那段信封）
       仍然保留且可展开：它是"这张图从哪来、多大"的可核对事实。 -->
  <div v-else-if="event.type === 'tool_result'" class="flex flex-col gap-1.5">
    <EventImages
      v-if="event.payload.images?.length"
      :task-id="event.taskId"
      :images="event.payload.images"
    />
    <ToolResultCard
      :tool-name="event.payload.toolName ?? '结果'"
      :result="event.payload.result"
      :is-error="event.payload.isError"
    />
  </div>
  <div v-else-if="event.type === 'status'" class="event-enter px-1 text-xs text-muted">
    {{ event.payload.text }}
  </div>
  <!-- 用户回退代码（Feedback P0）：黄色卡片，时间线留痕 -->
  <RewindCard v-else-if="event.type === 'rewind'" :event="event" />
  <!-- 会话内切换模型：灰色单行留痕（不抢戏，但可回溯"这一轮跑在哪个模型上"） -->
  <ModelSwitchNote v-else-if="event.type === 'model_switch'" :event="event" />
  <ModeSwitchNote v-else-if="event.type === 'mode_switch'" :event="event" />
  <PlanCard v-else-if="event.type === 'plan'" :event="event" />
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
