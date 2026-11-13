<script setup lang="ts">
// 新建需求页：路径 S0/S1/S2 + 流程包一行选用（workflow-execution-efficiency §4/§11.3）。
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import ContentColumn from '@/components/layout/ContentColumn.vue'
import Button from '@/components/ui/Button.vue'
import Input from '@/components/ui/Input.vue'
import Textarea from '@/components/ui/Textarea.vue'
import { useResponsive } from '@/composables/useResponsive'
import { createRequirement } from '@/services/api/requirements'
import { getSettings } from '@/services/api/settings'
import { listWorkflows, type WorkflowSummary } from '@/services/api/workflows'
import { useNotificationStore } from '@/stores/notification'
import type { ExecutionPath } from '@/types/requirement'
import { ApiError } from '@/services/api/client'

const props = withDefaults(defineProps<{ initialProjectPath?: string }>(), { initialProjectPath: '' })
const router = useRouter()
const notify = useNotificationStore()
const { isMobile } = useResponsive()

const title = ref('')
const body = ref('')
const projectPath = ref(props.initialProjectPath.trim())
const acText = ref('')
const submitting = ref(false)

const path = ref<ExecutionPath>('s1')
const suggestedPath = ref<ExecutionPath>('s1')
const heuristicHint = ref('有定案必要但单仓 → 建议 S1')
const workflows = ref<WorkflowSummary[]>([])
const workflowId = ref('')
const showPackSheet = ref(false)
const dupExisting = ref<{ id: string; title: string } | null>(null)

const selectedPack = computed(() => workflows.value.find((w) => w.id === workflowId.value))

function recomputeHeuristic() {
  const acLines = acText.value.split('\n').map((l) => l.trim()).filter(Boolean)
  const bodyLen = body.value.trim().length
  const multi =
    /多模块|多仓|前后端|frontend|backend|微服务/i.test(body.value) ||
    /多模块|多仓/.test(title.value)
  let next: ExecutionPath = 's1'
  let hint = '有定案必要但单仓 → 建议 S1（可覆盖）'
  if (acLines.length >= 5 || multi) {
    next = 's2'
    hint = 'AC≥5 或显式多角色/多仓 → 建议 S2（成本更高，可覆盖）'
  } else if (acLines.length <= 3 && bodyLen < 200 && !multi) {
    next = 's0'
    hint = 'AC≤3 且正文短 → 可走 S0 短路径（可覆盖）'
  }
  suggestedPath.value = next
  heuristicHint.value = hint
  if (!pathTouched.value) path.value = next
}

const pathTouched = ref(false)
watch([acText, body, title], () => recomputeHeuristic())

onMounted(async () => {
  recomputeHeuristic()
  try {
    const [s, wf] = await Promise.all([getSettings(), listWorkflows()])
    if (!projectPath.value && s.default_project_path) {
      projectPath.value = s.default_project_path
    }
    workflows.value = wf.workflows
    workflowId.value = s.default_workflow_id || wf.default_id || wf.workflows.find((w) => w.is_default)?.id || 's1-plan-serial'
  } catch {
    /* 缺省空路径：提交时校验挡住 */
  }
})

const canSubmit = computed(() => title.value.trim() && projectPath.value.trim() && !submitting.value)

function pickPath(p: ExecutionPath) {
  pathTouched.value = true
  path.value = p
  showPackSheet.value = false
}

