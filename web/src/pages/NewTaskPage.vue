<script setup lang="ts">
// 新建任务页：复用 Session 详情页骨架（Header + 底部输入条），
// 与详情页唯一的差别是顶部需要先选项目 + 执行 Agent（详情页这两项都由任务决定）。
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import Button from '@/components/ui/Button.vue'
import Input from '@/components/ui/Input.vue'
import Select from '@/components/ui/Select.vue'
import PromptInput from '@/features/session/components/PromptInput.vue'
import Spinner from '@/components/ui/Spinner.vue'
import { useTaskStore } from '@/stores/task'
import { useSessionStore } from '@/stores/session'
import { useAgentStore } from '@/stores/agent'
import { useNotificationStore } from '@/stores/notification'
import { groupKey } from '@/utils/format'

const router = useRouter()
const taskStore = useTaskStore()
const sessionStore = useSessionStore()
const agentStore = useAgentStore()
const notify = useNotificationStore()

const prompt = ref('')
const mode = ref<'select' | 'path'>('select')
const selectedPath = ref('')
const customPath = ref('')
const creating = ref(false)

// 执行 Agent：取值是 agent 业务名（claude / qoder）。**默认 Claude Code**——
// 初始值来自服务端目录的 default（后端保证是 claude），目录拉不到时由 store 兜底。
const agent = ref('')

const projects = computed(() => taskStore.recentProjects)

/** 当前选中的 agent 目录项（展示 transport / 说明用） */
const selectedAgent = computed(() => agentStore.byId(agent.value))

onMounted(async () => {
  // 目录就绪后再落默认值：先渲染再选中的顺序能避免"选择器空着闪一下"
  await agentStore.loadCatalog()
  if (!agent.value) agent.value = agentStore.defaultAgent
})

// 项目默认值：有历史项目就选中第一个，确认「一条都没有」才切自定义路径。
// 判空必须等 taskStore.loaded —— 冷启动时（移动端首屏尤其明显）此刻 tasks 往往
// 还没到，在 onMounted 里判空会把"还没到"当成"没有"（PC 上因列表先到才没暴露）。
// 落到 path 模式后不再自动切回：用户可能正在往路径框里打字。
watch(
  [projects, () => taskStore.loaded],
  ([list, loaded]) => {
    if (mode.value !== 'select' || selectedPath.value) return
    if (list.length) selectedPath.value = list[0].projectPath
    else if (loaded) mode.value = 'path'
  },
  { immediate: true },
)

// 目录晚于页面就绪（或曾拉取失败后重试成功）：补上默认 agent
watch(
  () => agentStore.defaultAgent,
  (d) => {
    if (!agent.value) agent.value = d
  },
)

const canSubmit = computed(() => {
  if (creating.value || !prompt.value.trim()) return false
  return mode.value === 'path' ? !!customPath.value.trim() : !!selectedPath.value
})

/** 当前生效的项目路径 */
function effectivePath(): string | null {
  if (mode.value === 'path') return customPath.value.trim() || null
  return selectedPath.value || null
}

async function submit() {
  if (!canSubmit.value) return
  const path = effectivePath()
  if (!path) {
    notify.error('请选择或输入项目路径')
    mode.value = 'path'
    return
  }
  creating.value = true
  try {
    const task = await taskStore.createTask(path, prompt.value.trim(), agent.value || undefined)
    sessionStore.setThinking(task.id, true)
    router.push(`/sessions/${task.id}`)
  } catch (err) {
    notify.error(err instanceof Error ? err.message : '创建失败')
  } finally {
    creating.value = false
  }
}
</script>

