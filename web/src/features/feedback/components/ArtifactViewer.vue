<script setup lang="ts">
// ArtifactViewer：应用内只读文件查看层（A 方案）。
//
// ★ 为什么是 `position: fixed` + `Teleport`：它曾经作为 SessionPage 里
//   `flex h-full flex-col` 的**兄弟节点**渲染（Drawer 的 PC 分支是 `h-full` 的 aside），
//   结果把会话 grid 压成 0 高、两套内容同时绘制 —— 就是"点代码页面乱了"的根因。
//   查看层**不参与任何布局**，从机制上杜绝这类事故。
//
// 分工：
//   code/text → CodeView（行号 / 着色 / 搜索 / 换行 / 复制）
//   markdown  → FilePreview（marked + DOMPurify；文本由本组件统一取，避免重复请求）
//   pdf       → iframe（浏览器内嵌）
//   html/image 不进来 —— 它们是"真页面"，由调用方新标签打开（CSP sandbox 见后端）。
//
// Agent 产出不可信：所有文本走文本节点/转义渲染，markdown 经 DOMPurify，不用 v-html 展示代码。
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { getTaskFileText, taskFileURL } from '@/services/api/feedback'
import { useResponsive } from '@/composables/useResponsive'
import { baseNameOf } from '@/utils/fileGroups'
import { previewKind } from '@/utils/filePreview'
import { langLabelOf, langOfPath } from '@/utils/highlight'
import CodeView from './CodeView.vue'
import FilePreview from './FilePreview.vue'
import Spinner from '@/components/ui/Spinner.vue'

const props = defineProps<{
  open: boolean
  taskId: string
  path: string | null
  /** 产物 kind（html/image 不会进来）；缺省时按扩展名推导 */
  kind?: string
}>()

const emit = defineEmits<{ close: [] }>()

/** 大文件只先渲染前 N 行：行号 + 着色是"每行一个节点"，比一个 <pre> 贵得多 */
const MAX_LINES = 3000

const { isMobile } = useResponsive()

const text = ref('')
const loading = ref(false)
const errorMsg = ref('')
const wrap = ref(false)
const showAll = ref(false)
const findOpen = ref(false)
const query = ref('')
const hitIndex = ref(-1)
const copied = ref(false)
const copiedPath = ref(false)
const bodyEl = ref<HTMLElement | null>(null)
const findInput = ref<HTMLInputElement | null>(null)

/** 当前文件的展示形态 */
const mode = computed<'code' | 'markdown' | 'pdf' | 'other'>(() => {
  const k = props.kind || (props.path ? previewKind(props.path) : null)
  if (k === 'code' || k === 'text') return 'code'
  if (k === 'markdown') return 'markdown'
  if (k === 'pdf') return 'pdf'
  return 'other'
})

const name = computed(() => (props.path ? baseNameOf(props.path) : ''))
/** 模板里做非空窄化用：open && path 时一定非空 */
const currentPath = computed(() => props.path ?? '')
const lang = computed(() => (props.path ? langOfPath(props.path) : ''))
const langLabel = computed(() => (props.path ? langLabelOf(props.path) : ''))
const lines = computed(() => text.value.replace(/\r\n/g, '\n').split('\n'))
const renderedLines = computed(() => (showAll.value ? lines.value : lines.value.slice(0, MAX_LINES)))
const truncated = computed(() => mode.value === 'code' && lines.value.length > renderedLines.value.length)
const byteSize = computed(() => (text.value ? new TextEncoder().encode(text.value).length : 0))

/** 搜索：命中行号（0-based），跨全部行（即使只渲染了前 N 行） */
const hits = computed<number[]>(() => {
  const needle = query.value.trim().toLowerCase()
  if (!needle || mode.value !== 'code') return []
  const out: number[] = []
  lines.value.forEach((line, i) => {
    if (line.toLowerCase().includes(needle)) out.push(i)
  })
  return out
})
const currentLine = computed(() => hits.value[hitIndex.value] ?? -1)
const hitLabel = computed(() => (hits.value.length ? `${hitIndex.value + 1} / ${hits.value.length}` : '0 处'))

