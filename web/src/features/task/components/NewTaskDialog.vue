<script setup lang="ts">
// 新建任务弹窗（方案 §16）：项目下拉（最近使用）+ 自定义路径 + prompt + 斜杠补全
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import Modal from '@/components/ui/Modal.vue'
import Button from '@/components/ui/Button.vue'
import PromptInput from '@/features/session/components/PromptInput.vue'
import { useTaskStore } from '@/stores/task'
import { useSessionStore } from '@/stores/session'
import { useNotificationStore } from '@/stores/notification'
import { groupKey } from '@/utils/format'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ close: [] }>()

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

// 打开时重置 + 默认选中最近项目
watch(
  () => props.open,
  (open) => {
    if (!open) return
    prompt.value = ''
    mode.value = 'select'
    customPath.value = ''
    selectedPath.value = projects.value[0]?.projectPath ?? ''
  },
)

const canSubmit = computed(() => {
  if (creating.value || !prompt.value.trim()) return false
  return mode.value === 'path' ? !!customPath.value.trim() : !!selectedPath.value
})

/** 当前生效的项目路径 */
function effectivePath(): string | null {
  if (mode.value === 'path') return customPath.value.trim() || null
  // 下拉无项目时（首次使用）自动切自定义路径
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
    emit('close')
    router.push(`/sessions/${task.id}`)
  } catch (err) {
    notify.error(err instanceof Error ? err.message : '创建失败')
  } finally {
    creating.value = false
  }
}
</script>

<template>
  <Modal :open="open" title="新建任务" @close="emit('close')">
    <div class="flex flex-col gap-3">
      <!-- 项目选择：下拉 ↔ 自定义路径 -->
      <div class="flex gap-2">
        <template v-if="mode === 'select'">
          <select
            v-model="selectedPath"
            class="min-w-0 flex-1 rounded-lg border border-border bg-background px-3 py-2 text-sm outline-none focus:border-accent/60"
          >
            <option v-for="p in projects" :key="groupKey(p.projectPath)" :value="p.projectPath">
              {{ p.projectId || p.projectPath }}
            </option>
            <option v-if="!projects.length" value="" disabled>暂无历史项目 — 请自定义路径</option>
          </select>
          <Button size="sm" @click="mode = 'path'">自定义路径</Button>
        </template>
        <template v-else>
          <input
            v-model="customPath"
            type="text"
            placeholder="输入绝对路径，如 G:\workspace\erp"
            class="min-w-0 flex-1 rounded-lg border border-border bg-background px-3 py-2 text-sm outline-none focus:border-accent/60"
          />
          <Button v-if="projects.length" size="sm" @click="mode = 'select'">选项目</Button>
        </template>
      </div>

      <PromptInput
        v-model="prompt"
        :rows="5"
        placeholder="描述要做什么… 输入 / 触发命令/Skill，Ctrl+Enter 创建"
        @submit="submit"
      />

      <div class="flex justify-end gap-2">
        <Button variant="ghost" @click="emit('close')">取消</Button>
        <Button variant="primary" :loading="creating" :disabled="!canSubmit" @click="submit">
          创建任务
        </Button>
      </div>
    </div>
  </Modal>
</template>
