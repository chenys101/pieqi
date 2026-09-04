<script setup lang="ts">
// EvidenceCard：P1 Evidence（p1-design.md §7-8）—— 验证证据快照 + 「带证据继续」。
// Evidence → Continue 是 Feedback 从展示系统升级为 Agent Control System 的关键闭环：
// 用户指令 + 当前证据（变更/checks/预览/错误）由后端组装为续问 prompt 走 Resume。
import { computed, ref, watch } from 'vue'
import { continueWithEvidence, getEvidence } from '@/services/api/feedback'
import type { CheckSummaryDto, EvidenceDto } from '@/types/api'
import Button from '@/components/ui/Button.vue'
import Spinner from '@/components/ui/Spinner.vue'
import { useNotificationStore } from '@/stores/notification'

const props = defineProps<{
  taskId: string
  /** Agent 执行中禁止续问（后端也会 409 兜底） */
  canContinue: boolean
}>()

const emit = defineEmits<{ continued: [prompt: string] }>()

const notify = useNotificationStore()
const evidence = ref<EvidenceDto | null>(null)
const loading = ref(false)
const instruction = ref('')
const continuing = ref(false)
/** Continue 成功后回显后端组装出的 prompt（审计） */
const appendedPrompt = ref<string | null>(null)

const checkIcon: Record<CheckSummaryDto['status'], string> = {
  success: '✓',
  failed: '✗',
  running: '⟳',
  pending: '…',
  skipped: '-',
}

const hasSignal = computed(() => {
  const ev = evidence.value
  if (!ev) return false
  return ev.changes.files > 0 || ev.checks.length > 0 || ev.errors > 0
})

async function refresh() {
  loading.value = true
  try {
    evidence.value = await getEvidence(props.taskId)
  } catch {
    /* 静默：面板内已有错误提示通道 */
  } finally {
    loading.value = false
  }
}

/** 带当前证据继续：后端组装 prompt → append_prompt → 新 Turn 接续 */
async function onContinue() {
  continuing.value = true
  try {
    const res = await continueWithEvidence(props.taskId, instruction.value.trim() || undefined)
    appendedPrompt.value = res.appended_prompt
    notify.success('已带证据继续（新对话轮已启动）')
    emit('continued', res.appended_prompt)
  } catch (err) {
    notify.error(err instanceof Error ? err.message : '续问失败')
  } finally {
    continuing.value = false
  }
}

watch(
  () => props.taskId,
  () => {
    appendedPrompt.value = null
    refresh()
  },
  { immediate: true },
)
</script>

<template>
  <div class="rounded-lg border border-border/60 bg-surface/60 px-3 py-2.5">
    <div class="flex items-center gap-2 text-xs">
      <span class="font-semibold">证据</span>
      <span class="text-muted">当前时刻验证快照</span>
      <Button variant="ghost" size="sm" class="ml-auto" title="刷新" @click="refresh">↻</Button>
    </div>

    <div v-if="loading && !evidence" class="flex items-center gap-2 py-3 text-xs text-muted">
      <Spinner class="h-3 w-3" /> 加载证据…
    </div>

    <template v-else-if="evidence">
      <!-- 无信号：变更/检查/错误全空 -->
      <div v-if="!hasSignal" class="py-2 text-xs text-muted">暂无变更与检查记录</div>

      <!-- 有信号：紧凑证据卡（变更 / 检查 / 错误 / 预览） -->
      <div v-else class="mt-1.5 flex flex-col gap-1 text-xs">
        <div v-if="evidence.changes.files" class="text-muted">
          变更 {{ evidence.changes.files }} 个文件
          <span class="font-mono">
            <span class="text-success">+{{ evidence.changes.additions }}</span>
            <span class="ml-1 text-error">-{{ evidence.changes.deletions }}</span>
          </span>
        </div>
        <div v-for="ck in evidence.checks" :key="ck.id" class="flex items-center gap-1.5 text-muted">
          <span :class="ck.status === 'success' ? 'text-success' : ck.status === 'failed' ? 'text-error' : ''">
            {{ checkIcon[ck.status] }}
          </span>
          <span class="truncate font-mono" :title="ck.name">{{ ck.name }}</span>
          <span v-if="ck.status === 'failed' && ck.exit_code" class="text-error">exit {{ ck.exit_code }}</span>
        </div>
        <div v-if="evidence.errors > 0" class="text-error">末轮 {{ evidence.errors }} 个工具调用失败</div>
        <div v-if="evidence.preview" class="text-muted">预览：{{ evidence.preview.state }}</div>
        <!-- 每文件一行摘要（默认折叠 3 条，超出可展开） -->
        <details v-if="evidence.diff_brief.length" class="text-muted">
          <summary class="cursor-pointer select-none text-[11px]">文件清单（{{ evidence.diff_brief.length }}）</summary>
          <div v-for="(line, i) in evidence.diff_brief" :key="i" class="truncate font-mono text-[11px]">{{ line }}</div>
        </details>
      </div>
    </template>

    <!-- Continue 成功后回显组装出的 prompt（审计） -->
    <div v-if="appendedPrompt" class="mt-2">
      <div class="text-[11px] text-muted">已发出（后端组装的续问 prompt）：</div>
      <pre class="mt-1 max-h-40 overflow-auto whitespace-pre-wrap rounded border border-border/60 bg-background px-2 py-1.5 font-mono text-[11px] leading-4 text-muted">{{ appendedPrompt }}</pre>
    </div>

    <!-- 续问输入 + 发送（Agent 执行中禁用） -->
    <div v-else class="mt-2 flex items-start gap-2">
      <input
        v-model="instruction"
        type="text"
        placeholder="补充指令（可空），如：请继续处理 build 失败"
        class="min-w-0 flex-1 rounded-md border border-border bg-background px-2.5 py-1.5 text-xs outline-none placeholder:text-muted/70 focus:border-accent/60"
        :disabled="!canContinue || continuing"
        @keyup.enter="canContinue && onContinue()"
      />
      <Button
        variant="primary"
        size="sm"
        :loading="continuing"
        :disabled="!canContinue"
        :title="canContinue ? '' : 'Agent 执行中，稍后再试'"
        @click="onContinue"
      >带证据继续</Button>
    </div>
  </div>
</template>