async function submit() {
  if (!canSubmit.value) return
  if (path.value === 's0') {
    // S0：短路径 Task，不进需求图
    const q = new URLSearchParams()
    if (title.value.trim()) q.set('title', title.value.trim())
    if (body.value.trim()) q.set('prompt', body.value.trim())
    if (projectPath.value.trim()) q.set('project_path', projectPath.value.trim())
    await router.push({ path: '/tasks/new', query: Object.fromEntries(q) })
    return
  }
  submitting.value = true
  dupExisting.value = null
  try {
    const lines = acText.value
      .split('\n')
      .map((l) => l.trim())
      .filter(Boolean)
    const acceptance = lines.map((text, i) => ({
      id: `ac${i + 1}`,
      text,
      verifiable: true,
    }))
    const r = await createRequirement({
      title: title.value.trim(),
      body: body.value.trim(),
      project_path: projectPath.value.trim(),
      acceptance: acceptance.length ? acceptance : undefined,
      path: path.value,
      workflow_id: workflowId.value || undefined,
    })
    await router.replace({ path: `/requirements/${r.id}`, query: { suggest: '1' } })
  } catch (e) {
    if (e instanceof ApiError && e.status === 409 && e.body?.code === 'duplicate_requirement' && e.body.existing_id) {
      dupExisting.value = {
        id: String(e.body.existing_id),
        title: String(e.body.existing_title || '同文案需求'),
      }
      return
    }
    notify.error(e instanceof Error ? e.message : '创建失败')
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="flex h-full flex-col">
    <header class="border-b border-border bg-surface/60 px-3 py-2.5 md:px-4">
      <ContentColumn :resizable="false">
        <div class="flex items-center gap-2">
          <button
            v-if="isMobile"
            class="rounded-md px-1.5 py-1 text-muted hover:bg-elevated hover:text-text"
            title="返回"
            aria-label="返回"
            data-testid="new-requirement-back"
            @click="router.push('/requirements')"
          >
            <svg viewBox="0 0 24 24" class="h-4 w-4" aria-hidden="true">
              <path d="M15 18l-6-6 6-6" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round" />
            </svg>
          </button>
          <h1 class="text-sm font-semibold">新建需求</h1>
        </div>
      </ContentColumn>
    </header>

    <div class="min-h-0 flex-1 overflow-y-auto px-3 py-4 md:px-4">
      <ContentColumn :resizable="false">
        <form class="mx-auto max-w-xl space-y-4" @submit.prevent="submit">
          <label class="block space-y-1">
            <span class="text-xs text-muted">标题</span>
            <Input v-model="title" placeholder="这条需求叫什么" />
          </label>
          <label class="block space-y-1">
            <span class="text-xs text-muted">项目路径（显式 cwd，禁止猜）</span>
            <Input v-model="projectPath" placeholder="D:\code\…" />
          </label>
          <label class="block space-y-1">
            <span class="text-xs text-muted">说明（可选）</span>
            <Textarea v-model="body" :rows="3" placeholder="背景 / 范围" />
          </label>
          <label class="block space-y-1">
            <span class="text-xs text-muted">验收标准（每行一条，可空）</span>
            <Textarea v-model="acText" :rows="4" placeholder="用户能登录&#10;列表分页正常" />
          </label>

          <div class="space-y-2">
            <span class="text-xs text-muted">执行路径</span>
            <button
              type="button"
              class="block w-full rounded-lg border px-3 py-2.5 text-left transition-colors"
              :class="path === 's0' ? 'border-accent bg-accent/5 shadow-[inset_3px_0_0_var(--color-accent,theme(colors.accent.DEFAULT))]' : 'border-border bg-surface hover:bg-elevated/60'"
              @click="pickPath('s0')"
            >
              <div class="flex items-center gap-2 text-sm font-medium">
                S0 短路径任务
                <span v-if="suggestedPath === 's0'" class="rounded bg-accent/15 px-1.5 py-0.5 text-[10px] font-semibold text-accent">建议</span>
              </div>
              <div class="mt-0.5 text-[11px] text-muted">不进需求图 · 无流程包</div>
            </button>
            <button
              type="button"
              class="block w-full rounded-lg border px-3 py-2.5 text-left transition-colors"
              :class="path === 's1' ? 'border-accent bg-accent/5 shadow-[inset_3px_0_0_var(--color-accent,theme(colors.accent.DEFAULT))]' : 'border-border bg-surface hover:bg-elevated/60'"
              @click="pickPath('s1')"
            >
              <div class="flex items-center gap-2 text-sm font-medium">
                S1 轻量需求
                <span v-if="suggestedPath === 's1'" class="rounded bg-accent/15 px-1.5 py-0.5 text-[10px] font-semibold text-accent">建议</span>
              </div>
              <div class="mt-0.5 text-[11px] text-muted">先定案再串行实现</div>
            </button>
            <button
              type="button"
              class="block w-full rounded-lg border px-3 py-2.5 text-left transition-colors"
              :class="path === 's2' ? 'border-accent bg-accent/5 shadow-[inset_3px_0_0_var(--color-accent,theme(colors.accent.DEFAULT))]' : 'border-border bg-surface hover:bg-elevated/60'"
              @click="pickPath('s2')"
            >
              <div class="flex items-center gap-2 text-sm font-medium">
                S2 完整协作平面
                <span v-if="suggestedPath === 's2'" class="rounded bg-accent/15 px-1.5 py-0.5 text-[10px] font-semibold text-accent">建议</span>
              </div>
              <div class="mt-0.5 text-[11px] text-muted">冷启动高 · 人审与合入税更高</div>
            </button>
            <p class="text-[11px] text-muted">{{ heuristicHint }}</p>
          </div>

          <div
            v-if="path !== 's0'"
            class="flex flex-wrap items-center gap-2 border-y border-border py-2.5"
          >
            <span class="text-xs text-muted">流程包</span>
            <strong class="text-sm">
              {{ selectedPack?.label || workflowId || '…' }}
              <span v-if="selectedPack" class="font-normal text-muted">@{{ selectedPack.version }}</span>
            </strong>
            <button type="button" class="rounded-md border border-border px-2 py-0.5 text-[11px] text-muted hover:bg-elevated" @click="showPackSheet = !showPackSheet">
              更换
            </button>
            <RouterLink class="text-[11px] text-accent underline-offset-2 hover:underline" to="/settings">管理流程包…</RouterLink>
          </div>
          <div v-if="showPackSheet && path !== 's0'" class="rounded-lg border border-border bg-surface p-2">
            <p class="mb-1 px-1 text-[11px] text-muted">选用已发布包</p>
            <button
              v-for="w in workflows"
              :key="w.id"
              type="button"
              class="mb-1 block w-full rounded-md px-2 py-2 text-left text-sm hover:bg-accent/10"
              :class="w.id === workflowId ? 'bg-accent/10' : ''"
              @click="workflowId = w.id; showPackSheet = false"
            >
              {{ w.label }} @{{ w.version }}
              <span v-if="w.is_default" class="ml-1 text-[10px] text-accent">默认</span>
            </button>
          </div>

          <div
            v-if="dupExisting"
            class="rounded-lg border border-error/40 bg-error/10 px-3 py-2 text-[12px] text-error"
            role="alert"
          >
            已有进行中的同文案需求「{{ dupExisting.title }}」
            <button
              type="button"
              class="ml-2 rounded border border-border px-2 py-0.5 text-[11px] text-text"
              @click="router.push(`/requirements/${dupExisting.id}`)"
            >
              打开续跑
            </button>
            <div class="mt-1 text-[11px] text-muted">禁止再建一条空转图。</div>
          </div>

          <div class="flex items-center gap-3 pt-2">
            <Button type="submit" :disabled="!canSubmit">
              {{ submitting ? '创建中…' : path === 's0' ? '改走新建任务' : '创建' }}
            </Button>
            <RouterLink class="text-xs text-muted hover:text-text" to="/tasks/new">改走短路径：新建任务</RouterLink>
          </div>
        </form>
      </ContentColumn>
    </div>
  </div>
</template>
