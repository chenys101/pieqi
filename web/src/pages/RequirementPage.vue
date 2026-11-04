<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import ContentColumn from '@/components/layout/ContentColumn.vue'
import Button from '@/components/ui/Button.vue'
import Input from '@/components/ui/Input.vue'
import Select from '@/components/ui/Select.vue'
import Textarea from '@/components/ui/Textarea.vue'
import Spinner from '@/components/ui/Spinner.vue'
import ModelPicker from '@/features/session/components/ModelPicker.vue'
import SplitProposalEditor from '@/features/requirement/SplitProposalEditor.vue'
import { newRequirementTarget } from '@/features/requirement/newRequirementTarget'
import * as api from '@/services/api/requirements'
import { useAgentStore } from '@/stores/agent'
import { useNotificationStore } from '@/stores/notification'
import { useRequirementStore } from '@/stores/requirement'
import type { ProposedTaskNode } from '@/types/requirement'

const route = useRoute()
const router = useRouter()
const store = useRequirementStore()
const agentStore = useAgentStore()
const notify = useNotificationStore()

const id = computed(() => String(route.params.id || ''))

const proposal = ref<{ nodes: ProposedTaskNode[] } | null>(null)
const suggestTaskId = ref('')
const suggesting = ref(false)
let pollTimer: ReturnType<typeof setInterval> | null = null

const busy = ref(false)
const nodePrompt = ref('')
const nodeTitle = ref('')
const issueTitle = ref('')
const issueSeverity = ref<'blocker' | 'major' | 'minor'>('major')

/** 拆分会话用的 agent / model（与新建任务同口径；空 model = agent 默认） */
const suggestAgent = ref('')
const suggestModel = ref('')
const catalog = computed(() => agentStore.catalog)
const suggestModels = computed(() => agentStore.modelsOf(suggestAgent.value))
const suggestModelsLoaded = computed(() => !!agentStore.modelsLoaded[suggestAgent.value])

watch(
  suggestAgent,
  (a) => {
    suggestModel.value = ''
    void agentStore.loadModels(a)
  },
  { immediate: true },
)
watch(
  () => agentStore.defaultAgent,
  (d) => {
    if (!suggestAgent.value && d) suggestAgent.value = d
  },
  { immediate: true },
)

const isSplitSuggestTitle = (title: string) => title.startsWith('[pieqi-split]')
/** 全部非拆分会话任务（含已废弃 cancelled） */
const workTasks = computed(() => store.tasks.filter((t) => !isSplitSuggestTitle(t.title)))
/** 当前有效任务图（覆盖确认后旧票为 cancelled，不进主列表） */
const activeWorkTasks = computed(() => workTasks.value.filter((t) => t.status !== 'cancelled'))
const abandonedWorkTasks = computed(() => workTasks.value.filter((t) => t.status === 'cancelled'))
const willReplaceOnConfirm = computed(() => activeWorkTasks.value.length > 0)
/** 已有票离开 planned：覆盖拆分需 force（与后端 replace_needs_force 对齐） */
const hasProgressedWork = computed(() =>
  activeWorkTasks.value.some((t) => t.status !== 'planned'),
)
const suggestTasks = computed(() => store.tasks.filter((t) => isSplitSuggestTitle(t.title)))
const suggestRunning = computed(() =>
  suggestTasks.value.some((t) => t.status === 'pending' || t.status === 'running' || t.status === 'waiting_input'),
)
const bodyExpanded = ref(false)
const bodyText = computed(() => (store.current?.body || '').trim())
const bodyPreview = computed(() => {
  const t = bodyText.value
  if (!t) return ''
  if (bodyExpanded.value || t.length <= 280) return t
  return t.slice(0, 280) + '…'
})

/** header「新建需求」：带上当前需求的 project_path（ADR-0021）。 */
const newReqTarget = computed(() => newRequirementTarget(store.current?.project_path))

const statusLabel: Record<string, string> = {
  draft: '草案',
  active: '进行中',
  done: '已完成',
  cancelled: '已取消',
}
const acColor: Record<string, string> = {
  green: 'text-success',
  red: 'text-error',
  pending: 'text-muted',
  deferred: 'text-muted',
}

async function reload() {
  if (!id.value) return
  await store.loadDetail(id.value)
  proposal.value = await api.getProposal(id.value)
}

function stopPoll() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

function startPoll() {
  stopPoll()
  pollTimer = setInterval(async () => {
    await reload()
    if (proposal.value?.nodes?.length || !suggestRunning.value) {
      suggesting.value = false
      if (proposal.value?.nodes?.length) stopPoll()
      if (!suggestRunning.value && !proposal.value) stopPoll()
    }
  }, 2500)
}

