<script setup lang="ts">
// 干预输入区（方案 §21）：底部输入框 + 右下角双态按钮
// 运行中（pending/running/waiting_input）→ ■ 中止；终态 → ↑ 发送（续问 Resume）
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
  <div class="flex items-end gap-2 border-t border-border bg-surface/80 px-3 py-2.5 backdrop-blur">
    <div class="min-w-0 flex-1">
      <PromptInput
        v-model="text"
        :rows="2"
        :placeholder="canCancel ? '运行中… 可中止' : '发送补充… 输入 / 触发命令/Skill'"
        :disabled="canCancel"
        @submit="!canCancel && onSend()"
      />
    </div>
    <!-- 双态按钮：中止 ■ / 发送 ↑ -->
    <button
      v-if="canCancel"
      class="shrink-0 rounded-lg bg-error/15 p-2.5 text-error transition-colors hover:bg-error/25"
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
      class="shrink-0 rounded-lg bg-accent p-2.5 text-white transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-40"
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
</template>
