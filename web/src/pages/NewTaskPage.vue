<script setup lang="ts">
// 新建任务页：复用 Session 详情页骨架（Header + 底部输入条），
// 与详情页唯一的差别是顶部需要先选项目（详情页的项目由任务决定）。
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import Button from '@/components/ui/Button.vue'
import Input from '@/components/ui/Input.vue'
import Select from '@/components/ui/Select.vue'
import PromptInput from '@/features/session/components/PromptInput.vue'
import { useTaskStore } from '@/stores/task'
import { useSessionStore } from '@/stores/session'
import { useNotificationStore } from '@/stores/notification'
import { groupKey } from '@/utils/format'

const router = useRouter()
const taskStore = useTaskStore()
const sessionStore = useSessionStore()
const notify = useNotificationStore()

const prompt = ref('')
const mode = ref<'select' | 'path'>('select')
const selectedPath = ref('')
const customPath = ref('')
const creating = ref(false)

const projects = computed(() => taskStore.recentProjects)

// 初始默认选中最近项目；无历史项目直接切自定义路径
onMounted(() => {
  const first = projects.value[0]
  if (first) selectedPath.value = first.projectPath
  else mode.value = 'path'
})

// 冷启动（任务列表后到）：最近项目就绪后自动选中第一个
watch(projects, (list) => {
  if (mode.value === 'select' && !selectedPath.value && list.length) {
    selectedPath.value = list[0].projectPath
  }
})

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
    const task = await taskStore.createTask(path, prompt.value.trim())
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
          <p class="px-1 text-xs text-muted">选择项目后，在下方描述要做什么，创建后进入会话。</p>
        </div>
      </div>

      <!-- 底部输入条（对应详情页 InterveneInput 位置）：prompt + 创建
           内层 max-w-3xl 与上方 header / 主区的正文左边界对齐（AC-R8-01） -->
      <div class="border-t border-border bg-surface/80 px-3 py-2.5 backdrop-blur md:px-4" data-testid="composer-bar">
        <div class="mx-auto flex w-full max-w-3xl items-end gap-2" data-testid="composer-inner">
          <div class="min-w-0 flex-1">
            <PromptInput
              v-model="prompt"
              :rows="3"
              aria-label="任务描述"
              placeholder="描述要做什么… 输入 / 触发命令/Skill，Ctrl+Enter 创建"
              @submit="submit"
            />
          </div>
          <!-- 视觉不变，命中区用伪元素外扩到 ≥44px 高（AC-R8-04） -->
          <Button
            variant="primary"
            :loading="creating"
            :disabled="!canSubmit"
            title="创建任务 (Ctrl+Enter)"
            class="relative after:absolute after:-inset-y-1.5 after:content-['']"
            @click="submit"
          >
            创建
          </Button>
        </div>
      </div>
    </div>
  </div>
</template>
