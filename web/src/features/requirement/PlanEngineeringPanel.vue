<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import Button from '@/components/ui/Button.vue'
import Input from '@/components/ui/Input.vue'
import Select from '@/components/ui/Select.vue'
import Textarea from '@/components/ui/Textarea.vue'
import * as api from '@/services/api/requirements'
import { ApiError } from '@/services/api/client'
import type { PlanAssertion, PlanDraft, PlanNodeRevision, PlanView } from '@/types/requirement'
import { useNotificationStore } from '@/stores/notification'

const props = defineProps<{ requirementId: string }>()
const emit = defineEmits<{ refreshed: [] }>()
const notify = useNotificationStore()

const loading = ref(false)
const busy = ref(false)
const view = ref<PlanView | null>(null)
const stage = ref('engineering')
const nodeId = ref('')
const nodeTitle = ref('')
const nodeRole = ref('backend')
const assertKind = ref('open_question')
const assertText = ref('')

const blockers = computed(() => view.value?.publish_blocked ?? [])
const nodes = computed(() => view.value?.draft?.nodes ?? [])
const assertions = computed(() => view.value?.draft?.assertions ?? [])
const pin = computed(() => view.value?.execution_pin)
const revisions = computed(() => view.value?.revisions ?? [])

async function reload() {
  if (!props.requirementId) return
  loading.value = true
  try {
    view.value = await api.getRequirementPlan(props.requirementId)
    stage.value = view.value.draft?.stage || 'engineering'
  } catch (e) {
    notify.error(e instanceof Error ? e.message : '加载 Plan 失败')
  } finally {
    loading.value = false
  }
}

watch(
  () => props.requirementId,
  () => {
    void reload()
  },
)
onMounted(() => {
  void reload()
})

function draftPayload(extra?: Partial<PlanDraft>): PlanDraft {
  return {
    stage: stage.value,
    nodes: [...nodes.value],
    assertions: [...assertions.value],
    ...extra,
  }
}

async function saveDraft(next: PlanDraft) {
  busy.value = true
  try {
    const res = await api.savePlanDraft(props.requirementId, next)
    view.value = {
      ...(view.value ?? { draft: res.draft }),
      draft: res.draft,
      publish_blocked: res.publish_blocked,
      execution_pin: view.value?.execution_pin,
      revisions: view.value?.revisions,
    }
    notify.success('草案已保存')
  } catch (e) {
    notify.error(e instanceof Error ? e.message : '保存失败')
  } finally {
    busy.value = false
  }
}

async function addNode() {
  const id = nodeId.value.trim() || `n-${Date.now().toString(36)}`
  const title = nodeTitle.value.trim()
  if (!title) {
    notify.error('节点标题必填')
    return
  }
  const n: PlanNodeRevision = { node_id: id, title, role: nodeRole.value, prompt: title }
  await saveDraft(draftPayload({ nodes: [...nodes.value, n] }))
  nodeId.value = ''
  nodeTitle.value = ''
}

async function addAssertion() {
  const text = assertText.value.trim()
  if (!text) {
    notify.error('断言正文必填')
    return
  }
  const a: PlanAssertion = {
    id: `a-${Date.now().toString(36)}`,
    kind: assertKind.value,
    text,
    blocks_publish: assertKind.value === 'open_question' || assertKind.value === 'assumption',
  }
  await saveDraft(draftPayload({ assertions: [...assertions.value, a] }))
  assertText.value = ''
}

async function acceptAssumption(a: PlanAssertion) {
  const next = assertions.value.map((x) =>
    x.id === a.id ? { ...x, accepted_risk: true, accepted_by: 'ui', blocks_publish: false } : x,
  )
  await saveDraft(draftPayload({ assertions: next }))
}

