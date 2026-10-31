<script setup lang="ts">
// FilePreview：只读文件预览（markdown / pdf / code / text）。
// html 不走这里——真页面要新标签 URL（样式/脚本/相对资源）。
// Agent 产物不可信：markdown 经 marked + DOMPurify；code/text 仅纯文本展示，无编辑。
//
// `text` 可由调用方传入（ArtifactViewer 已经为"复制/搜索"取过原文）——
// 传了就不再重复请求；不传则自己拉（保持组件可独立使用）。
//
// 大 markdown（> MD_RICH_MAX）降级为纯文本 <pre>：marked + DOMPurify 同步扫
// 整份再进 DOM，手机上会明显卡顿；降级后仍可读，完整渲染请新标签打开。
import { computed, ref, watch } from 'vue'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { getTaskFileText, taskFileURL } from '@/services/api/feedback'
import { previewKind } from '@/utils/filePreview'
import Spinner from '@/components/ui/Spinner.vue'

/** 超过此字符数不再走 rich markdown（约 200KB 量级） */
const MD_RICH_MAX = 200_000

const props = defineProps<{ taskId: string; path: string; text?: string }>()

const kind = computed(() => previewKind(props.path))
const pdfURL = computed(() => taskFileURL(props.taskId, props.path))

const fetched = ref('')
const loading = ref(false)
const errorMsg = ref('')

/** 有外部文本就用外部的；没有才用自己拉的那份 */
const source = computed(() => (props.text !== undefined ? props.text : fetched.value))
const richMd = computed(() => kind.value === 'markdown' && source.value.length <= MD_RICH_MAX)
const mdHtml = computed(() => {
  if (!richMd.value || !source.value) return ''
  const raw = marked.parse(source.value, { async: false }) as string
  return DOMPurify.sanitize(raw)
})

async function loadText() {
  if (props.text !== undefined) return
  loading.value = true
  errorMsg.value = ''
  try {
    const body = await getTaskFileText(props.taskId, props.path)
    fetched.value = body.text
  } catch (err) {
    errorMsg.value = err instanceof Error ? err.message : '加载失败'
  } finally {
    loading.value = false
  }
}

watch(
  () => [props.taskId, props.path, props.text] as const,
  () => {
    fetched.value = ''
    errorMsg.value = ''
    if (props.text !== undefined) return
    if (kind.value === 'markdown' || kind.value === 'code' || kind.value === 'text') loadText()
  },
  { immediate: true },
)
</script>

<template>
  <div class="min-h-0">
    <template v-if="kind === 'markdown'">
      <div v-if="loading" class="flex items-center gap-2 px-3 py-2 text-muted">
        <Spinner class="h-3 w-3" /> 加载预览…
      </div>
      <div v-else-if="errorMsg" class="px-3 py-2 text-xs text-error">{{ errorMsg }}</div>
      <template v-else-if="richMd">
        <div class="md-body px-3 py-2 text-xs" v-html="mdHtml" />
      </template>
      <template v-else>
        <p class="px-3 pt-2 text-[11px] text-text-tertiary" data-testid="file-preview-md-plain">
          文档较大，已降级为纯文本预览；需要排版请用「在新标签打开原文」。
        </p>
        <pre
          class="max-h-[70vh] overflow-auto whitespace-pre-wrap break-all px-3 py-2 font-mono text-[11px] leading-relaxed text-text"
        >{{ source }}</pre>
      </template>
    </template>

    <template v-else-if="kind === 'code' || kind === 'text'">
      <div v-if="loading" class="flex items-center gap-2 px-3 py-2 text-muted">
        <Spinner class="h-3 w-3" /> 加载预览…
      </div>
      <div v-else-if="errorMsg" class="px-3 py-2 text-xs text-error">{{ errorMsg }}</div>
      <pre
        v-else
        class="max-h-[70vh] overflow-auto whitespace-pre-wrap break-all px-3 py-2 font-mono text-[11px] leading-relaxed text-text"
        data-testid="file-preview-plain"
      >{{ source }}</pre>
    </template>

    <iframe
      v-else-if="kind === 'pdf'"
      :src="pdfURL"
      class="h-[60vh] w-full border-0 bg-surface"
      title="PDF 预览"
    />

    <div v-else class="px-3 py-2 text-xs text-muted">此类型请用「打开」在新标签查看</div>
  </div>
</template>

<style scoped>
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
