<script setup lang="ts">
// MarkdownBody：Agent 文本输出的 markdown 渲染体（需求 3）。
//
// 与 FilePreview 的分工：那边是**文件预览**（拉文本 → 渲染），这边是**事件流里的
// 对话正文**（markdown 源码已经在事件里）。共同点是消毒纪律（见 ../markdown.ts）。
//
// `looksLikeMarkdown` 为假时退回 `whitespace-pre-wrap` 纯文本 —— 一句话的回复
// 不该被包成段落块，也不该因为里面有个 `*` 就变成斜体。
//
// 图片（2026-10-24 加）：正文里的 `![x](D:\...\a.png)` 必须在**送进 marked 之前**
// 改写成本地图片端点，理由见 markdownImages.ts 顶部（反斜杠会被当转义吃掉）。
// 渲染完再把"读不到"的图降级成一行说明，并让图片可点开全屏（与工具图共用遮罩）。
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { looksLikeMarkdown, renderMarkdown } from '../markdown'
import { degradeFailedLocalImages, rewriteLocalImageTargets } from '../markdownImages'
import { artifactPathFromHash, rewriteLocalArtifactLinks } from '../markdownArtifactLinks'
import { useImageViewer } from '@/composables/useImageViewer'
import { useArtifactsStore } from '@/stores/artifacts'

const props = defineProps<{ text: string; taskId?: string }>()

const { open } = useImageViewer()
const artifactsStore = useArtifactsStore()

const isMarkdown = computed(() => looksLikeMarkdown(props.text))
const html = computed(() => {
  if (!isMarkdown.value) return ''
  const taskId = props.taskId ?? ''
  let md = rewriteLocalImageTargets(props.text, taskId)
  if (taskId) md = rewriteLocalArtifactLinks(md, artifactsStore.list(taskId))
  return renderMarkdown(md)
})

const body = ref<HTMLElement | null>(null)

/**
 * 渲染后处理：给图片挂失败降级。
 *
 * 必须在 DOM 更新**之后**（v-html 的节点此时才存在），且每次正文变化都要重挂 ——
 * 流式输出时这个函数一秒内会被调用很多次，所以内部只做 querySelectorAll + 一次 addEventListener，
 * 不做任何会读布局的操作（读布局会强制同步回流，在流式场景里很贵）。
 */
async function afterRender() {
  if (!isMarkdown.value) return
  await nextTick()
  const el = body.value
  if (el) degradeFailedLocalImages(el)
}

watch(() => [props.text, props.taskId], afterRender)
onMounted(afterRender)

/**
 * 点图看大图。用**委托**而不是给每个 <img> 挂 click：
 * 节点是 v-html 造出来的，每次重渲染都是新节点，逐个挂既漏又积。
 */
function onClick(e: MouseEvent) {
  const target = e.target as HTMLElement | null
  // 产物本地链（md/code/text）：hash → 只读 viewer
  const anchor = target?.closest?.('a') as HTMLAnchorElement | null
  if (anchor) {
    const href = anchor.getAttribute('href') ?? ''
    const artPath = artifactPathFromHash(href)
    if (artPath && props.taskId) {
      e.preventDefault()
      artifactsStore.openPath(props.taskId, artPath)
      return
    }
    // html/image 已改写成 /preview 或 /file：新标签打开，避免离开 SPA
    if (href.startsWith('/preview/') || href.includes('/file?')) {
      e.preventDefault()
      window.open(href, '_blank', 'noopener')
      return
    }
  }
  const img = target?.closest?.('img') as HTMLImageElement | null
  if (!img) return
  e.preventDefault()
  open({
    // 取属性而不是 `img.src`：后者是**绝对化后**的 URL（jsdom 里还带 host 前缀），
    // 遮罩用相对地址就够（同源），也让断言/日志里看到的是真正写进 DOM 的那个值。
    src: img.getAttribute('src') ?? img.src,
    alt: img.getAttribute('alt') || '',
  })
}
</script>

