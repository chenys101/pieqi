<script setup lang="ts">
// MarkdownBody：Agent 文本输出的 markdown 渲染体（需求 3）。
//
// 与 FilePreview 的分工：那边是**文件预览**（拉文本 → 渲染），这边是**事件流里的
// 对话正文**（markdown 源码已经在事件里）。共同点是消毒纪律（见 ../markdown.ts）。
//
// `looksLikeMarkdown` 为假时退回 `whitespace-pre-wrap` 纯文本 —— 一句话的回复
// 不该被包成段落块，也不该因为里面有个 `*` 就变成斜体。
import { computed } from 'vue'
import { looksLikeMarkdown, renderMarkdown } from '../markdown'

const props = defineProps<{ text: string }>()

const isMarkdown = computed(() => looksLikeMarkdown(props.text))
const html = computed(() => (isMarkdown.value ? renderMarkdown(props.text) : ''))
</script>

<template>
  <div
    v-if="isMarkdown"
    class="md-body break-words text-sm leading-relaxed"
    data-testid="markdown-body"
    v-html="html"
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
.md-body :deep(img) { max-width: 100%; border-radius: 0.375em; }
.md-body :deep(hr) { border: 0; border-top: 1px solid rgb(var(--color-text) / 0.2); margin: 0.7em 0; }
.md-body :deep(table) { border-collapse: collapse; margin: 0.5em 0; display: block; overflow-x: auto; }
.md-body :deep(th),
.md-body :deep(td) { border: 1px solid rgb(var(--color-text) / 0.2); padding: 0.25em 0.6em; }
.md-body :deep(th) { background: rgb(var(--color-text) / 0.06); font-weight: 600; }
.md-body :deep(input[type='checkbox']) { margin-right: 0.35em; }
</style>
