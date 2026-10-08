<script setup lang="ts">
// ModelSwitchNote：时间线里的模型切换记录（EventModelSwitch）。
//
// 为什么切换要留痕：per-turn 的模型选择**不落库**，光看界面无法回答"这一轮到底
// 跑在哪个模型上"。切换时追加一条事件（后端 model_switch.go），这里渲染成一行，
// 于是"什么时候、从什么、换到了什么"在会话里可回溯 —— 也让"沿用当前"这句话有依据。
//
// 展示名的解析：from/to 都是 agent 下发的**不透明串**，这里拿它们去比对
// GET /api/agents/{agent}/models 的清单换成人读名；**解析不到就原样显示**，
// 不做字符串加工（拼串是 model_catalog.go 明确禁止的，见那里的注释）。
import { computed } from 'vue'
import type { AgentEvent } from '@/types/event'
import { useAgentStore } from '@/stores/agent'
import { useTaskStore } from '@/stores/task'

const props = defineProps<{ event: AgentEvent }>()

const agentStore = useAgentStore()
const taskStore = useTaskStore()

/** 事件所属任务的 agent（清单按 agent 取，跨 agent 的值没有可比性） */
const agentName = computed(() => taskStore.byId(props.event.taskId)?.agent || '')

const switchInfo = computed(() => props.event.payload.modelSwitch)

/** 不透明值 → 人读名；查不到退回原值（宁可难看，也不要给一个编出来的名字） */
function displayName(value: string): string {
  if (!value) return 'Agent 默认'
  const hit = agentStore.modelsOf(agentName.value).find((m) => m.value === value)
  return hit?.name ?? value
}

const fromName = computed(() => displayName(switchInfo.value?.from ?? ''))
const toName = computed(() => displayName(switchInfo.value?.to ?? ''))

/** 载荷缺失（老数据 / 归一化失败）时退回后端写好的 Text，再兜一句通用文案 */
const text = computed(() => props.event.payload.text || '切换了模型')
</script>

<template>
  <div
    class="event-enter flex items-start gap-1.5 px-1 py-0.5 text-xs text-muted"
    data-testid="model-switch-note"
  >
    <span class="mt-px shrink-0" aria-hidden="true">◈</span>
    <span class="min-w-0 break-words">
      <template v-if="switchInfo">
        模型：<span class="text-text-tertiary line-through">{{ fromName }}</span>
        <span class="mx-1" aria-hidden="true">→</span>
        <span class="text-text">{{ toName }}</span>
      </template>
      <template v-else>{{ text }}</template>
    </span>
  </div>
</template>