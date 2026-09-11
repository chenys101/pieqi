<script setup lang="ts">
// FilePreview：文件预览（markdown / pdf）。
// pdf 直接 iframe 内嵌（浏览器原生渲染，同源 URL 带鉴权 cookie）；
// markdown 拉取原文后用 marked 渲染 + DOMPurify 消毒（Agent 产物不可信，防 XSS）。
import { computed, ref, watch } from 'vue'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { getTaskFileText, taskFileURL } from '@/services/api/feedback'
import { previewKind } from '../filePreview'
import Spinner from '@/components/ui/Spinner.vue'

const props = defineProps<{ taskId: string; path: string }>()

const kind = computed(() => previewKind(props.path))
const pdfURL = computed(() => taskFileURL(props.taskId, props.path))

const mdHtml = ref('')
const loading = ref(false)
const errorMsg = ref('')

async function loadMarkdown() {
  loading.value = true
  errorMsg.value = ''
  try {
    const text = await getTaskFileText(props.taskId, props.path)
    // marked 渲染 + DOMPurify 消毒（agent 生成的 markdown 属不可信输入）
    const raw = marked.parse(text, { async: false }) as string
    mdHtml.value = DOMPurify.sanitize(raw)
  } catch (err) {
    errorMsg.value = err instanceof Error ? err.message : '加载失败'
  } finally {
    loading.value = false
  }
}

watch(
  () => [props.taskId, props.path],
  () => {
    if (kind.value === 'markdown') loadMarkdown()
  },
  { immediate: true },
)
</script>

<template>
  <div class="border-t border-border/40">
    <!-- markdown：渲染为 HTML（样式见下方 scoped） -->
    <template v-if="kind === 'markdown'">
      <div v-if="loading" class="flex items-center gap-2 px-3 py-2 text-muted">
        <Spinner class="h-3 w-3" /> 加载预览…
      </div>
      <div v-else-if="errorMsg" class="px-3 py-2 text-xs text-error">{{ errorMsg }}</div>
      <div v-else class="md-body px-3 py-2 text-xs" v-html="mdHtml" />
    </template>

    <!-- pdf：浏览器原生内嵌 -->
    <iframe
      v-else-if="kind === 'pdf'"
      :src="pdfURL"
      class="h-[60vh] w-full border-0 bg-surface"
      title="PDF 预览"
    />
  </div>
</template>

<style scoped>
/* markdown 渲染体的最小可读样式（项目未引入 tailwind typography，此处手工收敛） */
.md-body :deep(h1),
.md-body :deep(h2),
.md-body :deep(h3) {
  font-weight: 600;
  margin: 0.5em 0 0.25em;
  line-height: 1.3;
}
.md-body :deep(h1) { font-size: 1.4em; }
.md-body :deep(h2) { font-size: 1.2em; }
.md-body :deep(h3) { font-size: 1.05em; }
.md-body :deep(p) { margin: 0.4em 0; }
.md-body :deep(ul),
.md-body :deep(ol) { margin: 0.4em 0; padding-left: 1.4em; }
.md-body :deep(li) { margin: 0.15em 0; }
.md-body :deep(ul) { list-style: disc; }
.md-body :deep(ol) { list-style: decimal; }
.md-body :deep(code) {
  font-family: ui-monospace, monospace;
  background: rgb(0 0 0 / 0.06);
  padding: 0.1em 0.3em;
  border-radius: 0.25em;
}
.md-body :deep(pre) {
  background: rgb(0 0 0 / 0.06);
  padding: 0.6em 0.75em;
  border-radius: 0.375em;
  overflow-x: auto;
  margin: 0.5em 0;
}
.md-body :deep(pre code) { background: none; padding: 0; }
.md-body :deep(blockquote) {
  border-left: 3px solid rgb(0 0 0 / 0.2);
  padding-left: 0.75em;
  margin: 0.4em 0;
  opacity: 0.8;
}
.md-body :deep(a) { text-decoration: underline; }
.md-body :deep(img) { max-width: 100%; }
.md-body :deep(table) { border-collapse: collapse; margin: 0.4em 0; }
.md-body :deep(th),
.md-body :deep(td) { border: 1px solid rgb(0 0 0 / 0.2); padding: 0.2em 0.5em; }
</style>
