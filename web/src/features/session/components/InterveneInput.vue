<script setup lang="ts">
// 干预输入区（方案 §21）：底部输入框，模型选择器与动作按钮一起嵌在框内。
// 运行中（pending/running/waiting_input）→ ■ 中止；终态 → ✈ 发送（续问 Resume）。
//
// 布局（R8 AC-R8-01）：外条满宽承载 border-top / 底色，内层再用 `max-w-3xl` 约束，
// 与 SessionHeader / SessionTimeline 的正文左边界**同一条线**。
// PC 与移动端是同一套 DOM（不做 md: 分支），按钮位置在两端一致。
//
// 模型选择器：**嵌在输入框内**（PromptInput 的 #lead 插槽，与发送按钮同一行，
// 在左），点开是**自下而上**的弹层（ModelPicker）。选完立即落库 —— 会话级切换，
// 不是"下一轮临时用一下"，所以不需要等到发送才生效，理由见 SessionPage.switchModel。
import { computed, ref } from 'vue'
import PromptInput from './PromptInput.vue'
import ModelPicker from './ModelPicker.vue'
import type { AgentModelDto } from '@/types/api'

const props = defineProps<{
  canCancel: boolean
  canSend: boolean
  /** 提交在途（防重） */
  busy?: boolean
  /** 该任务 agent 的可选模型（空 = 该 agent 不下发清单） */
  models?: AgentModelDto[]
  /** 模型清单在途（弹层内显示加载中） */
  modelsLoading?: boolean
  /** 清单是否已尝试加载过（区分"还在读"与"确实没有"） */
  modelsLoaded?: boolean
  /** 会话当前模型（不透明值，空 = Agent 默认）。触发器上显示的就是它。 */
  currentModel?: string
  /** 切换请求在途 */
  switching?: boolean
  /** 运行中不可切换（这一轮的模型已经定死了，见后端 setTaskModel） */
  switchDisabled?: boolean
}>()
const emit = defineEmits<{ send: [text: string]; cancel: []; 'switch-model': [value: string] }>()

const text = ref('')
const submitting = ref(false)

const sendDisabled = computed(() => !text.value.trim() || !props.canSend || submitting.value)

async function onSend() {
  if (sendDisabled.value) return
  submitting.value = true
  try {
    emit('send', text.value.trim())
    text.value = ''
  } finally {
    submitting.value = false
  }
}

function onPick(value: string) {
  emit('switch-model', value)
}
</script>

<template>
  <div class="border-t border-border bg-surface/80 px-3 py-2.5 backdrop-blur md:px-4" data-testid="composer-bar">
    <div class="mx-auto w-full max-w-3xl" data-testid="composer-inner">
      <PromptInput
        v-model="text"
        :rows="2"
        aria-label="补充指令"
        :placeholder="canCancel ? '运行中… 可中止' : '发送补充… 输入 / 触发命令/Skill'"
        :disabled="canCancel"
        @submit="!canCancel && onSend()"
      >
        <!-- 模型选择器：输入框内左下角，与右下角的发送/中止按钮同一行。
             ⚠️ 必须放在 #lead 而不是 #actions：#actions 整行是 justify-end，
             塞进去会被推到最右、和发送按钮挤在一起。 -->
        <template #lead>
          <ModelPicker
            :model="currentModel"
            :models="models"
            :models-loaded="modelsLoaded"
            :disabled="switchDisabled || switching"
            @update:model="onPick"
          />
        </template>

        <!-- 双态按钮：中止 ■ / 发送 ✈，嵌在输入框内右下角
             视觉 36×36，命中区用伪元素外扩到 44×44（AC-R8-04 允许视觉 36） -->
        <template #actions>
          <button
            v-if="canCancel"
            class="pointer-events-auto relative shrink-0 rounded-[var(--radius-sm)] bg-error/15 p-2.5 text-error transition-colors after:absolute after:-inset-1 after:content-[''] hover:bg-error/25 focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-accent/40"
            title="中止生成"
            aria-label="中止"
            @click="emit('cancel')"
          >
            <svg viewBox="0 0 24 24" class="h-4 w-4" aria-hidden="true">
              <rect x="6" y="6" width="12" height="12" rx="2" fill="currentColor" />
            </svg>
          </button>
          <button
            v-else
            class="pointer-events-auto relative shrink-0 rounded-[var(--radius-sm)] bg-accent p-2.5 text-white transition-opacity after:absolute after:-inset-1 after:content-[''] hover:opacity-90 focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-accent/40 disabled:cursor-not-allowed disabled:opacity-40"
            :disabled="sendDisabled"
            title="发送 (Ctrl+Enter)"
            aria-label="发送"
            @click="onSend"
          >
            <svg viewBox="0 0 24 24" class="h-4 w-4" aria-hidden="true">
              <path d="M22 2 11 13M22 2l-7 20-4-9-9-4 20-7z" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round" />
            </svg>
          </button>
        </template>
      </PromptInput>

      <!-- 该 agent 没有可选模型时的说明。
           为什么放在输入框**外面**而不是弹层里：这种情况根本点不开弹层
           （弹层只在有清单时才有内容），说明必须另有出处，否则用户永远看不到。 -->
      <p
        v-if="modelsLoaded && !models?.length"
        class="mt-1 px-1 text-xs text-muted"
        data-testid="composer-model-unsupported"
      >
        该 Agent 不提供可选模型{{ currentModel ? '，沿用其默认模型' : '' }}。
      </p>
    </div>
  </div>
</template>