async function runSuggest() {
  if (willReplaceOnConfirm.value) {
    const ok = window.confirm(
      `重新拆分将覆盖当前 ${activeWorkTasks.value.length} 个任务；确认提案后旧任务会废弃。是否继续？`,
    )
    if (!ok) return
  }
  busy.value = true
  suggesting.value = true
  try {
    const res = await api.suggestSplit(id.value, {
      agent: suggestAgent.value || undefined,
      model: suggestModel.value || undefined,
    })
    suggestTaskId.value = res.task?.id || ''
    notify.success(
      willReplaceOnConfirm.value
        ? 'Agent 正在重新拆分；确认后将覆盖并废弃现有任务'
        : 'Agent 正在拆分需求…',
    )
    await reload()
    startPoll()
  } catch (e) {
    suggesting.value = false
    notify.error(e instanceof Error ? e.message : '启动拆分失败')
  } finally {
    busy.value = false
  }
}

async function confirmProposal(nodes?: ProposedTaskNode[]) {
  const payload = nodes?.length ? nodes : proposal.value?.nodes
  if (!payload?.length) return
  if (willReplaceOnConfirm.value) {
    const ok = window.confirm(
      `确认后将废弃现有 ${activeWorkTasks.value.length} 个任务，并以本提案整表替换。是否继续？`,
    )
    if (!ok) return
  }
  busy.value = true
  try {
    // Agent 拆分确认：有旧票则 replace 覆盖；手工追加仍走下方「手工补一票」expand。
    const replace = willReplaceOnConfirm.value
    let force = false
    if (replace && hasProgressedWork.value) {
      force = window.confirm(
        '已有任务开跑或完成。覆盖拆分将废弃这些票（含未合入需求分支的施工）。确定强制覆盖？',
      )
      if (!force) {
        busy.value = false
        return
      }
    }
    const result = await api.confirmRequirement(id.value, payload, { replace, force })
    proposal.value = null
    await reload()
    if (result.abandoned && result.abandoned > 0) {
      notify.success(`已覆盖落地 ${result.tasks?.length ?? 0} 票，废弃旧任务 ${result.abandoned} 个`)
    } else {
      notify.success('已生成 planned Task')
    }
  } catch (e) {
    notify.error(e instanceof Error ? e.message : '确认失败')
  } finally {
    busy.value = false
  }
}

async function discardProposal() {
  busy.value = true
  try {
    await api.discardProposal(id.value)
    proposal.value = null
  } catch (e) {
    notify.error(e instanceof Error ? e.message : '丢弃失败')
  } finally {
    busy.value = false
  }
}

async function confirmPlanManual() {
  const prompt = nodePrompt.value.trim()
  if (!prompt) return
  busy.value = true
  try {
    const nodes = [
      {
        temp_id: 'n1',
        title: nodeTitle.value.trim() || undefined,
        prompt,
        covers_ac: (store.current?.acceptance ?? []).filter((a) => !a.deferred).map((a) => a.id),
      },
    ]
    if (activeWorkTasks.value.length > 0) await api.expandRequirement(id.value, nodes)
    else await api.confirmRequirement(id.value, nodes)
    nodePrompt.value = ''
    nodeTitle.value = ''
    await reload()
  } catch (e) {
    notify.error(e instanceof Error ? e.message : '确认失败')
  } finally {
    busy.value = false
  }
}

async function markDone() {
  busy.value = true
  try {
    await api.markRequirementDone(id.value)
    await reload()
  } catch (e) {
    notify.error(e instanceof Error ? e.message : '无法收口')
  } finally {
    busy.value = false
  }
}

async function openIssue() {
  if (!issueTitle.value.trim()) return
  busy.value = true
  try {
    await api.openIssue(id.value, {
      title: issueTitle.value.trim(),
      severity: issueSeverity.value,
      covers_ac: store.current?.acceptance?.[0] ? [store.current.acceptance[0].id] : undefined,
    })
    issueTitle.value = ''
    await reload()
  } catch (e) {
    notify.error(e instanceof Error ? e.message : '开立失败')
  } finally {
    busy.value = false
  }
}

async function confirmFix(issueId: string) {
  busy.value = true
  try {
    await api.confirmIssueFix(issueId)
    await reload()
  } catch (e) {
    notify.error(e instanceof Error ? e.message : '派修失败')
  } finally {
    busy.value = false
  }
}

async function resolve(issueId: string) {
  busy.value = true
  try {
    await api.resolveIssue(issueId)
    await reload()
  } catch (e) {
    notify.error(e instanceof Error ? e.message : '解决失败')
  } finally {
    busy.value = false
  }
}

