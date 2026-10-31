<script setup lang="ts">
// CodeView：只读代码正文（行号 + 语法着色 + 命中高亮）。
//
// 与 ArtifactViewer 的分工：查看器管"取哪份文本、工具条、滚动容器"，
// 这里只管"把 N 行渲染成可读的代码"。着色 token 全部是文本节点，
// **不用 v-html** —— Agent 产出不可信，渲染层不碰 HTML 字符串。
//
// 虚拟列表（换行关闭时）：行高固定 21px，只挂载视口 ± overscan。
// 换行开启时行高不定，退回有上限的整段渲染（WRAP_MAX），避免错位滚动。
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { tokenizeLine, type CodeToken, type CodeTokenKind } from '@/utils/highlight'

const props = defineProps<{
  /** 全部行（可能是服务端截断后的全文） */
  lines: string[]
  lang: string
  wrap: boolean
  /** 搜索词（空 = 不标） */
  query: string
  /** 命中的行号（0-based） */
  hits: number[]
  /** 当前命中的行号（0-based；-1 = 无） */
  current: number
}>()

interface DisplayToken {
  text: string
  kind: CodeTokenKind
  hit: boolean
}

const LINE_H = 21
const OVERSCAN = 24
/** 换行模式下最多挂载的行数（行高不定，不能虚拟滚） */
const WRAP_MAX = 1500

const root = ref<HTMLElement | null>(null)
const start = ref(0)
const end = ref(80)

const useVirtual = computed(() => !props.wrap)
const renderLines = computed(() =>
  useVirtual.value ? props.lines : props.lines.slice(0, WRAP_MAX),
)
const wrapTruncated = computed(() => props.wrap && props.lines.length > WRAP_MAX)

const totalHeight = computed(() =>
  useVirtual.value ? props.lines.length * LINE_H : undefined,
)
const offsetY = computed(() => (useVirtual.value ? start.value * LINE_H : 0))

const windowLines = computed(() =>
  useVirtual.value ? props.lines.slice(start.value, end.value) : renderLines.value,
)
const windowOffset = computed(() => (useVirtual.value ? start.value : 0))

const tokenLines = computed<CodeToken[][]>(() => {
  const state = { inBlockComment: false }
  if (useVirtual.value) {
    for (let i = 0; i < start.value; i++) {
      tokenizeLine(props.lines[i] ?? '', props.lang, state)
    }
  }
  return windowLines.value.map((line) => tokenizeLine(line, props.lang, state))
})

const hitSet = computed(() => new Set(props.hits))

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

function scrollerOf(): HTMLElement | null {
  return root.value?.closest('[data-testid="artifact-viewer-body"]') as HTMLElement | null
}

function syncWindow() {
  if (!useVirtual.value) {
    start.value = 0
    end.value = renderLines.value.length
    return
  }
  const sc = scrollerOf()
  if (!sc) {
    start.value = 0
    end.value = Math.min(props.lines.length, 80)
    return
  }
  const top = sc.scrollTop
  const viewH = sc.clientHeight || 600
  const first = Math.max(0, Math.floor(top / LINE_H) - OVERSCAN)
  const last = Math.min(props.lines.length, Math.ceil((top + viewH) / LINE_H) + OVERSCAN)
  start.value = first
  end.value = Math.max(first + 1, last)
}

let scroller: HTMLElement | null = null
function onScroll() {
  syncWindow()
}

onMounted(() => {
  scroller = scrollerOf()
  scroller?.addEventListener('scroll', onScroll, { passive: true })
  syncWindow()
})

onBeforeUnmount(() => {
  scroller?.removeEventListener('scroll', onScroll)
  scroller = null
})

watch(
  () => [props.lines.length, props.wrap] as const,
  () => {
    void nextTick(syncWindow)
  },
)

watch(
  () => props.current,
  async (line) => {
    if (line < 0) return
    await nextTick()
    const sc = scrollerOf()
    if (!sc) return
    if (useVirtual.value) {
      const target = line * LINE_H - sc.clientHeight / 2 + LINE_H / 2
      sc.scrollTop = Math.max(0, target)
      syncWindow()
      return
    }
    root.value
      ?.querySelector<HTMLElement>(`.line[data-line="${line + 1}"]`)
      ?.scrollIntoView({ block: 'center', behavior: 'smooth' })
  },
)
</script>

<template>
  <div
    ref="root"
    class="code"
    :data-wrap="wrap ? 'on' : 'off'"
    data-testid="code-view"
    :style="useVirtual ? { height: `${totalHeight}px`, position: 'relative' } : undefined"
  >
    <div :style="useVirtual ? { transform: `translateY(${offsetY}px)` } : undefined">
      <div
        v-for="(tokens, i) in display"
        :key="windowOffset + i"
        class="line"
        :class="{ hit: hitSet.has(windowOffset + i), cur: windowOffset + i === current }"
        :data-line="windowOffset + i + 1"
      >
        <span class="ln" aria-hidden="true">{{ windowOffset + i + 1 }}</span>
        <span class="lc">
          <template v-for="(t, j) in tokens" :key="j">
            <mark v-if="t.hit" class="hit-mark">{{ t.text }}</mark>
            <span v-else :class="`t-${t.kind}`">{{ t.text }}</span>
          </template>
        </span>
      </div>
    </div>
    <div
      v-if="wrapTruncated"
      class="px-3 py-2 text-[11px] text-text-tertiary"
      data-testid="code-view-wrap-cap"
    >
      换行模式下仅渲染前 {{ WRAP_MAX }} 行；关闭换行可浏览全部已加载内容。
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
  box-sizing: content-box;
}
.line {
  display: flex;
  align-items: flex-start;
  min-width: 100%;
  height: 21px;
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
  height: auto;
  min-height: 21px;
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
