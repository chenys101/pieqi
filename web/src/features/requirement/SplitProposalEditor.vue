<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import Button from '@/components/ui/Button.vue'
import Select from '@/components/ui/Select.vue'
import ModelPicker from '@/features/session/components/ModelPicker.vue'
import { useAgentStore } from '@/stores/agent'
import type { ProposedTaskNode } from '@/types/requirement'
import {
  LANES,
  isLongPrompt,
  laneMeta,
  promptLenLabel,
  roleToLane,
  type LaneId,
} from './lanes'

export interface DraftNode extends ProposedTaskNode {
  /** 客户端编辑态：展开 prompt */
  _expanded?: boolean
  /** 客户端：行内改 agent/model */
  _editingAssign?: boolean
}

const props = defineProps<{
  nodes: ProposedTaskNode[]
  busy?: boolean
}>()

const emit = defineEmits<{
  confirm: [nodes: ProposedTaskNode[]]
  discard: []
}>()

const agentStore = useAgentStore()
const catalog = computed(() => agentStore.catalog)

/** 可编辑草案（含软删回收） */
const draft = ref<DraftNode[]>([])
const trash = ref<DraftNode[]>([])
const openLanes = ref<Set<LaneId>>(new Set(['design', 'fe', 'be', 'test']))
const laneAssign = reactive<Record<string, { agent: string; model: string }>>({})

const modal = reactive({
  open: false,
  mode: 'add' as 'add' | 'edit',
  lane: 'design' as LaneId,
  taskId: '',
  title: '',
  prompt: '',
})

let seq = 1

watch(
  () => props.nodes,
  (nodes) => {
    draft.value = (nodes || []).map((n) => ({ ...n, depends_on: [...(n.depends_on || [])] }))
    trash.value = []
    for (const l of LANES) {
      if (!laneAssign[l.id]) laneAssign[l.id] = { agent: '', model: '' }
    }
    // 默认展开有票的泳道
    openLanes.value = new Set(
      LANES.filter((l) => draft.value.some((n) => roleToLane(n.role) === l.id)).map((l) => l.id),
    )
    if (openLanes.value.size === 0) openLanes.value.add('design')
  },
  { immediate: true, deep: true },
)

interface LaneGroup {
  meta: (typeof LANES)[number]
  tasks: Array<DraftNode & { taskNo: number }>
  agent: string
  model: string
}

const noByTempId = computed(() => {
  const m: Record<string, number> = {}
  let no = 1
  for (const lane of LANES) {
    for (const n of draft.value.filter((x) => roleToLane(x.role) === lane.id)) {
      m[n.temp_id] = no++
    }
  }
  return m
})

const groups = computed<LaneGroup[]>(() => {
  return LANES.map((meta) => {
    const tasks = draft.value
      .filter((n) => roleToLane(n.role) === meta.id)
      .map((n) => ({ ...n, taskNo: noByTempId.value[n.temp_id] || 0 }))
    const a = laneAssign[meta.id] || { agent: '', model: '' }
    return { meta, tasks, agent: a.agent, model: a.model }
  })
})

const visibleGroups = computed(() =>
  groups.value.filter((g) => g.meta.id !== 'other' || g.tasks.length > 0),
)

function depLabels(depends?: string[]): string[] | null {
  if (!depends?.length) return null
  return depends.map((id) => {
    const n = noByTempId.value[id]
    return n != null ? `#${n}` : id
  })
}

function effective(n: DraftNode, g: LaneGroup) {
  if (n.agent) return { agent: n.agent, model: n.model || '', from: 'task' as const }
  return { agent: g.agent || '', model: g.model || '', from: 'wave' as const }
}

function agentLabel(id: string) {
  return catalog.value.find((a) => a.id === id)?.name || id || '未指定'
}

function modelLabel(agentId: string, model: string) {
  if (!agentId) return 'model —'
  if (!model) return '默认模型'
  return model
}

