<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import ContentColumn from '@/components/layout/ContentColumn.vue'
import Button from '@/components/ui/Button.vue'
import Input from '@/components/ui/Input.vue'
import Textarea from '@/components/ui/Textarea.vue'
import Spinner from '@/components/ui/Spinner.vue'
import * as api from '@/services/api/requirements'
import { useNotificationStore } from '@/stores/notification'
import { useRequirementStore } from '@/stores/requirement'

const route = useRoute()
const router = useRouter()
const store = useRequirementStore()
const notify = useNotificationStore()

const id = computed(() => String(route.params.id || ''))

async function reload() {
  if (id.value) await store.loadDetail(id.value)
}

onMounted(() => void reload())
watch(id, () => void reload())

const nodePrompt = ref('')
const nodeTitle = ref('')
const busy = ref(false)

const issueTitle = ref('')
const issueSeverity = ref<'blocker' | 'major' | 'minor'>('major')

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

async function confirmPlan() {
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
    const isExpand = (store.tasks?.length ?? 0) > 0
    if (isExpand) await api.expandRequirement(id.value, nodes)
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

          <!-- AC 矩阵 -->
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
            <p v-else class="text-xs text-muted">零 AC（挂靠容器，不能宣称 testing 闭环）</p>
          </section>

          <!-- Tasks -->
          <section>
            <h2 class="mb-2 text-xs font-semibold text-text-secondary">任务图</h2>
            <ul v-if="store.tasks.length" class="space-y-1">
              <li v-for="t in store.tasks" :key="t.id">
                <RouterLink
                  class="flex items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-elevated/60"
                  :to="`/sessions/${t.id}`"
                >
                  <span class="min-w-0 flex-1 truncate">{{ t.title }}</span>
                  <span class="shrink-0 text-[11px] text-muted">{{ t.status }}</span>
                </RouterLink>
              </li>
            </ul>
            <p v-else class="text-xs text-muted">尚无 planned / 执行中任务 — 在下方确认拆分</p>
          </section>

          <!-- Confirm / Expand -->
          <section class="space-y-2 rounded-lg border border-border p-3">
            <h2 class="text-xs font-semibold text-text-secondary">
              {{ store.tasks.length ? 'Expand 追加' : '确认拆分（首版）' }}
            </h2>
            <Input v-model="nodeTitle" placeholder="节点标题（可选）" />
            <Textarea v-model="nodePrompt" :rows="3" placeholder="这一票要 agent 做什么" />
            <Button :disabled="busy || !nodePrompt.trim()" @click="confirmPlan">
              {{ store.tasks.length ? '确认 expand' : '确认并落地 planned' }}
            </Button>
          </section>

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
