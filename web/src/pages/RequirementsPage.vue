<script setup lang="ts">
import { onMounted } from 'vue'
import { useRouter } from 'vue-router'
import ContentColumn from '@/components/layout/ContentColumn.vue'
import Spinner from '@/components/ui/Spinner.vue'
import { useRequirementStore } from '@/stores/requirement'

const router = useRouter()
const store = useRequirementStore()

onMounted(() => {
  void store.loadList()
})

const statusLabel: Record<string, string> = {
  draft: '草案',
  active: '进行中',
  done: '已完成',
  cancelled: '已取消',
}
</script>

<template>
  <div class="flex h-full flex-col">
    <header class="border-b border-border bg-surface/60 px-3 py-2.5 md:px-4">
      <ContentColumn :resizable="false">
        <div class="flex items-center gap-2">
          <button
            class="rounded-md px-1.5 py-1 text-muted transition-colors hover:bg-elevated hover:text-text"
            title="返回"
            aria-label="返回"
            @click="router.push('/dashboard')"
          >
            <svg viewBox="0 0 24 24" class="h-4 w-4" aria-hidden="true">
              <path d="M15 18l-6-6 6-6" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round" />
            </svg>
          </button>
          <h1 class="min-w-0 flex-1 truncate text-sm font-semibold">需求</h1>
          <button
            class="rounded-lg bg-accent px-3 py-1.5 text-xs font-medium text-white hover:opacity-90"
            data-testid="req-new"
            @click="router.push('/requirements/new')"
          >
            ＋ 新建需求
          </button>
        </div>
      </ContentColumn>
    </header>

    <div class="min-h-0 flex-1 overflow-y-auto px-3 py-4 md:px-4">
      <ContentColumn :resizable="false">
        <p class="mb-4 text-xs text-muted">
          长路径：拆成多票、按依赖跑、Issue 回流。日常一票事请用
          <RouterLink class="text-accent underline-offset-2 hover:underline" to="/tasks/new">新建任务</RouterLink>。
        </p>
        <div v-if="store.loading" class="flex justify-center py-12"><Spinner /></div>
        <p v-else-if="store.error" class="text-sm text-error">{{ store.error }}</p>
        <p v-else-if="!store.items.length" class="py-12 text-center text-sm text-muted">暂无需求</p>
        <ul v-else class="space-y-2">
          <li v-for="r in store.items" :key="r.id">
            <button
              class="w-full rounded-lg border border-border bg-surface px-3 py-3 text-left transition-colors hover:bg-elevated/60"
              @click="router.push(`/requirements/${r.id}`)"
            >
              <div class="flex items-center gap-2">
                <span class="min-w-0 flex-1 truncate text-sm font-medium">{{ r.title || '（无标题）' }}</span>
                <span class="shrink-0 text-[11px] text-muted">{{ statusLabel[r.status] || r.status }}</span>
              </div>
              <div class="mt-1 truncate text-[11px] text-muted" :title="r.project_path">{{ r.project_path }}</div>
            </button>
          </li>
        </ul>
      </ContentColumn>
    </div>
  </div>
</template>
