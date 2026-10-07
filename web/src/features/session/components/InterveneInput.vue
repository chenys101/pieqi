<script setup lang="ts">
// 干预输入区（方案 §21）：底部输入框，动作按钮嵌在框内右下角
// 运行中（pending/running/waiting_input）→ ■ 中止；终态 → ✈ 发送（续问 Resume）
//
// 布局（R8 AC-R8-01）：外条满宽承载 border-top / 底色，内层再用 `max-w-3xl` 约束，
// 与 SessionHeader / SessionTimeline 的正文左边界**同一条线** —— 之前输入条独立留
// px-3，正文在 max-w-3xl 里居中，两者在宽屏下是错开的。
// PC 与移动端是同一套 DOM（不做 md: 分支），按钮位置在两端一致。
//
// 模型选择器（本轮模型）：只在**续问**这一态出现，理由见 showModelPicker。
import { computed, ref } from 'vue'
import PromptInput from './PromptInput.vue'
import Select from '@/components/ui/Select.vue'
import type { AgentModelDto } from '@/types/api'

const props = defineProps<{
  canCancel: boolean
  canSend: boolean
  /** 提交在途（防重） */
  busy?: boolean
  /** 该任务 agent 的可选模型（空 = 不支持选模型 → 不显示选择器） */
  models?: AgentModelDto[]
  /** 模型清单在途（选择器位置显示加载中） */
  modelsLoading?: boolean
}>()
const emit = defineEmits<{ send: [text: string, model: string]; cancel: [] }>()

const text = ref('')
const submitting = ref(false)

// 本轮模型：空 = 沿用会话当前路由（不是"agent 默认"——续问是复用已有会话，
// agent 侧记着上一次的选择，这里留空才是不改变现状）。取值同样是不透明串，原样回传。
const model = ref('')

const sendDisabled = computed(() => !text.value.trim() || !props.canSend || submitting.value)

const catalog = computed(() => props.models ?? [])

/**
 * 选择器只在**真的会被采纳**时出现。
 *
 * 后端只在续问路径（Resume 起新一轮）读这个字段；运行中的 append_prompt 是往当前轮
 * 注入 stdin，带了也不生效。所以判据是"下一发送会走 Resume"——那恰好是 !canCancel
 * （运行中/等待中这里显示的是中止按钮，输入框本身是禁用的）。
 * 摆一个不生效的下拉框比不摆更糟：用户会以为自己换了模型。
 */
const showModelPicker = computed(() => props.canSend && !props.canCancel)

/** 按分组整理（dsh 按 provider 分组；无分组信息时平铺） */
const modelGroups = computed(() => {
  const out: { name: string; options: { value: string; name: string }[] }[] = []
  for (const m of catalog.value) {
    const name = m.group || ''
    let g = out.find((x) => x.name === name)
    if (!g) {
      g = { name, options: [] }
      out.push(g)
    }
    g.options.push({ value: m.value, name: m.name })
  }
  return out
})

const selectedHint = computed(() => catalog.value.find((m) => m.value === model.value)?.description || '')

async function onSend() {
  if (sendDisabled.value) return
  submitting.value = true
  try {
    emit('send', text.value.trim(), model.value)
    text.value = ''
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="border-t border-border bg-surface/80 px-3 py-2.5 backdrop-blur md:px-4" data-testid="composer-bar">
    <div class="mx-auto w-full max-w-3xl" data-testid="composer-inner">
      <!-- 本轮模型：与输入框同宽同左边界（放在同一个 max-w-3xl 容器里），
           不新起一栏 —— 它是输入条的一部分，不是页面上的第二块面板 -->
      <div
        v-if="showModelPicker && (catalog.length || modelsLoading)"
        class="mb-1.5 flex min-w-0 items-center gap-2 px-1"
        data-testid="turn-model-field"
      >
        <span class="shrink-0 text-xs text-muted">模型</span>
        <p v-if="!catalog.length" class="text-xs text-muted">正在读取可选模型…</p>
        <template v-else>
          <Select v-model="model" class="max-w-[14rem]" aria-label="本轮模型" data-testid="turn-model-select">
            <option value="">沿用当前</option>
            <template v-for="g in modelGroups" :key="g.name">
              <optgroup v-if="g.name" :label="g.name">
                <option v-for="m in g.options" :key="m.value" :value="m.value">{{ m.name }}</option>
              </optgroup>
              <template v-else>
                <option v-for="m in g.options" :key="m.value" :value="m.value">{{ m.name }}</option>
              </template>
            </template>
          </Select>
          <span class="min-w-0 flex-1 truncate text-xs text-muted" :title="selectedHint || undefined">
            {{ selectedHint || '不选则沿用会话当前模型' }}
          </span>
        </template>
      </div>

      <PromptInput
        v-model="text"
        :rows="2"
        aria-label="补充指令"
        :placeholder="canCancel ? '运行中… 可中止' : '发送补充… 输入 / 触发命令/Skill'"
        :disabled="canCancel"
        @submit="!canCancel && onSend()"
      >
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
    </div>
  </div>
</template>
