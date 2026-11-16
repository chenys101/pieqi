<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import Button from '@/components/ui/Button.vue'
import Input from '@/components/ui/Input.vue'
import Textarea from '@/components/ui/Textarea.vue'
import type { AcceptanceItem, SplitBlock } from '@/types/requirement'

const props = defineProps<{
  /** gate = 入口缺分支；blocked = agent 判定不能拆 */
  mode: 'gate' | 'blocked'
  block?: SplitBlock | null
  /** 409 返回的 candidate_branch */
  candidateBranch?: string
  integrationBranch?: string
  body?: string
  acceptance?: AcceptanceItem[]
  busy?: boolean
}>()

const emit = defineEmits<{
  save: [payload: { integration_branch: string; body: string; acceptance: AcceptanceItem[]; resuggest: boolean }]
  discard: []
}>()

const branch = ref('')
const acText = ref('')
const bodyText = ref('')

function acToText(items?: AcceptanceItem[]) {
  return (items || [])
    .filter((a) => !a.deferred && (a.text || '').trim())
    .map((a) => a.text.trim())
    .join('\n')
}

function textToAcceptance(raw: string): AcceptanceItem[] {
  return raw
    .split('\n')
    .map((l) => l.trim())
    .filter(Boolean)
    .map((text, i) => ({ id: `ac${i + 1}`, text, verifiable: true }))
}

watch(
  () =>
    [
      props.mode,
      props.candidateBranch,
      props.integrationBranch,
      props.body,
      props.acceptance,
      props.block?.id,
    ] as const,
  () => {
    branch.value = (props.integrationBranch || props.candidateBranch || '').trim()
    acText.value = acToText(props.acceptance)
    bodyText.value = props.body || ''
  },
  { immediate: true },
)

const title = computed(() => (props.mode === 'gate' ? '先登记合入分支' : '还不能拆'))
const summary = computed(() => {
  if (props.mode === 'gate') {
    return '未登记需求分支，不能开拆分会话（避免 Agent 猜「当前分支」）。'
  }
  return props.block?.summary || '需求不够明确，不能硬拆'
})
const reasons = computed(() => {
  if (props.mode === 'gate') return ['integration_branch 为空', '入口闸 409，未创建拆分 task']
  return props.block?.reasons || []
})
const questions = computed(() => {
  if (props.mode === 'gate') return ['合入目标是主仓哪条分支？']
  return props.block?.questions || []
})
const missing = computed(() => {
  if (props.mode === 'gate') return ['integration_branch']
  return props.block?.missing || []
})

function onSave(resuggest: boolean) {
  emit('save', {
    integration_branch: branch.value.trim(),
    body: bodyText.value,
    acceptance: textToAcceptance(acText.value),
    resuggest,
  })
}
</script>

<template>
  <div
    class="space-y-3 rounded-lg border border-warning/40 bg-black/10 p-3"
    data-testid="split-blocked-panel"
    :data-mode="mode"
  >
    <div>
      <p class="text-[13px] font-semibold text-warning">{{ title }}</p>
      <p class="mt-1 text-sm font-medium leading-snug text-text">{{ summary }}</p>
    </div>

    <ul v-if="reasons.length" class="list-disc space-y-0.5 pl-4 text-[12px] text-text-secondary">
      <li v-for="(r, i) in reasons" :key="'r' + i">{{ r }}</li>
    </ul>

    <div v-if="questions.length">
      <p class="mb-1 text-[11px] font-semibold text-text-secondary">需要你回答</p>
      <ul class="list-disc space-y-0.5 pl-4 text-[12px] text-text-secondary">
        <li v-for="(q, i) in questions" :key="'q' + i">{{ q }}</li>
      </ul>
    </div>

    <div v-if="missing.length" class="flex flex-wrap gap-1.5">
      <span
        v-for="m in missing"
        :key="m"
        class="rounded-md border border-border bg-elevated px-1.5 py-0.5 font-mono text-[10.5px] text-muted"
      >{{ m }}</span>
    </div>

    <label class="block space-y-1">
      <span class="text-[11px] text-muted">需求分支</span>
      <Input
        v-model="branch"
        size="sm"
        placeholder="如 feat/… 或 main"
        data-testid="split-polish-branch"
        :disabled="busy"
      />
    </label>
    <label class="block space-y-1">
      <span class="text-[11px] text-muted">验收 AC（一行一条）</span>
      <Textarea
        v-model="acText"
        :rows="3"
        placeholder="刷新失败统一跳登录"
        data-testid="split-polish-ac"
        :disabled="busy"
      />
    </label>
    <label class="block space-y-1">
      <span class="text-[11px] text-muted">说明</span>
      <Textarea v-model="bodyText" :rows="3" data-testid="split-polish-body" :disabled="busy" />
    </label>

    <div class="flex flex-wrap gap-2 pt-0.5">
      <Button
        variant="primary"
        size="sm"
        data-testid="split-polish-save-resuggest"
        :disabled="busy || !branch.trim()"
        @click="onSave(true)"
      >
        保存并重新拆分
      </Button>
      <Button variant="ghost" size="sm" :disabled="busy || !branch.trim()" @click="onSave(false)">
        仅保存
      </Button>
      <Button v-if="mode === 'blocked'" variant="ghost" size="sm" :disabled="busy" @click="emit('discard')">
        丢弃
      </Button>
    </div>
  </div>
</template>
