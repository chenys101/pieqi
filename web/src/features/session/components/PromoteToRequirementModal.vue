<script setup lang="ts">
// 升格轻确认：可编辑种子 → 建需求 + Promote + 停写打磨 Task → 进需求页。
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import Modal from '@/components/ui/Modal.vue'
import Button from '@/components/ui/Button.vue'
import Input from '@/components/ui/Input.vue'
import Textarea from '@/components/ui/Textarea.vue'
import {
  createRequirement,
  promoteTaskToRequirement,
  savePlanDraft,
} from '@/services/api/requirements'
import { cancelTask } from '@/services/api/tasks'
import { getSettings } from '@/services/api/settings'
import { useNotificationStore } from '@/stores/notification'
import { useTaskStore } from '@/stores/task'
import type { Task } from '@/types/task'
import { isTerminalStatus } from '@/types/task'

const props = defineProps<{ open: boolean; task: Task }>()
const emit = defineEmits<{ close: [] }>()

const router = useRouter()
const notify = useNotificationStore()
const taskStore = useTaskStore()

const title = ref('')
const body = ref('')
const acText = ref('')
const submitting = ref(false)

watch(
  () => [props.open, props.task.id] as const,
  ([open]) => {
    if (!open) return
    title.value = (props.task.title || '').trim() || '未命名需求'
    body.value = (props.task.prompt || '').trim()
    acText.value = ''
  },
  { immediate: true },
)

const canConfirm = computed(
  () =>
    !!title.value.trim() &&
    !!props.task.projectPath?.trim() &&
    !!(body.value.trim() || acText.value.trim()) &&
    !submitting.value,
)

const blockReason = computed(() => {
  if (submitting.value) return ''
  if (!title.value.trim()) return '请先填写标题'
  if (!props.task.projectPath?.trim()) return '任务缺少项目路径'
  if (!body.value.trim() && !acText.value.trim()) return '请填写说明或至少一条验收标准'
  return ''
})

async function confirm() {
  if (!canConfirm.value) return
  submitting.value = true
  try {
    let workflowId = ''
    try {
      const s = await getSettings()
      workflowId = s.default_workflow_id || ''
    } catch {
      /* 无默认包也可建 */
    }
    const lines = acText.value
      .split('\n')
      .map((l) => l.trim())
      .filter(Boolean)
    const acceptance = lines.map((text, i) => ({
      id: `ac${i + 1}`,
      text,
      verifiable: true,
    }))
    const req = await createRequirement({
      title: title.value.trim(),
      body: body.value.trim(),
      project_path: props.task.projectPath.trim(),
      acceptance: acceptance.length ? acceptance : undefined,
      path: 's1',
      workflow_id: workflowId || undefined,
    })
    await promoteTaskToRequirement(req.id, props.task.id)
    // 停写打磨 Task（仍 running / waiting 时）；终态只挂靠。
    if (!isTerminalStatus(props.task.status)) {
      try {
        await cancelTask(props.task.id)
      } catch {
        /* 挂靠已成功；停写失败不挡进 PE */
      }
    }
    try {
      await taskStore.refreshTask(props.task.id)
    } catch {
      /* ignore */
    }
    // Intent 种子：写入 PlanDraft，避免 PE 空转 Intent（runner 未就绪时也落库）。
    try {
      const assertions = [
        {
          id: 'seed-intent',
          kind: 'decision',
          text: body.value.trim() || title.value.trim(),
          blocks_publish: false,
        },
        ...acceptance.map((a) => ({
          id: `seed-${a.id}`,
          kind: 'decision' as const,
          text: `AC: ${a.text}`,
          blocks_publish: false,
        })),
      ]
      await savePlanDraft(req.id, {
        stage: 'business',
        assertions,
      })
    } catch {
      /* 热启动失败不挡导航 */
    }
    emit('close')
    await router.push(`/requirements/${encodeURIComponent(req.id)}`)
  } catch (e) {
    notify.error(e instanceof Error ? e.message : '升格失败')
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <Modal :open="open" title="升格为需求" max-width="max-w-lg" @close="emit('close')">
    <p class="mb-3 text-[12px] text-muted">
      编辑种子后确认：建需求 · 挂靠本任务 · 停写打磨 · Intent 热启动 · 进入 PE。
    </p>
    <label class="mb-3 block space-y-1">
      <span class="text-xs text-muted">标题</span>
      <Input v-model="title" data-testid="promote-title" />
    </label>
    <label class="mb-3 block space-y-1">
      <span class="text-xs text-muted">说明（可改）</span>
      <Textarea v-model="body" :rows="4" data-testid="promote-body" />
    </label>
    <label class="mb-3 block space-y-1">
      <span class="text-xs text-muted">验收（可改 · 每行一条）</span>
      <Textarea v-model="acText" :rows="3" placeholder="默认关闭记住我&#10;勾选后 7 天内免登" data-testid="promote-ac" />
    </label>
    <p class="mb-4 text-[11px] text-muted">
      cwd：{{ task.projectPath || '—' }} · 任务 #{{ task.id.slice(0, 8) }}
    </p>
    <div class="flex flex-wrap items-center gap-2">
      <Button
        variant="primary"
        data-testid="promote-confirm"
        :disabled="!canConfirm"
        :title="blockReason || undefined"
        @click="confirm"
      >
        {{ submitting ? '升格中…' : '确认并启动 PE' }}
      </Button>
      <Button variant="ghost" :disabled="submitting" @click="emit('close')">取消</Button>
      <span v-if="blockReason" class="text-[11px] text-muted">{{ blockReason }}</span>
    </div>
  </Modal>
</template>
