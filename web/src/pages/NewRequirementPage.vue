<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import ContentColumn from '@/components/layout/ContentColumn.vue'
import Button from '@/components/ui/Button.vue'
import Input from '@/components/ui/Input.vue'
import Textarea from '@/components/ui/Textarea.vue'
import { createRequirement } from '@/services/api/requirements'
import { getSettings } from '@/services/api/settings'
import { useNotificationStore } from '@/stores/notification'

const props = withDefaults(defineProps<{ initialProjectPath?: string }>(), { initialProjectPath: '' })
const router = useRouter()
const notify = useNotificationStore()

const title = ref('')
const body = ref('')
const projectPath = ref(props.initialProjectPath.trim())
const acText = ref('')
const submitting = ref(false)

onMounted(async () => {
  if (projectPath.value) return
  try {
    const s = await getSettings()
    if (!projectPath.value && s.default_project_path) {
      projectPath.value = s.default_project_path
    }
  } catch {
    /* 缺省空路径：提交时校验挡住 */
  }
})

const canSubmit = computed(() => title.value.trim() && projectPath.value.trim() && !submitting.value)

async function submit() {
  if (!canSubmit.value) return
  submitting.value = true
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
    })
    // 创建后进详情并自动拉起 Agent 拆分建议（?suggest=1）
    await router.replace({ path: `/requirements/${r.id}`, query: { suggest: '1' } })
  } catch (e) {
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
            class="rounded-md px-1.5 py-1 text-muted hover:bg-elevated hover:text-text"
            aria-label="返回"
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
          <div class="flex items-center gap-3 pt-2">
            <Button type="submit" :disabled="!canSubmit">{{ submitting ? '创建中…' : '创建' }}</Button>
            <RouterLink class="text-xs text-muted hover:text-text" to="/tasks/new">改走短路径：新建任务</RouterLink>
          </div>
        </form>
      </ContentColumn>
    </div>
  </div>
</template>
