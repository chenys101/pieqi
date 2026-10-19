<script setup lang="ts">
// Agent 卡片（方案 §22）：名称 / 最近活跃 / Transport / 会话统计 / **能力矩阵**。
//
// ★ 2026-10-18 重写判据：此前这张卡把 `capabilities` 渲染成一排小圆点，
//   而三个 agent 的 capabilities **完全相同**（都是「流式输出 / 工具审批 / 续问 / 取消」），
//   于是三张卡长得一模一样 —— 用户看不出该选谁，卡片等于没给信息。
//
//   现在改成逐项对照：每一项都是**能吃差异的判据**（服务端算，前端不推断）。
//   仍保留 capabilities 那排圆点：它是"共同底座"，说明"这些不用比"。
//
// ⚠️ 另一个修正是**在线状态**：以前显示 `🟢 Online`，但后端明确不做进程探活
//   （见 AgentStats.online 的注释）。现在只展示"最近活跃于多久之前"，
//   查不到就说"无会话" —— 不编造一个探不出来的状态。
import { computed } from 'vue'
import Badge from '@/components/ui/Badge.vue'
import { timeAgo } from '@/utils/date'
import type { AgentInfo, AgentStats } from '@/types/agent'

const props = defineProps<{ info: AgentInfo; stats: AgentStats }>()

/** 能力矩阵的一行：三态标记 + 人读值 + 判据说明。 */
type Level = 'yes' | 'partial' | 'no'

interface Row {
  key: string
  label: string
  level: Level
  value: string
  /** 为什么是这个结论 —— 悬停可见。用户最容易被误导的几格必须能看理由。 */
  hint: string
}

/**
 * 三态而不是二态：claude 的 goal 是真实的部分可用（命令生效、语义与 dsh 不同），
 * 写成"支持/不支持"会丢信息。✓ 完整可用 / ◐ 部分可用 / ✗ 不可用。
 */
const LEVEL_STYLE: Record<Level, { icon: string; cls: string }> = {
  yes: { icon: '✓', cls: 'text-success' },
  partial: { icon: '◐', cls: 'text-warning' },
  no: { icon: '✗', cls: 'text-text-disabled' },
}

/**
 * goal 一行：**三家语义不同，不能统一写成"支持 goal"**。
 *
 * - `tool`：模型可见的工具（create_goal/update_goal），语义是「目标（objective）」
 * - `command`：斜杠命令 + Stop hook（`/goal <条件>`），语义是「完成条件（condition）」
 * - 缺省：该通道不开放（qoder：ACP 只开 4 个命令，且 goal 走 SDK control request）
 *
 * 详见 docs/agents-goal-and-capabilities.md。
 */
const goalRow = computed<Row>(() => {
  const mode = props.info.goalMode
  if (mode === 'tool') {
    return {
      key: 'goal',
      label: 'goal',
      level: 'yes',
      value: '目标 · 工具',
      hint: 'create_goal / update_goal 是模型可见的工具：即使通道没有命令平面，模型也会自己调。状态走工具调用事件，时间线可见。',
    }
  }
  if (mode === 'command') {
    return {
      key: 'goal',
      label: 'goal',
      level: props.info.goalStateVisible ? 'yes' : 'partial',
      value: props.info.goalStateVisible ? '完成条件 · 命令' : '完成条件 · 状态未透出',
      hint: '`/goal <条件>` 由 CLI 本地解析，再由 Stop hook 判定是否继续 —— 语义是「完成条件」，与 dsh 的「目标」不是一回事。',
    }
  }
  return {
    key: 'goal',
    label: 'goal',
    level: 'no',
    value: '不可用',
    hint: '该 agent 自身有 goal，但当前通道不开放：ACP 只支持 /init /memory /about /help 四个斜杠命令，且 goal 是 SDK control request。输入 /goal 不会生效。',
  }
})