<template>
  <div class="flex h-full">
    <div class="flex min-h-0 min-w-0 flex-1 flex-col">
      <!-- Header（与 SessionHeader 同骨架）：返回 + 标题 -->
      <header class="border-b border-border bg-surface/60 px-3 py-2.5 md:px-4">
        <div class="mx-auto flex max-w-3xl items-center gap-2">
          <button
            class="rounded-md px-1.5 py-1 text-muted transition-colors hover:bg-elevated hover:text-text"
            title="返回仪表盘"
            aria-label="返回"
            @click="router.push('/dashboard')"
          >
            <svg viewBox="0 0 24 24" class="h-4 w-4" aria-hidden="true">
              <path d="M15 18l-6-6 6-6" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round" />
            </svg>
          </button>
          <h1 class="min-w-0 flex-1 truncate text-sm font-semibold">新建任务</h1>
        </div>
      </header>

      <!-- 主区（对应详情页 Timeline 位置）：项目选择 -->
      <div class="min-h-0 flex-1 overflow-y-auto px-3 py-4 md:px-4">
        <div class="mx-auto flex max-w-3xl flex-col gap-2" data-testid="page-content">
          <div class="rounded-lg border border-border bg-surface p-3">
            <div class="mb-1.5 text-xs font-medium text-muted">项目</div>
            <div class="flex gap-2">
              <template v-if="mode === 'select'">
                <Select v-model="selectedPath" class="min-w-0 flex-1">
                  <option v-for="p in projects" :key="groupKey(p.projectPath)" :value="p.projectPath">
                    {{ p.projectId || p.projectPath }}
                  </option>
                  <option v-if="!projects.length" value="" disabled>暂无历史项目 — 请自定义路径</option>
                </Select>
                <Button size="sm" @click="mode = 'path'">自定义路径</Button>
              </template>
              <template v-else>
                <Input
                  v-model="customPath"
                  class="min-w-0 flex-1"
                  placeholder="输入绝对路径，如 G:\workspace\erp"
                />
                <Button v-if="projects.length" size="sm" @click="mode = 'select'">选项目</Button>
              </template>
            </div>
          </div>
          <div class="rounded-lg border border-border bg-surface p-3">
            <div class="mb-1.5 text-xs font-medium text-muted">执行 Agent</div>
            <Select v-model="agent" aria-label="执行 Agent" data-testid="agent-select">
              <option v-for="a in agentStore.catalog" :key="a.id" :value="a.id">{{ a.name }}</option>
            </Select>
            <!-- 选中项的说明随选择变化：换 agent 是"换执行者"，用户需要知道换了什么 -->
            <p class="mt-1.5 text-xs text-muted">
              {{ selectedAgent?.description || '选择由哪个 coding agent 执行本任务' }}
            </p>
          </div>
          <p class="px-1 text-xs text-muted">选择项目与 Agent 后，在下方描述要做什么，创建后进入会话。</p>
        </div>
      </div>

      <!-- 底部输入条（对应详情页 InterveneInput 位置）：prompt + 嵌在输入框右下角的创建按钮
           内层 max-w-3xl 与上方 header / 主区的正文左边界对齐（AC-R8-01）；
           PC 与移动端共用这一套 DOM，按钮位置两端一致 -->
      <div class="border-t border-border bg-surface/80 px-3 py-2.5 backdrop-blur md:px-4" data-testid="composer-bar">
        <div class="mx-auto w-full max-w-3xl" data-testid="composer-inner">
          <PromptInput
            v-model="prompt"
            :rows="3"
            aria-label="任务描述"
            placeholder="描述要做什么… 输入 / 触发命令/Skill，Ctrl+Enter 创建"
            @submit="submit"
          >
            <template #actions>
              <!-- 视觉 36×36，命中区用伪元素外扩到 ≥44px 高（AC-R8-04） -->
              <button
                class="pointer-events-auto relative shrink-0 rounded-[var(--radius-sm)] bg-accent p-2.5 text-white transition-opacity after:absolute after:-inset-1 after:content-[''] hover:opacity-90 focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-accent/40 disabled:cursor-not-allowed disabled:opacity-40"
                :disabled="!canSubmit"
                title="创建任务 (Ctrl+Enter)"
                aria-label="创建任务"
                data-testid="composer-submit"
                @click="submit"
              >
                <Spinner v-if="creating" class="h-4 w-4" />
                <svg v-else viewBox="0 0 24 24" class="h-4 w-4" aria-hidden="true">
                  <path d="M22 2 11 13M22 2l-7 20-4-9-9-4 20-7z" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round" />
                </svg>
              </button>
            </template>
          </PromptInput>
        </div>
      </div>
    </div>
  </div>
</template>