<template>
  <div
    v-if="isMarkdown"
    ref="body"
    class="md-body break-words text-sm leading-relaxed"
    data-testid="markdown-body"
    v-html="html"
    @click="onClick"
  />
  <div v-else class="whitespace-pre-wrap break-words" data-testid="plain-body">{{ text }}</div>
</template>

<style scoped>
/* markdown 渲染体的最小可读样式（项目未引入 tailwind typography，手工收敛；
   scoped + :deep 是必须的 —— v-html 出来的节点拿不到 scoped 属性）。 */
.md-body :deep(h1),
.md-body :deep(h2),
.md-body :deep(h3),
.md-body :deep(h4) {
  font-weight: 600;
  margin: 0.6em 0 0.25em;
  line-height: 1.3;
}
.md-body :deep(h1) { font-size: 1.35em; }
.md-body :deep(h2) { font-size: 1.2em; }
.md-body :deep(h3) { font-size: 1.08em; }
.md-body :deep(h4) { font-size: 1em; }
/* 首个块不带上外边距：气泡已有 padding，再叠一次会让首行离顶边很远 */
.md-body :deep(> :first-child) { margin-top: 0; }
.md-body :deep(> :last-child) { margin-bottom: 0; }
.md-body :deep(p) { margin: 0.45em 0; }
.md-body :deep(ul),
.md-body :deep(ol) { margin: 0.45em 0; padding-left: 1.4em; }
.md-body :deep(ul) { list-style: disc; }
.md-body :deep(ol) { list-style: decimal; }
.md-body :deep(li) { margin: 0.15em 0; }
.md-body :deep(li > ul),
.md-body :deep(li > ol) { margin: 0.15em 0; }
/* 行内代码与代码块用同一族等宽字体、同一底色体系。
   灰底用 rgb(var(--color-text) / n)：浅色主题下接近黑、深色下接近白，
   一套规则同时适配两套主题（写死 rgb(0 0 0 / n) 会在深色下糊成一块）。 */
.md-body :deep(code) {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 0.9em;
  background: rgb(var(--color-text) / 0.08);
  padding: 0.1em 0.32em;
  border-radius: 0.25em;
}
.md-body :deep(pre) {
  background: rgb(var(--color-text) / 0.06);
  border: 1px solid rgb(var(--color-text) / 0.12);
  padding: 0.6em 0.75em;
  border-radius: 0.375em;
  overflow-x: auto;
  margin: 0.5em 0;
  line-height: 1.5;
}
.md-body :deep(pre code) { background: none; padding: 0; font-size: 0.85em; }
.md-body :deep(blockquote) {
  border-left: 3px solid rgb(var(--color-text) / 0.25);
  padding-left: 0.75em;
  margin: 0.45em 0;
  opacity: 0.85;
}
.md-body :deep(a) { color: rgb(var(--color-accent)); text-decoration: underline; }
/* 正文图片：只缩小不放大（位图放大只会糊），点开由全屏遮罩负责看清楚 */
.md-body :deep(img) {
  max-width: 100%;
  border-radius: 0.375em;
  cursor: zoom-in;
}
/* 本地图片读不到时的降级说明（markdownImages.degradeFailedLocalImages 造出来的节点）。
   用虚线框 + 小字：让"图没了"看起来是**一条可读的事实**，而不是一枚破图图标。 */
.md-body :deep(.md-image-missing) {
  display: block;
  margin: 0.35em 0;
  border: 1px dashed rgb(var(--color-text) / 0.25);
  border-radius: 0.375em;
  padding: 0.4em 0.6em;
  font-size: 0.85em;
  opacity: 0.75;
  word-break: break-all;
}
.md-body :deep(hr) { border: 0; border-top: 1px solid rgb(var(--color-text) / 0.2); margin: 0.7em 0; }
.md-body :deep(table) { border-collapse: collapse; margin: 0.5em 0; display: block; overflow-x: auto; }
.md-body :deep(th),
.md-body :deep(td) { border: 1px solid rgb(var(--color-text) / 0.2); padding: 0.25em 0.6em; }
.md-body :deep(th) { background: rgb(var(--color-text) / 0.06); font-weight: 600; }
.md-body :deep(input[type='checkbox']) { margin-right: 0.35em; }
</style>