function toggleLane(id: LaneId) {
  const next = new Set(openLanes.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  openLanes.value = next
}

function applyLane(lane: LaneId) {
  const a = laneAssign[lane]
  if (!a) return
  draft.value = draft.value.map((n) => {
    if (roleToLane(n.role) !== lane) return n
    // 组级应用：清掉单票覆盖，跟组走（agent/model 写到节点上以便 confirm）
    return { ...n, agent: a.agent || undefined, model: a.model || undefined }
  })
}

function clearLaneOverrides(lane: LaneId) {
  const a = laneAssign[lane]
  draft.value = draft.value.map((n) => {
    if (roleToLane(n.role) !== lane) return n
    return {
      ...n,
      agent: a?.agent || undefined,
      model: a?.model || undefined,
      _editingAssign: false,
    }
  })
}

function onLaneAgent(lane: LaneId, agent: string) {
  laneAssign[lane] = { agent, model: '' }
  if (agent) void agentStore.loadModels(agent)
}

function toggleAssign(tempId: string) {
  draft.value = draft.value.map((n) =>
    n.temp_id === tempId ? { ...n, _editingAssign: !n._editingAssign } : n,
  )
  const n = draft.value.find((x) => x.temp_id === tempId)
  if (n?.agent) void agentStore.loadModels(n.agent)
}

function setTaskAgent(tempId: string, agent: string) {
  draft.value = draft.value.map((n) => {
    if (n.temp_id !== tempId) return n
    if (!agent) return { ...n, agent: undefined, model: undefined, _editingAssign: true }
    return { ...n, agent, model: undefined, _editingAssign: true }
  })
  if (agent) void agentStore.loadModels(agent)
}

function setTaskModel(tempId: string, model: string) {
  draft.value = draft.value.map((n) => (n.temp_id === tempId ? { ...n, model: model || undefined } : n))
}

function togglePrompt(tempId: string) {
  const next = !draft.value.find((n) => n.temp_id === tempId)?._expanded
  draft.value = draft.value.map((n) => ({
    ...n,
    _expanded: next && n.temp_id === tempId,
  }))
}

function openAdd(lane: LaneId) {
  modal.open = true
  modal.mode = 'add'
  modal.lane = lane
  modal.taskId = ''
  modal.title = ''
  modal.prompt = ''
}

function openEdit(n: DraftNode) {
  modal.open = true
  modal.mode = 'edit'
  modal.lane = roleToLane(n.role)
  modal.taskId = n.temp_id
  modal.title = n.title || ''
  modal.prompt = n.prompt || ''
}

function closeModal() {
  modal.open = false
}

function confirmModal() {
  const title = modal.title.trim()
  const prompt = modal.prompt.trim()
  if (modal.mode === 'edit') {
    draft.value = draft.value.map((n) =>
      n.temp_id === modal.taskId
        ? { ...n, title: title || n.title, prompt: prompt || n.prompt }
        : n,
    )
    closeModal()
    return
  }
  const meta = laneMeta(modal.lane)
  const id = `n${Date.now().toString(36)}${seq++}`
  const a = laneAssign[modal.lane]
  draft.value = [
    ...draft.value,
    {
      temp_id: id,
      title: title || `${meta.label} 新任务`,
      prompt: prompt || '（手工补充）请填写这一票要 agent 做什么。',
      role: meta.role,
      agent: a?.agent || undefined,
      model: a?.model || undefined,
      depends_on: [],
      covers_ac: [],
    },
  ]
  openLanes.value = new Set([...openLanes.value, modal.lane])
  closeModal()
}

function removeTask(tempId: string) {
  const idx = draft.value.findIndex((n) => n.temp_id === tempId)
  if (idx < 0) return
  const [removed] = draft.value.splice(idx, 1)
  trash.value = [...trash.value, removed]
  draft.value = draft.value.map((n) => ({
    ...n,
    depends_on: (n.depends_on || []).filter((d) => d !== tempId),
  }))
}

function restoreOne(tempId: string) {
  const idx = trash.value.findIndex((n) => n.temp_id === tempId)
  if (idx < 0) return
  const next = [...trash.value]
  const [n] = next.splice(idx, 1)
  trash.value = next
  draft.value = [...draft.value, n]
  openLanes.value = new Set([...openLanes.value, roleToLane(n.role)])
}

function restoreAll() {
  if (!trash.value.length) return
  draft.value = [...draft.value, ...trash.value]
  for (const n of trash.value) openLanes.value.add(roleToLane(n.role))
  trash.value = []
}

function toPayload(): ProposedTaskNode[] {
  return draft.value.map(({ _expanded, _editingAssign, ...n }) => {
    void _expanded
    void _editingAssign
    const lane = roleToLane(n.role)
    const g = laneAssign[lane] || { agent: '', model: '' }
    // 单票有 agent = 覆盖；否则继承组头（未点「应用到本组」也能落地）
    const agent = (n.agent || g.agent || '').trim()
    const model = (n.agent ? n.model || '' : g.model || n.model || '').trim()
    const out: ProposedTaskNode = {
      temp_id: n.temp_id,
      prompt: n.prompt,
      title: n.title,
      role: n.role,
      skills: n.skills,
      covers_ac: n.covers_ac,
      depends_on: n.depends_on,
    }
    if (agent) out.agent = agent
    if (model) out.model = model
    return out
  })
}

function onConfirm() {
  emit('confirm', toPayload())
}
</script>

<template>
  <div class="space-y-3">
    <p class="text-[11px] leading-relaxed text-muted">
      按 <code class="rounded bg-elevated px-1">design → fe → be → test</code> 分组；
      planned 可空 agent/model；点徽章改执行配置；组末「+」添加；✎ 与添加共用弹窗。
    </p>

    <div
      v-if="trash.length"
      class="flex flex-wrap items-center gap-2 rounded-lg border border-dashed border-border-strong bg-elevated/60 px-2.5 py-2 text-xs text-text-secondary"
    >
      <strong class="text-text">已删除 {{ trash.length }}</strong>
      <button
        v-for="t in trash"
        :key="t.temp_id"
        type="button"
        class="max-w-[10rem] truncate rounded-full border border-border bg-surface px-2 py-0.5 text-[11px] text-muted hover:border-accent/40 hover:text-accent"
        :title="'恢复 ' + (t.title || t.temp_id)"
        @click="restoreOne(t.temp_id)"
      >
        {{ roleToLane(t.role) }} · {{ t.title || t.temp_id }}
      </button>
      <Button size="sm" variant="ghost" class="ml-auto" :disabled="busy" @click="restoreAll">一键恢复</Button>
    </div>

    <div class="mb-1 flex flex-wrap items-center gap-1.5 font-mono text-[11px] text-muted">
      <template v-for="(l, i) in LANES.filter((x) => x.id !== 'other')" :key="l.id">
        <span v-if="i" class="text-border-strong">→</span>
        <strong class="text-accent">{{ l.label }}</strong>
      </template>
    </div>

    <section
      v-for="g in visibleGroups"
      :key="g.meta.id"
      class="overflow-hidden rounded-xl border border-border bg-surface"
      :data-lane="g.meta.id"
    >
      <button
        type="button"
        class="grid w-full grid-cols-[auto_1fr_auto] items-center gap-2 border-b border-border bg-elevated/50 px-2.5 py-2 text-left hover:bg-elevated"
        @click="toggleLane(g.meta.id)"
      >
        <span
          class="inline-flex h-7 items-center gap-1 rounded-lg px-2 font-mono text-[11px] font-bold"
          :class="{
            'bg-info/15 text-info': g.meta.id === 'design',
            'bg-accent/15 text-accent': g.meta.id === 'fe',
            'bg-warning/15 text-warning': g.meta.id === 'be',
            'bg-success/15 text-success': g.meta.id === 'test',
            'bg-elevated text-muted': g.meta.id === 'other',
          }"
        >
          {{ g.meta.label }}
          <span class="opacity-70">· {{ g.tasks.length }}</span>
        </span>
        <div class="min-w-0">
          <div class="flex flex-wrap items-center gap-1.5 text-[12px] font-semibold">
            <span
              v-if="g.tasks.length"
              class="rounded-full border border-border bg-elevated px-1.5 py-0.5 font-mono text-[10.5px] font-medium text-muted"
            >
              #{{ g.tasks[0].taskNo }}{{ g.tasks.length > 1 ? `–#${g.tasks[g.tasks.length - 1].taskNo}` : '' }}
            </span>
            <span v-else class="text-[11px] font-medium text-muted">空</span>
          </div>
          <p class="mt-0.5 truncate text-[11px] text-muted">
            {{ g.meta.hint
            }}{{ g.tasks.length ? ' · ' + g.tasks.map((t) => t.title || t.temp_id).slice(0, 2).join(' · ') : ' · 点下方 + 添加' }}
          </p>
        </div>
        <span class="text-muted transition" :class="openLanes.has(g.meta.id) ? 'rotate-90' : ''">›</span>
      </button>

      <template v-if="openLanes.has(g.meta.id)">
        <div class="grid grid-cols-1 gap-2 border-b border-border p-2 sm:grid-cols-[1fr_1fr_auto] sm:items-end">
          <label class="min-w-0 space-y-1">
            <span class="sr-only">本组 Agent</span>
            <Select
              :model-value="g.agent"
              size="sm"
              class="w-full"
              aria-label="本组 Agent"
              :disabled="busy"
              @update:model-value="onLaneAgent(g.meta.id, $event)"
            >
              <option value="">Agent（可选）</option>
              <option v-for="a in catalog" :key="a.id" :value="a.id">{{ a.name }}</option>
            </Select>
          </label>
          <div class="min-w-0">
            <ModelPicker
              v-if="g.agent"
              :model="g.model"
              :models="agentStore.modelsOf(g.agent)"
              :models-loaded="!!agentStore.modelsLoaded[g.agent]"
              :disabled="busy"
              @update:model="laneAssign[g.meta.id] = { agent: g.agent, model: $event }"
            />
            <Select v-else size="sm" class="w-full" disabled model-value="">
              <option value="">默认 Model</option>
            </Select>
          </div>
          <div class="flex flex-wrap gap-1.5 sm:justify-end">
            <Button size="sm" variant="ghost" :disabled="busy" @click="applyLane(g.meta.id)">应用到本组</Button>
            <Button size="sm" variant="ghost" :disabled="busy" @click="clearLaneOverrides(g.meta.id)">清单任务覆盖</Button>
          </div>
        </div>

        <div>
          <div
            v-for="t in g.tasks"
            :key="t.temp_id"
            class="grid grid-cols-[2rem_1fr_auto] gap-2 border-t border-border px-2.5 py-2 first:border-t-0"
            :class="{ 'bg-accent/5': !!t.agent && t.agent !== g.agent }"
          >
            <div class="pt-0.5 font-mono text-[11px] tabular-nums text-muted">#{{ t.taskNo }}</div>
            <div class="min-w-0">
              <div class="flex flex-wrap items-baseline gap-1.5">
                <span class="text-[13px] font-semibold">{{ t.title || t.temp_id }}</span>
              </div>
              <div class="relative mt-1">
                <p
                  class="whitespace-pre-wrap break-words text-[11.5px] leading-relaxed text-text-secondary"
                  :class="t._expanded ? 'max-h-[min(40vh,240px)] overflow-auto rounded-lg border border-border bg-elevated p-2' : 'line-clamp-2'"
                >
                  {{ t.prompt }}
                </p>
              </div>
              <button
                v-if="isLongPrompt(t.prompt)"
                type="button"
                class="mt-1 text-[11px] font-semibold text-accent hover:underline"
                @click="togglePrompt(t.temp_id)"
              >
                {{ t._expanded ? '收起' : '展开全文' }}
                <span class="ml-1 font-medium text-muted">{{ promptLenLabel(t.prompt) }}</span>
              </button>
              <div class="mt-1.5 flex flex-wrap items-center gap-1.5">
                <template v-if="t._editingAssign">
                  <Select
                    size="sm"
                    class="max-w-[9rem]"
                    :model-value="t.agent || ''"
                    :disabled="busy"
                    @update:model-value="setTaskAgent(t.temp_id, $event)"
                  >
                    <option value="">跟随本组</option>
                    <option v-for="a in catalog" :key="a.id" :value="a.id">{{ a.name }}</option>
                  </Select>
                  <ModelPicker
                    v-if="t.agent"
                    :model="t.model || ''"
                    :models="agentStore.modelsOf(t.agent)"
                    :models-loaded="!!agentStore.modelsLoaded[t.agent]"
                    :disabled="busy"
                    @update:model="setTaskModel(t.temp_id, $event)"
                  />
                  <button type="button" class="text-[11px] text-accent" @click="toggleAssign(t.temp_id)">收起</button>
                </template>
                <template v-else>
                  <button
                    type="button"
                    class="rounded-full border border-border bg-elevated px-2 py-0.5 text-[10.5px] text-text-secondary hover:border-accent/40 hover:text-accent"
                    :class="!effective(t, g).agent ? 'border-dashed text-muted' : ''"
                    title="点击修改 Agent"
                    @click="toggleAssign(t.temp_id)"
                  >
                    {{ effective(t, g).agent ? agentLabel(effective(t, g).agent) + (t.agent ? '' : ' · 组') : 'agent 未设' }}
                  </button>
                  <button
                    type="button"
                    class="rounded-full border border-border bg-elevated px-2 py-0.5 text-[10.5px] text-text-secondary hover:border-accent/40 hover:text-accent"
                    :class="!effective(t, g).agent ? 'border-dashed text-muted' : ''"
                    title="点击修改 Model"
                    @click="toggleAssign(t.temp_id)"
                  >
                    {{ modelLabel(effective(t, g).agent, effective(t, g).model) }}
                  </button>
                </template>
                <span
                  v-if="depLabels(t.depends_on)"
                  class="rounded-full border border-border bg-elevated px-2 py-0.5 font-mono text-[10.5px] text-muted"
                >
                  ← {{ depLabels(t.depends_on)!.join(' ') }}
                </span>
              </div>
            </div>
            <div class="flex flex-col items-end gap-1">
              <button
                type="button"
                class="grid h-7 w-7 place-items-center rounded-md border border-border bg-elevated text-text-secondary hover:bg-elevated"
                title="编辑标题 / Prompt"
                :disabled="busy"
                @click="openEdit(t)"
              >
                ✎
              </button>
              <button
                type="button"
                class="grid h-7 w-7 place-items-center rounded-md border border-border bg-elevated text-text-secondary hover:border-error/40 hover:bg-error/10 hover:text-error"
                title="删除任务"
                :disabled="busy"
                @click="removeTask(t.temp_id)"
              >
                ×
              </button>
            </div>
          </div>

          <div class="flex justify-center border-t border-dashed border-border px-2.5 py-2.5">
            <button
              type="button"
              class="grid h-8 w-8 place-items-center rounded-full border border-dashed border-border-strong bg-elevated text-lg text-text-secondary hover:border-accent/45 hover:bg-accent/10 hover:text-accent"
              title="添加 task"
              :disabled="busy"
              @click="openAdd(g.meta.id)"
            >
              +
            </button>
          </div>
        </div>
      </template>
    </section>

    <div class="flex flex-wrap gap-2 pt-1">
      <Button variant="primary" :disabled="busy || !draft.length" @click="onConfirm">落地为 planned</Button>
      <Button size="sm" variant="ghost" :disabled="busy" @click="emit('discard')">丢弃建议</Button>
    </div>

    <!-- 添加 / 编辑共用弹窗 -->
    <div
      v-if="modal.open"
      class="fixed inset-0 z-50 flex items-end justify-center bg-black/40 p-0 sm:items-center sm:p-4"
      @click.self="closeModal"
    >
      <div
        class="w-full max-w-md rounded-t-2xl border border-border bg-surface p-4 shadow-lg sm:rounded-2xl"
        role="dialog"
        aria-modal="true"
      >
        <h3 class="text-sm font-semibold">{{ modal.mode === 'edit' ? '编辑 task' : '添加 task' }}</h3>
        <p class="mt-1 text-xs text-muted">
          {{ modal.mode === 'edit' ? laneMeta(modal.lane).label : `添加到 ${laneMeta(modal.lane).label} 泳道` }}
        </p>
        <label class="mt-3 block space-y-1">
          <span class="text-[11px] text-muted">标题</span>
          <input
            v-model="modal.title"
            class="w-full rounded-lg border border-border bg-elevated px-2.5 py-2 text-sm"
            placeholder="例如：补登录态校验"
          />
        </label>
        <label class="mt-2 block space-y-1">
          <span class="text-[11px] text-muted">Prompt</span>
          <textarea
            v-model="modal.prompt"
            rows="8"
            class="max-h-[min(50vh,360px)] min-h-[140px] w-full resize-y rounded-lg border border-border bg-elevated px-2.5 py-2 text-[12.5px] leading-relaxed"
            placeholder="这一票要 agent 做什么（可很长，列表里会折叠）"
          />
          <span v-if="modal.prompt" class="block text-right text-[10.5px] text-muted">{{ promptLenLabel(modal.prompt) }}</span>
        </label>
        <div class="mt-3 flex justify-end gap-2">
          <Button variant="ghost" @click="closeModal">取消</Button>
          <Button variant="primary" @click="confirmModal">{{ modal.mode === 'edit' ? '保存' : '添加' }}</Button>
        </div>
      </div>
    </div>
  </div>
</template>
