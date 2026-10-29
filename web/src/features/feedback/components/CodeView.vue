<script setup lang="ts">
// CodeView：只读代码正文（行号 + 语法着色 + 命中高亮）。
//
// 与 ArtifactViewer 的分工：查看器管"取哪份文本、工具条、滚动容器"，
// 这里只管"把 N 行渲染成可读的代码"。着色 token 全部是文本节点，
// **不用 v-html** —— Agent 产出不可信，渲染层不碰 HTML 字符串。
import { computed, nextTick, ref, watch } from 'vue'
import { tokenizeLine, type CodeToken, type CodeTokenKind } from '@/utils/highlight'

const props = defineProps<{
  /** 已渲染的行（可能是大文件的前 N 行） */
  lines: string[]
  lang: string
  wrap: boolean
  /** 搜索词（空 = 不标） */
  query: string
  /** 命中的行号（0-based，可能包含未渲染的行） */
  hits: number[]
  /** 当前命中的行号（0-based；-1 = 无） */
  current: number
}>()

interface DisplayToken {
  text: string
  kind: CodeTokenKind
  hit: boolean
}

const root = ref<HTMLElement | null>(null)

/** 着色只跟"文件内容 + 语言"有关：搜索词变化时复用，不重新分词 */
const tokenLines = computed<CodeToken[][]>(() => {
  const state = { inBlockComment: false }
  return props.lines.map((line) => tokenizeLine(line, props.lang, state))
})

const hitSet = computed(() => new Set(props.hits))

/** 把命中片段从 token 里切出来：token 文本拼起来必须仍是原行 */
function markTokens(tokens: CodeToken[], needle: string): DisplayToken[] {
  if (!needle) return tokens.map((t) => ({ ...t, hit: false }))
  const lower = needle.toLowerCase()
  const out: DisplayToken[] = []
  for (const token of tokens) {
    const text = token.text
    let from = 0
    for (;;) {
      const at = text.toLowerCase().indexOf(lower, from)
      if (at === -1) {
        if (from < text.length) out.push({ text: text.slice(from), kind: token.kind, hit: false })
        break
      }
      if (at > from) out.push({ text: text.slice(from, at), kind: token.kind, hit: false })
      out.push({ text: text.slice(at, at + needle.length), kind: token.kind, hit: true })
      from = at + needle.length
    }
  }
  return out
}

const display = computed<DisplayToken[][]>(() => {
  const needle = props.query.trim()
  return tokenLines.value.map((tokens) => markTokens(tokens, needle))
})

/** 跳到当前命中行：滚动交给最近的滚动祖先（查看器正文），本组件不持有滚动条 */
watch(
  () => props.current,
  async (line) => {
    if (line < 0) return
    await nextTick()
    root.value
      ?.querySelector<HTMLElement>(`.line[data-line="${line + 1}"]`)
      ?.scrollIntoView({ block: 'center', behavior: 'smooth' })
  },
)
</script>

<template>
  <div ref="root" class="code" :data-wrap="wrap ? 'on' : 'off'" data-testid="code-view">
    <div
      v-for="(tokens, i) in display"
      :key="i"
      class="line"
      :class="{ hit: hitSet.has(i), cur: i === current }"
      :data-line="i + 1"
    >
      <span class="ln" aria-hidden="true">{{ i + 1 }}</span>
      <span class="lc">
        <template v-for="(t, j) in tokens" :key="j">
          <mark v-if="t.hit" class="hit-mark">{{ t.text }}</mark>
          <span v-else :class="`t-${t.kind}`">{{ t.text }}</span>
        </template>
      </span>
    </div>
  </div>
</template>

<style scoped>
.code {
  min-width: 100%;
  width: max-content;
  padding: 8px 0 42px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace;
  font-size: 12px;
  line-height: 21px;
  tab-size: 2;
}
.line {
  display: flex;
  align-items: flex-start;
  min-width: 100%;
  min-height: 21px;
}
.line:hover {
  background: rgb(var(--color-text) / 0.025);
}
.ln {
  position: sticky;
  left: 0;
  z-index: 1;
  flex: none;
  width: 52px;
  padding-right: 13px;
  text-align: right;
  color: rgb(var(--color-text-disabled));
  background: rgb(var(--color-surface));
  user-select: none;
  font-variant-numeric: tabular-nums;
}
.lc {
  flex: 1;
  padding-right: 28px;
  white-space: pre;
  color: rgb(var(--color-text));
}
.code[data-wrap="on"] {
  width: auto;
}
.code[data-wrap="on"] .line {
  min-width: 0;
}
.code[data-wrap="on"] .lc {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.code[data-wrap="on"] .ln {
  position: static;
}
.line.hit {
  background: rgb(var(--color-warning) / 0.09);
  box-shadow: inset 3px 0 0 rgb(var(--color-warning) / 0.55);
}
.line.hit .ln {
  background: rgb(var(--color-warning) / 0.09);
  color: rgb(var(--color-warning));
}
.line.cur {
  background: rgb(var(--color-warning) / 0.17);
  box-shadow: inset 3px 0 0 rgb(var(--color-warning));
}
.line.cur .ln {
  background: rgb(var(--color-warning) / 0.17);
  color: rgb(var(--color-warning));
}
.hit-mark {
  padding: 0;
  border-radius: 2px;
  color: inherit;
  background: rgb(var(--color-warning) / 0.34);
  box-shadow: 0 0 0 1px rgb(var(--color-warning) / 0.16);
}
.line.cur .hit-mark {
  background: rgb(var(--color-warning) / 0.62);
}
.t-kw {
  color: rgb(var(--color-code-keyword));
}
.t-str {
  color: rgb(var(--color-code-string));
}
.t-num {
  color: rgb(var(--color-code-number));
}
.t-cm {
  color: rgb(var(--color-code-comment));
  font-style: italic;
}
.t-tag {
  color: rgb(var(--color-code-tag));
}
.t-fn {
  color: rgb(var(--color-code-fn));
}
</style>
