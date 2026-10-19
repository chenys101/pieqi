<script setup lang="ts">
// 工具调用卡片（方案 §19）。
//
// 两种形态，**同一套折叠逻辑**：
//   · 认得出语义的（goal / todo / plan / subagent / jobs）→ 语义卡：
//     左侧一个类别标记 + 人读标题 + 最该看的那一句
//   · 其余 → 通用 🔧 卡：原名 + 参数（默认折叠）
//
// ★ 为什么语义只影响"长什么样"，不影响别的：
//   审批判据是后端 riskLevelKinds（按 ACP ToolKind 分级），而 create_goal /
//   todo_write 等**刻意留在 other（L2 强制人工）**（见 internal/agent/toolkind_map.go）。
//   把这张表接到放行逻辑上，等于让"卡片画得好看"变成"权限更宽"。
//   本文件只 import 一个纯函数表，不碰 any store / api。
import { computed, ref } from 'vue'
import { formatToolInput } from '@/utils/format'
import { semanticOf, headlineOf, SEMANTIC_LABEL, type ToolSemantic } from '../toolSemantics'

const props = defineProps<{ toolName: string; input?: unknown }>()
const collapsed = ref(true)
const rows = computed(() => formatToolInput(props.input))

/** 认出的语义；undefined = 走通用卡（**认不出就不猜**） */
const semantic = computed<ToolSemantic | undefined>(() => semanticOf(props.toolName))
/** 语义卡顶部那句人读摘要（取不到为空串，退回显示工具名） */
const headline = computed(() => (semantic.value ? headlineOf(semantic.value, props.input) : ''))

/** 每类语义的图标 + 配色。只用 token（tailwind.config 里定义过），不写任意色值。 */
const MARK: Record<ToolSemantic, { icon: string; cls: string }> = {
  goal: { icon: '◎', cls: 'text-accent' },
  todo: { icon: '☑', cls: 'text-info' },
  plan: { icon: '▤', cls: 'text-warning' },
  subagent: { icon: '⧉', cls: 'text-text-secondary' },
  jobs: { icon: '⏱', cls: 'text-text-secondary' },
}
</script>

<template>
  <!-- ── 语义卡：认得出语义的工具 ─────────────────────────────────────── -->
  <div
    v-if="semantic"
    class="event-enter rounded-lg border border-border/60 bg-surface/60"
    :data-testid="`tool-semantic-${semantic}`"
  >
    <button
      class="flex w-full items-center gap-2 px-3 py-2 text-left text-xs hover:text-text"
      @click="collapsed = !collapsed"
    >
      <span :class="MARK[semantic].cls" aria-hidden="true">{{ MARK[semantic].icon }}</span>
      <span class="shrink-0 font-semibold" :class="MARK[semantic].cls">{{ SEMANTIC_LABEL[semantic] }}</span>
      <!-- 人读摘要：这是卡片的**主信息**（"目标改成了什么"比"调了 create_goal"重要） -->
      <span v-if="headline" class="min-w-0 flex-1 truncate text-text" :title="headline">{{ headline }}</span>
      <span v-else class="min-w-0 flex-1 truncate font-mono text-muted">{{ toolName }}</span>
      <span class="ml-auto shrink-0 text-muted transition-transform" :class="collapsed ? '-rotate-90' : ''">▾</span>
    </button>
    <div v-show="!collapsed" class="border-t border-border/40 px-3 py-2">
      <div v-for="(row, i) in rows" :key="i" class="flex gap-2 py-0.5 text-xs">
        <span class="shrink-0 font-mono text-muted">{{ row.key }}:</span>
        <span class="min-w-0 break-all font-mono">{{ row.value }}</span>
      </div>
      <div v-if="!rows.length" class="text-xs text-muted">（无参数）</div>
    </div>
  </div>

  <!-- ── 通用卡：认不出的工具（原名 + 参数，不做任何猜测）──────────────── -->
  <div v-else class="event-enter rounded-lg border border-border/60 bg-surface/60">
    <button
      class="flex w-full items-center gap-2 px-3 py-2 text-left text-xs hover:text-text"
      @click="collapsed = !collapsed"
    >
      <span>🔧</span>
      <!-- 工具名可截断：ACP 把整条命令当作 tool_name（可达数百字符），
           flex 项默认 min-width:auto 不允许缩到内容宽度以下，会把整行顶出视口 → 详情页横向滚动条 -->
      <span class="min-w-0 truncate font-mono font-semibold uppercase" :title="toolName">{{ toolName }}</span>
      <!-- 首行参数摘要（折叠时的预览） -->
      <span v-if="collapsed && rows.length" class="min-w-0 flex-1 truncate text-muted">
        {{ rows[0].value }}
      </span>
      <span class="ml-auto shrink-0 text-muted transition-transform" :class="collapsed ? '-rotate-90' : ''">▾</span>
    </button>
    <div v-show="!collapsed" class="border-t border-border/40 px-3 py-2">
      <div v-for="(row, i) in rows" :key="i" class="flex gap-2 py-0.5 text-xs">
        <span class="shrink-0 font-mono text-muted">{{ row.key }}:</span>
        <span class="min-w-0 break-all font-mono">{{ row.value }}</span>
      </div>
      <div v-if="!rows.length" class="text-xs text-muted">（无参数）</div>
    </div>
  </div>
</template>