const rows = computed<Row[]>(() => [
  {
    key: 'image',
    label: '图片入站',
    level: props.info.imagePrompt ? 'yes' : 'no',
    value: props.info.imagePrompt ? '支持' : '不支持',
    hint: props.info.imagePrompt
      ? '该传输能把图送出去；对端模型是否看得见取决于模型自身视觉能力（发不进去时后端会明确报错，不会静默丢图）。'
      : '当前传输没有图片通路（如 claude 配成 print 回退时，claude -p 是纯文本通道）。',
  },
  {
    key: 'models',
    label: '可选模型',
    level: props.info.modelSelectable ? 'yes' : 'no',
    value: props.info.modelSelectable ? '可切换' : '用 agent 默认',
    hint: props.info.modelSelectable
      ? '会下发可选模型清单（清单按需拉取：服务端取清单要起一次 agent 进程）。'
      : '不下发模型清单 —— 不选就是用 agent 自己的默认模型。',
  },
  goalRow.value,
  {
    key: 'sessionAlways',
    label: '同类免审',
    level: props.info.sessionAlways ? 'yes' : 'no',
    value: props.info.sessionAlways ? '支持' : '不支持',
    hint: props.info.sessionAlways
      ? '审批卡上会出现「同类免审」：本任务会话内同一 ToolKind 不再弹卡（pieqi 侧记账，不写 agent 配置、不落盘、随会话销毁）。'
      : '该路径的审批没有 approve_session 选项（claude 走 PreToolUse hook），只能逐次批准。',
  },
  {
    key: 'coldStart',
    label: '冷启动',
    level: props.info.coldStartCold ? 'partial' : 'yes',
    value: props.info.coldStartCold ? '较慢（>90s）' : '快',
    hint: props.info.coldStartCold
      ? '首次拉起进程实测超过 90 秒（因此 init_timeout 设为 2 分钟）。选它创建任务时要有等待预期。'
      : '进程启动在秒级。',
  },
])

const lastActive = computed(() => (props.stats.lastActiveAt ? timeAgo(props.stats.lastActiveAt) : ''))
</script>

<template>
  <div class="rounded-lg border border-border bg-surface p-4">
    <div class="flex items-start justify-between gap-2">
      <div class="min-w-0">
        <div class="flex items-center gap-2">
          <span class="text-sm font-semibold">{{ info.name }}</span>
          <!-- 不再显示 "Online"：后端不做进程探活，那是编出来的状态。
               改为"最近活跃"——它由任务派生，是真事实。 -->
          <Badge v-if="stats.activeSessions" tone="success">
            {{ stats.activeSessions }} 个活跃会话
          </Badge>
          <Badge v-else tone="neutral">{{ lastActive ? `最近活跃 ${lastActive}前` : '无会话' }}</Badge>
        </div>
        <p v-if="info.description" class="mt-1 text-[11.5px] leading-relaxed text-muted">
          {{ info.description }}
        </p>
      </div>
      <span class="shrink-0 font-mono text-xs text-muted">{{ info.transport }}</span>
    </div>

    <!-- 会话统计 -->
    <div class="mt-3 grid grid-cols-2 gap-2 text-xs">
      <div class="rounded border border-border/60 bg-background px-2.5 py-2">
        <div class="text-muted">活跃会话</div>
        <div class="mt-0.5 text-base font-semibold">{{ stats.activeSessions }}</div>
      </div>
      <div class="rounded border border-border/60 bg-background px-2.5 py-2">
        <div class="text-muted">总会话</div>
        <div class="mt-0.5 text-base font-semibold">{{ stats.totalSessions }}</div>
      </div>
    </div>

    <!-- ★ 能力矩阵：逐项对照，能看出差异 -->
    <dl class="mt-3 divide-y divide-border/50 rounded border border-border/60" data-testid="capability-matrix">
      <div v-for="row in rows" :key="row.key" class="flex items-center gap-2 px-2.5 py-[5px] text-xs">
        <dt class="w-[4.5rem] shrink-0 text-muted">{{ row.label }}</dt>
        <dd class="flex min-w-0 items-center gap-1.5" :title="row.hint">
          <span :class="LEVEL_STYLE[row.level].cls" class="w-3 shrink-0 text-center font-semibold">
            {{ LEVEL_STYLE[row.level].icon }}
          </span>
          <span class="truncate">{{ row.value }}</span>
        </dd>
      </div>
    </dl>

    <!-- 共同底座：三家都有的，列出来说明"这些不用比" -->
    <div class="mt-2 flex flex-wrap gap-1.5">
      <span
        v-for="cap in info.capabilities"
        :key="cap"
        class="rounded-full border border-border px-2 py-0.5 text-[11px] text-muted"
      >
        {{ cap }}
      </span>
    </div>
  </div>
</template>
