<script setup lang="ts">
// Agent 引擎：对接支持列表勾选 + 上下排序。
//
// 选项源 = GET /api/agents 的 supported（全量 manifest）；
// 启停与顺序 = settings.agents.enabled（数组序 = 选择器展示序）。
// 不再要求手改 settings.json。
import { computed, onMounted, ref, watch } from 'vue'
import { useAgentStore } from '@/stores/agent'
import { useNotificationStore } from '@/stores/notification'
import { getAgents } from '@/services/api/agents'
import { patchSettings, type AgentSelection } from '@/services/api/settings'
import Switch from '@/components/ui/Switch.vue'
import Button from '@/components/ui/Button.vue'

const props = defineProps<{
  /** 当前 settings.agents；空 enabled 时服务端已投影为全选 */
  agents: AgentSelection | null | undefined
  disabled?: boolean
}>()

const emit = defineEmits<{
  'update:agents': [value: AgentSelection]
}>()

const agentStore = useAgentStore()
const notify = useNotificationStore()

interface SupportedItem {
  name: string
  display_name: string
  description?: string
  transport?: string
}

interface Row {
  id: string
  on: boolean
}

const supported = ref<SupportedItem[]>([])
const rows = ref<Row[]>([])
const saving = ref(false)

onMounted(async () => {
  try {
    const res = await getAgents()
    supported.value = res.supported?.length
      ? res.supported
      : (res.agents ?? []).map((a) => ({
          name: a.name,
          display_name: a.display_name,
          description: a.description,
          transport: a.transport,
        }))
    rebuildRows()
  } catch {
    // 目录拉失败：保留空列表，开关 disabled
  }
})

watch(
  () => props.agents?.enabled,
  () => rebuildRows(),
  { deep: true },
)

function rebuildRows() {
  if (!supported.value.length) return
  const enabled = props.agents?.enabled ?? []
  const enabledSet = new Set(enabled)
  const known = new Set(supported.value.map((s) => s.name))
  const next: Row[] = []
  for (const id of enabled) {
    if (known.has(id)) next.push({ id, on: true })
  }
  for (const s of supported.value) {
    if (!enabledSet.has(s.name)) next.push({ id: s.name, on: false })
  }
  rows.value = next
}

const byId = computed(() => {
  const m = new Map<string, SupportedItem>()
  for (const s of supported.value) m.set(s.name, s)
  return m
})

function infoOf(id: string): SupportedItem | undefined {
  return byId.value.get(id)
}

async function persist(nextRows: Row[]) {
  if (props.disabled || saving.value) return
  const enabled = nextRows.filter((r) => r.on).map((r) => r.id)
  if (enabled.length === 0) {
    notify.error('至少保留一个 Agent')
    return
  }
  const prev = rows.value
  rows.value = nextRows
  saving.value = true
  try {
    const next: AgentSelection = {
      ...(props.agents ?? {}),
      enabled,
    }
    const saved = await patchSettings({ agents: next })
    emit('update:agents', saved.agents)
    // 选择器目录必须立刻跟上，否则设置页勾了、新建页还是旧名单
    await agentStore.loadCatalog(true)
  } catch (e) {
    rows.value = prev
    notify.error(e instanceof Error ? e.message : '保存失败')
  } finally {
    saving.value = false
  }
}

function toggle(id: string, on: boolean) {
  const next = rows.value.map((r) => (r.id === id ? { ...r, on } : r))
  void persist(next)
}

function move(index: number, delta: number) {
  const j = index + delta
  if (j < 0 || j >= rows.value.length) return
  const next = rows.value.slice()
  const tmp = next[index]!
  next[index] = next[j]!
  next[j] = tmp
  void persist(next)
}
</script>

<template>
  <div class="flex flex-col gap-2 px-[14px] pb-3" data-testid="agent-engine-list">
    <div
      v-for="(row, i) in rows"
      :key="row.id"
      class="flex items-center gap-2 rounded-md border border-border-subtle bg-background px-3 py-2"
      :class="!row.on && 'opacity-60'"
    >
      <div class="flex shrink-0 flex-col gap-0.5">
        <Button
          size="sm"
          variant="ghost"
          class="!h-6 !min-w-0 !px-1.5 text-[11px]"
          :disabled="disabled || saving || i === 0"
          aria-label="上移"
          @click="move(i, -1)"
        >
          ↑
        </Button>
        <Button
          size="sm"
          variant="ghost"
          class="!h-6 !min-w-0 !px-1.5 text-[11px]"
          :disabled="disabled || saving || i === rows.length - 1"
          aria-label="下移"
          @click="move(i, 1)"
        >
          ↓
        </Button>
      </div>
      <div class="min-w-0 flex-1">
        <div class="flex min-w-0 items-baseline gap-1.5">
          <span class="text-[13px] text-text">{{ infoOf(row.id)?.display_name || row.id }}</span>
          <span
            class="shrink-0 font-mono text-[11px] text-text-tertiary"
            :title="infoOf(row.id)?.transport"
          >
            {{ infoOf(row.id)?.transport }}
          </span>
        </div>
        <div
          v-if="infoOf(row.id)?.description"
          class="truncate text-[11px] text-text-tertiary"
          :title="infoOf(row.id)?.description"
        >
          {{ infoOf(row.id)?.description }}
        </div>
        <div class="mt-0.5 text-[11px] text-text-tertiary">
          活跃 {{ agentStore.agents.find((s) => s.agentId === row.id)?.activeSessions ?? 0 }} ·
          总计 {{ agentStore.agents.find((s) => s.agentId === row.id)?.totalSessions ?? 0 }}
        </div>
      </div>
      <Switch
        :model-value="row.on"
        :label="`启用 ${infoOf(row.id)?.display_name || row.id}`"
        :disabled="disabled || saving"
        @update:model-value="(v) => toggle(row.id, v)"
      />
    </div>
    <p v-if="!rows.length" class="px-1 text-[11px] text-text-tertiary">加载对接列表…</p>
  </div>
</template>
