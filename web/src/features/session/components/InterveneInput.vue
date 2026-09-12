<script setup lang="ts">
// 干预输入区（方案 §21）：底部输入框 + 右下角双态按钮
// 运行中（pending/running/waiting_input）→ ■ 中止；终态 → ↑ 发送（续问 Resume）
//
// 布局（R8 AC-R8-01）：外条满宽承载 border-top / 底色，内层再用 `max-w-3xl` 约束，
// 与 SessionHeader / SessionTimeline 的正文左边界**同一条线** —— 之前输入条独立留
// px-3，正文在 max-w-3xl 里居中，两者在宽屏下是错开的。
import { computed, ref } from 'vue'
import PromptInput from './PromptInput.vue'

const props = defineProps<{
  canCancel: boolean
  canSend: boolean
  /** 提交在途（防重） */
  busy?: boolean
}>()
const emit = defineEmits<{ send: [text: string]; cancel: [] }>()

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
</script>

<template>
  <div class="border-t border-border bg-surface/80 px-3 py-2.5 backdrop-blur md:px-4" data-testid="composer-bar">
    <div class="mx-auto flex w-full max-w-3xl items-end gap-2" data-testid="composer-inner">
      <div class="min-w-0 flex-1">
        <PromptInput
          v-model="text"
          :rows="2"
          aria-label="补充指令"
          :placeholder="canCancel ? '运行中… 可中止' : '发送补充… 输入 / 触发命令/Skill'"
          :disabled="canCancel"
          @submit="!canCancel && onSend()"
        />
      </div>
      <!-- 双态按钮：中止 ■ / 发送 ↑
           视觉 36×36，命中区用伪元素外扩到 44×44（AC-R8-04 允许视觉 36） -->
      <button
        v-if="canCancel"
        class="relative shrink-0 rounded-[var(--radius-sm)] bg-error/15 p-2.5 text-error transition-colors after:absolute after:-inset-1 after:content-[''] hover:bg-error/25 focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-accent/40"
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
        class="relative shrink-0 rounded-[var(--radius-sm)] bg-accent p-2.5 text-white transition-opacity after:absolute after:-inset-1 after:content-[''] hover:opacity-90 focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-accent/40 disabled:cursor-not-allowed disabled:opacity-40"
        :disabled="sendDisabled"
        title="发送 (Ctrl+Enter)"
        aria-label="发送"
        @click="onSend"
      >
        <svg viewBox="0 0 24 24" class="h-4 w-4" aria-hidden="true">
          <path d="M12 19V5M5 12l7-7 7 7" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
      </button>
    </div>
  </div>
</template>