let reqSeq = 0
async function load() {
  const path = props.path
  if (!path) return
  const seq = ++reqSeq
  showAll.value = false
  wrap.value = isMobile.value
  findOpen.value = false
  query.value = ''
  hitIndex.value = -1
  copied.value = false
  copiedPath.value = false
  text.value = ''
  errorMsg.value = ''
  if (mode.value === 'pdf') return
  loading.value = true
  try {
    const body = await getTaskFileText(props.taskId, path)
    if (seq !== reqSeq) return
    text.value = body
  } catch (err) {
    if (seq !== reqSeq) return
    errorMsg.value = err instanceof Error ? err.message : '加载失败'
  } finally {
    if (seq === reqSeq) loading.value = false
  }
}

watch(
  () => [props.open, props.taskId, props.path] as const,
  ([open]) => {
    if (!open) {
      reqSeq += 1
      return
    }
    void load()
    nextTick(() => {
      if (bodyEl.value) bodyEl.value.scrollTop = 0
    })
  },
  { immediate: true },
)

/** 命中行可能在"只渲染前 N 行"之外：跳到它时自动放开全部 */
watch(currentLine, (line) => {
  if (line >= renderedLines.value.length) showAll.value = true
})

function openFind() {
  if (mode.value !== 'code') return
  findOpen.value = true
  void nextTick(() => {
    findInput.value?.focus()
    findInput.value?.select()
  })
}

function closeFind() {
  findOpen.value = false
  query.value = ''
  hitIndex.value = -1
}

function stepHit(delta: number) {
  if (!hits.value.length) return
  hitIndex.value = (hitIndex.value + delta + hits.value.length) % hits.value.length
}

watch(query, () => {
  hitIndex.value = hits.value.length ? 0 : -1
})

function close() {
  closeFind()
  emit('close')
}

async function copyText(value: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(value)
    return true
  } catch {
    const ta = document.createElement('textarea')
    ta.value = value
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    let ok = false
    try {
      ok = document.execCommand('copy')
    } catch {
      ok = false
    }
    ta.remove()
    return ok
  }
}

async function copyBody() {
  if (!text.value) return
  if (await copyText(text.value)) {
    copied.value = true
    setTimeout(() => (copied.value = false), 1500)
  }
}

async function copyPath() {
  if (!props.path) return
  if (await copyText(props.path)) {
    copiedPath.value = true
    setTimeout(() => (copiedPath.value = false), 1500)
  }
}

function openRaw() {
  if (!props.path) return
  window.open(taskFileURL(props.taskId, props.path), '_blank', 'noopener')
}

function onKeydown(e: KeyboardEvent) {
  if (!props.open) return
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'f') {
    if (mode.value !== 'code') return
    e.preventDefault()
    openFind()
    return
  }
  if (e.key === 'Escape') {
    if (findOpen.value) {
      closeFind()
      return
    }
    close()
  }
}

onMounted(() => document.addEventListener('keydown', onKeydown))
onBeforeUnmount(() => document.removeEventListener('keydown', onKeydown))

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}
</script>