async function publish(materialize: boolean) {
  busy.value = true
  try {
    await api.savePlanDraft(props.requirementId, draftPayload({ stage: stage.value }))
    await api.publishPlan(props.requirementId, { materialize, force: true })
    notify.success(materialize ? '已发布并物化为任务' : '已发布 PlanRevision')
    await reload()
    emit('refreshed')
  } catch (e) {
    notify.error(e instanceof ApiError ? e.message : e instanceof Error ? e.message : '发布失败')
    await reload()
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <section
    class="space-y-3 rounded-lg border border-border bg-elevated/30 p-3"
    data-testid="plan-engineering-panel"
  >
    <div class="flex flex-wrap items-center gap-2">
      <h2 class="text-xs font-semibold text-text-secondary">Plan Engineering</h2>
      <span class="text-[11px] text-muted">草案 → 门禁 → 发布 → 物化</span>
      <Button size="sm" variant="ghost" :disabled="loading || busy" @click="reload">刷新</Button>
    </div>

    <p v-if="loading" class="text-[11px] text-muted">加载中…</p>
    <template v-else>
      <label class="block space-y-1">
        <span class="text-[11px] text-muted">阶段</span>
        <Select v-model="stage" size="sm" :disabled="busy">
          <option value="intent">intent</option>
          <option value="business">business</option>
          <option value="system">system</option>
          <option value="solution">solution</option>
          <option value="engineering">engineering</option>
          <option value="review">review</option>
        </Select>
      </label>

      <div class="space-y-1.5">
        <h3 class="text-[11px] font-medium text-text-secondary">节点（{{ nodes.length }}）</h3>
        <ul v-if="nodes.length" class="space-y-1 text-[12px]">
          <li v-for="n in nodes" :key="n.node_id" class="rounded border border-border/60 px-2 py-1">
            <span class="font-mono text-[11px] text-muted">{{ n.node_id }}</span>
            · {{ n.title }}
            <span class="text-muted">({{ n.role || '?' }})</span>
          </li>
        </ul>
        <div class="flex flex-wrap gap-2">
          <Input v-model="nodeId" size="sm" placeholder="node_id" class="min-w-[7rem] flex-1" />
          <Input v-model="nodeTitle" size="sm" placeholder="标题" class="min-w-[8rem] flex-1" />
          <Select v-model="nodeRole" size="sm" class="w-28">
            <option value="design">design</option>
            <option value="frontend">frontend</option>
            <option value="backend">backend</option>
            <option value="test">test</option>
            <option value="review">review</option>
          </Select>
          <Button size="sm" :disabled="busy" @click="addNode">加节点</Button>
        </div>
      </div>

      <div class="space-y-1.5">
        <h3 class="text-[11px] font-medium text-text-secondary">断言（{{ assertions.length }}）</h3>
        <ul v-if="assertions.length" class="space-y-1 text-[12px]">
          <li v-for="a in assertions" :key="a.id" class="rounded border border-border/60 px-2 py-1">
            <span class="text-muted">{{ a.kind }}</span>
            · {{ a.text }}
            <span v-if="a.blocks_publish" class="text-warning"> · 阻断</span>
            <Button
              v-if="a.kind === 'assumption' && !a.accepted_risk"
              size="sm"
              variant="ghost"
              class="ml-1"
              :disabled="busy"
              @click="acceptAssumption(a)"
            >
              接受风险
            </Button>
          </li>
        </ul>
        <div class="flex flex-wrap gap-2">
          <Select v-model="assertKind" size="sm" class="w-36">
            <option value="decision">decision</option>
            <option value="system_fact">system_fact</option>
            <option value="assumption">assumption</option>
            <option value="open_question">open_question</option>
          </Select>
          <Textarea v-model="assertText" rows="2" class="min-w-[12rem] flex-1 text-sm" placeholder="断言" />
          <Button size="sm" :disabled="busy" @click="addAssertion">加断言</Button>
        </div>
      </div>

      <div
        v-if="blockers.length"
        class="rounded-md border border-warning/40 bg-warning/10 px-2.5 py-2 text-[11px] text-warning"
      >
        <p class="font-medium">门禁未过：</p>
        <ul class="mt-1 list-disc pl-4">
          <li v-for="(b, i) in blockers" :key="i">{{ b }}</li>
        </ul>
      </div>

      <div class="flex flex-wrap gap-2">
        <Button size="sm" variant="secondary" :disabled="busy" @click="saveDraft(draftPayload({ stage }))">
          保存草案
        </Button>
        <Button size="sm" :disabled="busy || blockers.length > 0" @click="publish(false)">仅发布</Button>
        <Button size="sm" :disabled="busy || blockers.length > 0" @click="publish(true)">发布并物化</Button>
      </div>

      <div v-if="pin || revisions.length" class="space-y-1 border-t border-border/50 pt-2 text-[11px] text-muted">
        <p v-if="pin">
          Pin：
          <span class="font-mono text-text">{{ pin.plan_revision_id.slice(0, 8) }}…</span>
        </p>
        <p>Revisions：{{ revisions.length }}</p>
      </div>
    </template>
  </section>
</template>