async function verifyAC(acId: string, pass: boolean) {
  busy.value = true
  try {
    await api.humanVerifyAC(id.value, acId, pass)
    await reload()
  } catch (e) {
    notify.error(e instanceof Error ? e.message : '人验失败')
  } finally {
    busy.value = false
  }
}

onMounted(async () => {
  await Promise.all([reload(), agentStore.loadCatalog()])
  if (!suggestAgent.value && agentStore.defaultAgent) {
    suggestAgent.value = agentStore.defaultAgent
  }
  if (route.query.suggest === '1' && !proposal.value && !suggestRunning.value) {
    await runSuggest()
    router.replace({ path: route.path, query: {} })
  } else if (suggestRunning.value) {
    suggesting.value = true
    startPoll()
  }
})
watch(id, () => void reload())
onUnmounted(stopPoll)
</script>

<template>
  <div class="flex h-full flex-col">
    <header class="border-b border-border bg-surface/60 px-3 py-2.5 md:px-4">
      <ContentColumn :resizable="false">
        <div class="flex items-center gap-2">
          <button
            class="rounded-md px-1.5 py-1 text-muted hover:bg-elevated hover:text-text"
            aria-label="返回"
            @click="router.push('/requirements')"
          >
            <svg viewBox="0 0 24 24" class="h-4 w-4" aria-hidden="true">
              <path d="M15 18l-6-6 6-6" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round" />
            </svg>
          </button>
          <h1 class="min-w-0 flex-1 truncate text-sm font-semibold">
            {{ store.current?.title || '需求' }}
          </h1>
          <span v-if="store.current" class="shrink-0 text-[11px] text-muted">
            {{ statusLabel[store.current.status] || store.current.status }}
          </span>
          <RouterLink
            v-if="store.current"
            class="shrink-0 rounded-lg border border-border px-2.5 py-1 text-xs text-muted hover:bg-elevated/60 hover:text-text"
            data-testid="requirement-new-requirement"
            :to="newReqTarget"
          >
            ＋ 新建需求
          </RouterLink>
          <Button
            v-if="store.outcome?.can_mark_done && store.current?.status !== 'done'"
            size="sm"
            :disabled="busy"
            @click="markDone"
          >
            标记完成
          </Button>
        </div>
      </ContentColumn>
    </header>

    <div class="min-h-0 flex-1 overflow-y-auto px-3 py-4 md:px-4">
      <ContentColumn :resizable="false">
        <div v-if="store.loading && !store.current" class="flex justify-center py-12"><Spinner /></div>
        <p v-else-if="store.error" class="text-sm text-error">{{ store.error }}</p>
        <div v-else-if="store.current" class="mx-auto max-w-2xl space-y-6">
          <p class="truncate text-xs text-muted" :title="store.current.project_path">
            {{ store.current.project_path }}
          </p>
          <p
            v-if="store.current.integration_branch"
            class="font-mono text-xs text-muted"
            title="实现票 worktree 基线与合入目标"
          >
            需求分支 · {{ store.current.integration_branch }}
          </p>

          <!-- 原始需求正文 -->
          <section class="space-y-1.5">
            <h2 class="text-xs font-semibold text-text-secondary">原始需求</h2>
            <div
              v-if="bodyText"
              class="whitespace-pre-wrap rounded-lg border border-border bg-elevated/40 px-3 py-2.5 text-sm leading-relaxed text-text"
            >
              {{ bodyPreview }}
              <button
                v-if="bodyText.length > 280"
                type="button"
                class="mt-1 block text-[11px] text-accent"
                @click="bodyExpanded = !bodyExpanded"
              >
                {{ bodyExpanded ? '收起' : '展开全文' }}
              </button>
            </div>
            <p v-else class="text-xs text-muted">未填写正文（仅有标题）</p>
          </section>

          <!-- Agent 拆分（刚需主路径） -->
          <section class="space-y-3 rounded-lg border border-accent/30 bg-accent/5 p-3">
            <div class="flex flex-wrap items-center gap-2">
              <h2 class="text-xs font-semibold text-text-secondary">Agent 拆分建议</h2>
              <span class="text-[11px] text-muted">出 Task List → 你确认 → 一键生成</span>
            </div>
            <p
              v-if="willReplaceOnConfirm"
              class="rounded-md border border-warning/40 bg-warning/10 px-2.5 py-2 text-[11px] leading-relaxed text-warning"
              role="status"
            >
              已有 {{ activeWorkTasks.length }} 个任务。重新拆分并确认后将整表覆盖，旧任务标记为废弃（cancelled），不会追加。
            </p>
            <div class="flex flex-wrap items-end gap-2">
              <label class="min-w-[8rem] flex-1 space-y-1">
                <span class="block text-[11px] text-muted">Agent</span>
                <Select
                  v-model="suggestAgent"
                  size="sm"
                  aria-label="拆分 Agent"
                  :disabled="busy || suggesting || suggestRunning"
                >
                  <option v-for="a in catalog" :key="a.id" :value="a.id">{{ a.name }}</option>
                </Select>
              </label>
              <div class="flex min-w-[7rem] flex-col gap-1">
                <span class="text-[11px] text-muted">模型</span>
                <ModelPicker
                  v-if="suggestAgent"
                  :model="suggestModel"
                  :models="suggestModels"
                  :models-loaded="suggestModelsLoaded"
                  :disabled="busy || suggesting || suggestRunning"
                  data-testid="suggest-model-picker"
                  @update:model="suggestModel = $event"
                />
              </div>
              <Button
                class="ml-auto shrink-0"
                size="sm"
                variant="primary"
                :disabled="busy || suggesting || suggestRunning || !suggestAgent"
                @click="runSuggest"
              >
                {{ suggestRunning || suggesting ? '拆分中…' : proposal ? '重新生成建议' : '让 Agent 拆分' }}
              </Button>
            </div>
            <p v-if="suggestRunning || suggesting" class="flex items-center gap-2 text-xs text-muted">
              <Spinner class="h-3.5 w-3.5" />
              Agent 正在生成 Task List
              <RouterLink
                v-if="suggestTaskId || suggestTasks[0]"
                class="text-accent"
                :to="`/sessions/${suggestTaskId || suggestTasks[0].id}`"
              >
                查看会话
              </RouterLink>
            </p>
            <p
              v-if="proposal?.nodes?.length && willReplaceOnConfirm && !hasProgressedWork"
              class="rounded-md border border-warning/40 bg-warning/10 px-2.5 py-2 text-[11px] leading-relaxed text-warning"
              role="status"
            >
              确认落地将废弃现有任务列表，以本提案替换。请核对后再点确认。
            </p>
            <p
              v-else-if="proposal?.nodes?.length && hasProgressedWork"
              class="rounded-md border border-error/40 bg-error/10 px-2.5 py-2 text-[11px] leading-relaxed text-error"
              role="status"
            >
              已有任务开跑或完成。覆盖需二次确认（force）；未合入需求分支的施工会被废弃。
            </p>
            <SplitProposalEditor
              v-if="proposal?.nodes?.length"
              :nodes="proposal.nodes"
              :busy="busy"
              :confirm-label="willReplaceOnConfirm ? '覆盖并落地（废弃旧任务）' : '落地为 planned'"
              @confirm="confirmProposal"
              @discard="discardProposal"
            />
          </section>

          <section
            class="rounded-md border border-border bg-elevated/40 px-3 py-2.5 text-[11px] leading-relaxed text-muted"
            aria-label="交付约定"
          >
            <p class="font-medium text-text-secondary">交付约定</p>
            <ul class="mt-1 list-disc space-y-0.5 pl-4">
              <li>交付面是<strong class="font-medium text-text">需求分支</strong>：实现票 worktree 改完后合入该分支才算交付。</li>
              <li>实现票自测<strong class="font-medium text-text">仅单元/组件测试</strong>；功能测由 test 票在依赖合入后、隔离起服务执行。</li>
              <li>pieqi 功能测须另起实例（独立 PIEQI_HOME + 端口），勿占用现网面板。</li>
              <li>票面 completed ≠ 已进 main；test 失败会自动开 Issue（blocker）派修。</li>
            </ul>
          </section>

          <!-- AC -->
          <section>
            <h2 class="mb-2 text-xs font-semibold text-text-secondary">验收（AC）</h2>
            <ul v-if="store.outcome?.acceptance?.length" class="space-y-1.5">
              <li
                v-for="ac in store.outcome.acceptance"
                :key="ac.id"
                class="flex flex-wrap items-center gap-2 rounded-md border border-border px-2.5 py-2 text-sm"
              >
                <span class="font-mono text-[11px] text-muted">{{ ac.id }}</span>
                <span class="min-w-0 flex-1">{{ ac.text }}</span>
                <span class="text-[11px] font-medium" :class="acColor[ac.state]">{{ ac.state }}</span>
                <template v-if="ac.state !== 'deferred' && ac.state !== 'green'">
                  <button class="text-[11px] text-accent" :disabled="busy" @click="verifyAC(ac.id, true)">人验过</button>
                  <button class="text-[11px] text-error" :disabled="busy" @click="verifyAC(ac.id, false)">人验拒</button>
                </template>
              </li>
            </ul>
            <p v-else class="text-xs text-muted">零 AC（挂靠容器）</p>
          </section>

          <!-- Tasks -->
          <section>
            <h2 class="mb-2 text-xs font-semibold text-text-secondary">任务图</h2>
            <ul v-if="activeWorkTasks.length" class="space-y-1">
              <li v-for="t in activeWorkTasks" :key="t.id">
                <RouterLink
                  class="flex items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-elevated/60"
                  :to="`/sessions/${t.id}`"
                >
                  <span class="min-w-0 flex-1 truncate">{{ t.title }}</span>
                  <span
                    v-if="t.status === 'planned' && !t.agent"
                    class="shrink-0 rounded-full border border-dashed border-warning/40 px-1.5 py-0.5 text-[10.5px] text-warning"
                  >缺 agent</span>
                  <span
                    v-else-if="t.agent"
                    class="shrink-0 truncate text-[11px] text-muted"
                  >{{ t.agent }}{{ t.model ? ' / ' + t.model : '' }}</span>
                  <span class="shrink-0 text-[11px] text-muted">{{ t.status }}</span>
                </RouterLink>
              </li>
            </ul>
            <p v-else class="text-xs text-muted">尚无执行任务 — 确认上方拆分建议即可生成</p>
            <details v-if="abandonedWorkTasks.length" class="mt-2">
              <summary class="cursor-pointer text-[11px] text-muted">
                已废弃 {{ abandonedWorkTasks.length }} 个（覆盖拆分）
              </summary>
              <ul class="mt-1 space-y-0.5 opacity-70">
                <li v-for="t in abandonedWorkTasks" :key="t.id">
                  <RouterLink
                    class="flex items-center gap-2 rounded-md px-2 py-1 text-xs text-muted line-through hover:bg-elevated/40"
                    :to="`/sessions/${t.id}`"
                  >
                    <span class="min-w-0 flex-1 truncate">{{ t.title }}</span>
                    <span class="shrink-0">cancelled</span>
                  </RouterLink>
                </li>
              </ul>
            </details>
          </section>

          <!-- Manual fallback -->
          <details class="rounded-lg border border-border p-3">
            <summary class="cursor-pointer text-xs font-semibold text-muted">手工补一票（备用）</summary>
            <div class="mt-2 space-y-2">
              <Input v-model="nodeTitle" placeholder="节点标题（可选）" />
              <Textarea v-model="nodePrompt" :rows="3" placeholder="这一票要 agent 做什么" />
              <Button size="sm" :disabled="busy || !nodePrompt.trim()" @click="confirmPlanManual">
                {{ activeWorkTasks.length ? '确认 expand' : '确认并落地 planned' }}
              </Button>
            </div>
          </details>

          <!-- Issues -->
          <section>
            <h2 class="mb-2 text-xs font-semibold text-text-secondary">Issue</h2>
            <ul v-if="store.current.issues?.length" class="mb-3 space-y-2">
              <li
                v-for="iss in store.current.issues"
                :key="iss.id"
                class="rounded-md border border-border px-2.5 py-2 text-sm"
              >
                <div class="flex flex-wrap items-center gap-2">
                  <span class="font-medium">{{ iss.title }}</span>
                  <span class="text-[11px] text-muted">{{ iss.severity }} · {{ iss.status }}</span>
                  <button
                    v-if="iss.status === 'open'"
                    class="text-[11px] text-accent"
                    :disabled="busy"
                    @click="confirmFix(iss.id)"
                  >
                    确认派修
                  </button>
                  <button
                    v-if="iss.status !== 'resolved'"
                    class="text-[11px] text-muted"
                    :disabled="busy"
                    @click="resolve(iss.id)"
                  >
                    标为已解决
                  </button>
                </div>
              </li>
            </ul>
            <div class="flex flex-wrap gap-2">
              <Input v-model="issueTitle" class="min-w-[12rem] flex-1" placeholder="新 Issue 标题" />
              <select v-model="issueSeverity" class="rounded-md border border-border bg-surface px-2 text-xs">
                <option value="blocker">blocker</option>
                <option value="major">major</option>
                <option value="minor">minor</option>
              </select>
              <Button size="sm" :disabled="busy || !issueTitle.trim()" @click="openIssue">开立</Button>
            </div>
          </section>
        </div>
      </ContentColumn>
    </div>
  </div>
</template>