<template>
  <Teleport to="body">
    <Transition name="av">
      <div
        v-if="open && path"
        class="fixed inset-0 z-[85] flex"
        role="dialog"
        aria-modal="true"
        aria-label="文件查看"
        data-testid="artifact-viewer"
      >
        <div class="absolute inset-0 bg-black/30" @click="close" />
        <section class="av-panel relative ml-auto flex h-full w-full flex-col border-l border-border bg-surface shadow-lg md:w-[min(920px,68vw)]">
          <!-- 头部：文件名 + 只读 + 工具条；路径单独一行，点击复制 -->
          <header class="shrink-0 border-b border-border">
            <div class="flex items-center gap-2 px-3 py-2">
              <button
                type="button"
                class="flex shrink-0 items-center gap-1 rounded px-1.5 py-1 text-[11.5px] text-text-tertiary transition-colors hover:bg-surface-muted hover:text-text"
                title="返回变更反馈（Esc）"
                data-testid="artifact-viewer-close"
                @click="close"
              >
                <svg viewBox="0 0 24 24" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <path d="M15 18l-6-6 6-6" />
                </svg>
                <span class="hidden sm:inline">变更反馈</span>
              </button>

              <div class="min-w-0 flex-1">
                <div class="flex items-center gap-1.5">
                  <span class="shrink-0 rounded bg-accent/10 px-1.5 py-0.5 font-mono text-[9.5px] font-semibold tracking-wide text-accent">
                    {{ langLabel }}
                  </span>
                  <h2 class="min-w-0 truncate text-[13px] font-semibold text-text" :title="name">{{ name }}</h2>
                  <span class="shrink-0 rounded border border-border px-1 py-0.5 text-[9.5px] text-text-tertiary">只读</span>
                </div>
                <button
                  type="button"
                  class="mt-0.5 block max-w-full truncate text-left font-mono text-[10.5px] text-text-tertiary hover:text-accent hover:underline"
                  :title="`点击复制路径：${path}`"
                  @click="copyPath"
                >
                  {{ path }}<span v-if="copiedPath" class="ml-1 text-success">已复制</span>
                </button>
              </div>

              <div class="flex shrink-0 items-center gap-0.5">
                <button
                  v-if="mode === 'code'"
                  type="button"
                  class="av-act"
                  title="文件内搜索（Ctrl+F）"
                  data-testid="artifact-viewer-search"
                  @click="openFind"
                >
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
                    <circle cx="11" cy="11" r="7" /><path d="M20 20l-3.6-3.6" />
                  </svg>
                  <span class="av-act-label">搜索</span>
                </button>
                <button
                  v-if="mode === 'code'"
                  type="button"
                  class="av-act"
                  :aria-pressed="wrap"
                  title="自动换行"
                  data-testid="artifact-viewer-wrap"
                  @click="wrap = !wrap"
                >
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                    <path d="M4 6h16M4 12h11a3 3 0 010 6h-4" /><path d="M8 15l-3 3 3 3" />
                  </svg>
                  <span class="av-act-label">换行</span>
                </button>
                <button
                  v-if="mode !== 'pdf'"
                  type="button"
                  class="av-act"
                  :disabled="loading || !text"
                  title="复制全文"
                  data-testid="artifact-viewer-copy"
                  @click="copyBody"
                >
                  <svg v-if="copied" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                    <path d="M5 13l4 4L19 7" />
                  </svg>
                  <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                    <rect x="9" y="9" width="11" height="11" rx="2" /><path d="M5 15V6a2 2 0 012-2h8" />
                  </svg>
                  <span class="av-act-label">{{ copied ? '已复制' : '复制' }}</span>
                </button>
                <button type="button" class="av-act" title="在新标签打开原文" @click="openRaw">
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                    <path d="M14 4h6v6" /><path d="M20 4l-9 9" /><path d="M19 14v5a1 1 0 01-1 1H5a1 1 0 01-1-1V6a1 1 0 011-1h5" />
                  </svg>
                </button>
                <button
                  type="button"
                  class="av-act av-close"
                  title="关闭（Esc）"
                  aria-label="关闭"
                  @click="close"
                >×</button>
              </div>
            </div>

            <div v-if="findOpen && mode === 'code'" class="flex items-center gap-1.5 px-3 pb-2" data-testid="artifact-viewer-find">
              <div class="av-find-box flex h-7 min-w-0 flex-1 items-center gap-1.5 rounded-md border border-accent/50 bg-surface px-2">
                <svg viewBox="0 0 24 24" class="h-3 w-3 shrink-0 text-text-tertiary" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
                  <circle cx="11" cy="11" r="7" /><path d="M20 20l-3.6-3.6" />
                </svg>
                <input
                  ref="findInput"
                  v-model="query"
                  type="text"
                  class="min-w-0 flex-1 bg-transparent text-[11.5px] text-text outline-none placeholder:text-text-disabled"
                  placeholder="在文件中查找…"
                  spellcheck="false"
                  data-testid="artifact-viewer-find-input"
                />
                <span class="shrink-0 font-mono text-[10.5px] text-text-tertiary" data-testid="artifact-viewer-find-count">{{ hitLabel }}</span>
              </div>
              <button type="button" class="av-mini" title="上一处" @click="stepHit(-1)">↑</button>
              <button type="button" class="av-mini" title="下一处" @click="stepHit(1)">↓</button>
              <button type="button" class="av-mini" title="关闭查找" @click="closeFind">×</button>
            </div>
          </header>

          <div ref="bodyEl" class="min-h-0 flex-1 overflow-auto" data-testid="artifact-viewer-body">
            <div v-if="loading" class="flex items-center justify-center gap-2 py-16 text-xs text-text-tertiary">
              <Spinner class="h-4 w-4" /> 加载文件…
            </div>
            <div v-else-if="errorMsg" class="mx-auto max-w-md px-4 py-16 text-center">
              <p class="text-xs text-error">{{ errorMsg }}</p>
              <button
                type="button"
                class="mt-2 rounded-md border border-border px-2.5 py-1 text-[11.5px] text-text-secondary hover:bg-surface-muted"
                @click="load"
              >重试</button>
            </div>
            <CodeView
              v-else-if="mode === 'code'"
              :lines="renderedLines"
              :lang="lang"
              :wrap="wrap"
              :query="query"
              :hits="hits"
              :current="currentLine"
            />
            <div v-else-if="mode === 'markdown'" class="mx-auto max-w-3xl py-2">
              <FilePreview :task-id="taskId" :path="currentPath" :text="text" />
            </div>
            <iframe
              v-else-if="mode === 'pdf'"
              :src="taskFileURL(taskId, currentPath)"
              class="h-full w-full border-0 bg-surface"
              title="PDF 预览"
            />
            <div v-else class="px-4 py-10 text-center text-xs text-text-tertiary">此类型请用「在新标签打开原文」查看</div>
          </div>

          <footer class="flex shrink-0 items-center gap-2 border-t border-border bg-surface-subtle px-3 py-1.5 text-[10.5px] text-text-tertiary">
            <span class="inline-flex items-center gap-1">
              <svg viewBox="0 0 24 24" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
                <rect x="5" y="11" width="14" height="9" rx="2" /><path d="M8 11V8a4 4 0 018 0v3" />
              </svg>
              只读 · 来自本任务 worktree
            </span>
            <span v-if="mode === 'code'" class="font-mono">
              {{ truncated ? `已渲染前 ${renderedLines.length} / ${lines.length} 行` : `${lines.length} 行` }}
              <span v-if="text" class="ml-1">· {{ formatBytes(byteSize) }}</span>
              <button
                v-if="truncated"
                type="button"
                class="ml-1 text-accent hover:underline"
                @click="showAll = true"
              >显示全部</button>
            </span>
            <span class="ml-auto hidden items-center gap-1 md:inline-flex">
              <kbd v-if="mode === 'code'" class="rounded border border-border px-1 py-0.5 font-mono text-[9.5px]">Ctrl</kbd>
              <span v-if="mode === 'code'" class="opacity-60">+</span>
              <kbd v-if="mode === 'code'" class="rounded border border-border px-1 py-0.5 font-mono text-[9.5px]">F</kbd>
              <span v-if="mode === 'code'" class="ml-1">搜索</span>
              <span class="mx-1 opacity-50">·</span>
              <kbd class="rounded border border-border px-1 py-0.5 font-mono text-[9.5px]">Esc</kbd>
              <span class="ml-1">关闭</span>
            </span>
          </footer>
        </section>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.av-enter-active,
