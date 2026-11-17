<script setup lang="ts">
// planned 启动条：选 Agent（可选 Model）→ POST /api/tasks/:id/start。
// 选择器复用新建任务页 / 拆分编辑的 Select + ModelPicker，不另开第三套。
import { computed, onMounted, ref, watch } from 'vue'
import Select from '@/components/ui/Select.vue'
import Button from '@/components/ui/Button.vue'
import ContentColumn from '@/components/layout/ContentColumn.vue'
import ModelPicker from './ModelPicker.vue'
import { useAgentStore } from '@/stores/agent'
import { useTaskStore } from '@/stores/task'
import { useNotificationStore } from '@/stores/notification'
import type { Task } from '@/types/task'

const props = withDefaults(
  defineProps<{
    taskId: string
    /** 任务当前 agent（planned 可空） */
    agent?: string
    /** 任务当前 model（空 = agent 默认） */
    model?: string
    /**
     * 是否包一层会话底栏外壳（ContentColumn + border-t）。
     * 会话页 true；需求图弹窗内 false（外层已有 Modal padding）。
     */
    docked?: boolean
  }>(),
  { agent: '', model: '', docked: true },
)

const emit = defineEmits<{ started: [task: Task] }>()

const agentStore = useAgentStore()
const taskStore = useTaskStore()
const notify = useNotificationStore()

const selectedAgent = ref('')
const selectedModel = ref('')
const starting = ref(false)

const catalog = computed(() => agentStore.catalog)
const selectedInfo = computed(() => agentStore.byId(selectedAgent.value))
const models = computed(() => agentStore.modelsOf(selectedAgent.value))
const modelsLoaded = computed(() => !!agentStore.modelsLoaded[selectedAgent.value])
const canStart = computed(() => !!selectedAgent.value.trim() && !starting.value)
const missingAgentHint = computed(() =>
  selectedAgent.value.trim() ? '' : '启动前必须指定 Agent（agent required before start）',
)
/** docked 时用 ContentColumn 对齐会话列；弹窗内用裸 div */
const wrap = computed(() => (props.docked ? ContentColumn : 'div'))

function syncFromProps() {
  selectedAgent.value = (props.agent || '').trim()
  selectedModel.value = (props.model || '').trim()
  if (selectedAgent.value) void agentStore.loadModels(selectedAgent.value)
}

watch(
  () => [props.taskId, props.agent, props.model] as const,
  () => syncFromProps(),
  { immediate: true },
)

/** 用户改 agent：清 model 并拉清单（勿放进 selectedAgent 的 watch，否则 syncFromProps 会误清） */
function onAgentChange(v: string) {
  selectedAgent.value = v
  selectedModel.value = ''
  if (v) void agentStore.loadModels(v)
}

onMounted(() => {
  void agentStore.loadCatalog()
})

async function onStart() {
  const agent = selectedAgent.value.trim()
  if (!agent) {
    notify.error('agent required before start')
    return
  }
  starting.value = true
  try {
    const task = await taskStore.startPlannedTask(props.taskId, {
      agent,
      model: selectedModel.value.trim() || undefined,
    })
    notify.success('已启动')
    emit('started', task)
  } catch (err) {
    notify.error(err instanceof Error ? err.message : '启动失败')
  } finally {
    starting.value = false
  }
}
</script>

<template>
  <div
    :class="docked ? 'shrink-0 border-t border-border bg-surface/80 px-3 py-2.5 backdrop-blur md:px-4' : ''"
    data-testid="planned-start-panel"
  >
    <component :is="wrap" v-bind="docked ? { resizable: false } : {}">
      <div class="flex flex-col gap-2" data-testid="planned-start-inner">
        <p class="text-xs text-muted">
          {{ docked ? '任务仍在计划中。指定执行 Agent 后即可启动' : '指定执行 Agent 后即可启动本票'
          }}<span v-if="agent" class="text-text-secondary">（已预填，可直接启动）</span>。
        </p>
        <div class="flex flex-col gap-2 sm:flex-row sm:items-end">
          <label class="min-w-0 flex-1 space-y-1">
            <span class="text-[11px] font-medium text-muted">执行 Agent</span>
            <Select
              :model-value="selectedAgent"
              size="sm"
              aria-label="执行 Agent"
              data-testid="planned-agent-select"
              :disabled="starting"
              @update:model-value="onAgentChange"
            >
              <option value="">请选择 Agent</option>
              <option v-for="a in catalog" :key="a.id" :value="a.id">{{ a.name }}</option>
            </Select>
            <p v-if="docked" class="text-[11px] text-muted">
              {{ selectedInfo?.description || '选择由哪个 coding agent 执行本任务' }}
            </p>
          </label>
          <div class="min-w-0 sm:w-44">
            <span class="mb-1 block text-[11px] font-medium text-muted">模型（可选）</span>
            <ModelPicker
              v-if="selectedAgent"
              :model="selectedModel"
              :models="models"
              :models-loaded="modelsLoaded"
              :disabled="starting"
              data-testid="planned-model-picker"
              @update:model="selectedModel = $event"
            />
            <Select v-else size="sm" class="w-full" disabled model-value="">
              <option value="">先选 Agent</option>
            </Select>
          </div>
          <div class="flex shrink-0 flex-col items-stretch gap-1 sm:items-end">
            <Button
              variant="primary"
              size="sm"
              :disabled="!canStart"
              data-testid="planned-start-btn"
              :title="missingAgentHint || '启动任务'"
              @click="onStart"
            >
              {{ starting ? '启动中…' : '启动' }}
            </Button>
            <p
              v-if="missingAgentHint"
              class="text-[11px] text-warning"
              data-testid="planned-agent-required"
            >
              {{ missingAgentHint }}
            </p>
          </div>
        </div>
      </div>
    </component>
  </div>
</template>