.av-leave-active {
  transition: opacity 0.18s ease;
}
.av-enter-from,
.av-leave-to {
  opacity: 0;
}
.av-enter-active .av-panel,
.av-leave-active .av-panel {
  transition: transform 0.2s cubic-bezier(0.2, 0.8, 0.2, 1);
}
.av-enter-from .av-panel,
.av-leave-to .av-panel {
  transform: translateX(26px);
}
.av-act {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  height: 27px;
  padding: 0 7px;
  border-radius: 6px;
  color: rgb(var(--color-text-secondary));
  font-size: 11px;
  white-space: nowrap;
}
.av-act svg {
  width: 14px;
  height: 14px;
}
.av-act:hover:not(:disabled) {
  background: rgb(var(--color-surface-muted));
  color: rgb(var(--color-text));
}
.av-act[aria-pressed='true'] {
  background: rgb(var(--color-accent) / 0.1);
  color: rgb(var(--color-accent));
}
.av-act:disabled {
  opacity: 0.4;
  cursor: default;
}
.av-act-label {
  display: none;
}
@media (min-width: 640px) {
  .av-act-label {
    display: inline;
  }
}
.av-close {
  width: 27px;
  justify-content: center;
  padding: 0;
  font-size: 17px;
  color: rgb(var(--color-text-tertiary));
}
.av-mini {
  display: grid;
  place-items: center;
  width: 24px;
  height: 24px;
  border-radius: 5px;
  color: rgb(var(--color-text-tertiary));
}
.av-find-box {
  box-shadow: 0 0 0 3px rgb(var(--color-accent) / 0.08);
}
.av-mini:hover {
  background: rgb(var(--color-surface-muted));
  color: rgb(var(--color-text));
}
</style>
