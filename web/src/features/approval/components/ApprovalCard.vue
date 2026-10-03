<script setup lang="ts">
// 审批卡片（SPEC §5.4 / §6.2）：仪表盘待审批组 + Session 内联决策**共用这一个组件**。
// P1：可展开「前瞻性 Diff」——批准前看到将发生什么。
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import Button from '@/components/ui/Button.vue'
import { ApprovalDiffCard } from '@/features/feedback'
import ApprovalSummary from './ApprovalSummary.vue'
import { useApprovalStore } from '@/stores/approval'
import { useTaskStore } from '@/stores/task'
import { RISK_LABELS, riskOf, type ApprovalChoice, type ApprovalRequest, type RiskLevel } from '@/types/approval'
import { timeAgo } from '@/utils/date'

const props = defineProps<{ approval: ApprovalRequest }>()
const approvalStore = useApprovalStore()
const taskStore = useTaskStore()
const router = useRouter()

const loading = ref<ApprovalChoice | null>(null)
const task = computed(() => taskStore.byId(props.approval.taskId))
/** 前瞻性 Diff 展开/收起（不展开不请求） */
const showDiff = ref(false)

const risk = computed<RiskLevel>(() => riskOf(props.approval.risk))

/**
 * 三档视觉强度。
 *
 * ⚠️ **L0/L1 刻意不用警告色。** 这两档是**可以自动放行的** —— 它们出现在卡片上，
 * 只是因为用户把自动放行关掉了，属于"你想看一眼"，不是"这里有危险"。
 * 警告色如果铺满所有审批，就等于没有警告色：真正需要警觉的 L3 会淹没在一片黄里。
 * 强度差是**信号**，不是装饰 —— 铺满即失效。
 */
const TONE: Record<RiskLevel, { box: string; head: string; badge: string; title: string }> = {
  L0: {
    box: 'border-border bg-surface',
    head: 'text-text',
    badge: 'bg-elevated text-muted',
    title: '需要授权',
  },
  L1: {
    box: 'border-info/40 bg-surface',
    head: 'text-info',
    badge: 'bg-info/15 text-info',
    title: '需要授权',
  },
  L2: {
    box: 'border-warning/40 bg-surface',
    head: 'text-warning',
    badge: 'bg-warning/15 text-warning',
    title: '需要授权',
  },
  L3: {
    box: 'border-error/50 bg-surface',
    head: 'text-error',
    badge: 'bg-error/15 text-error',
    title: '需要确认',
  },
}
const tone = computed(() => TONE[risk.value])

/** L3 二次确认：只拦"允许"，不拦"拒绝"（拒绝是安全方向，再确认一次是添堵） */
const needsConfirm = computed(() => risk.value === 'L3')
/** 被 L3 确认条拦下来的那个允许动作；null = 没有在确认中 */
const pending = ref<'approve' | 'approve_session' | null>(null)

/** 「同类免审」由后端在 options 里声明可用（只有 ACP 路径带） */
const canApproveSession = computed(() => props.approval.options.includes('approve_session'))

async function act(kind: ApprovalChoice) {
  if (kind !== 'deny' && needsConfirm.value && pending.value === null) {
    pending.value = kind
    return
  }
  pending.value = null
  loading.value = kind
  try {
    const taskId = props.approval.taskId
    if (kind === 'approve') await approvalStore.approve(taskId)
    else if (kind === 'approve_session') await approvalStore.approveSession(taskId)
    else await approvalStore.deny(taskId)
  } finally {
    loading.value = null
  }
}
</script>

<template>
  <div class="rounded-lg border p-4" :class="tone.box">
    <div class="flex items-start justify-between gap-2">
      <div class="flex items-center gap-2">
        <span class="text-sm font-medium" :class="tone.head">{{ tone.title }}</span>
        <!-- 风险徽章：等级 + 它意味着什么。只写等级用户还得猜"L3 是多大" -->
        <span class="rounded px-1.5 py-0.5 text-[11px] font-medium" :class="tone.badge">
          {{ risk }} · {{ RISK_LABELS[risk] }}
        </span>
      </div>
      <span class="shrink-0 text-xs text-muted">{{ timeAgo(approval.createdAt) }}前</span>
    </div>
    <div class="mt-1.5 text-xs text-muted">
      {{ task?.title ?? '' }}
      <span v-if="task"> · {{ task.project }}</span>
    </div>
    <!-- 工具标签独立一行：它已经是一个短人读标签（后端 permLabel 保证），
         和摘要挤在同一个折叠块里会被算进"3 行"里，把真正要看的命令挤出折叠线 -->
    <div v-if="approval.tool" class="mt-2 text-xs font-medium text-muted">{{ approval.tool }}</div>
    <div class="mt-1">
      <ApprovalSummary :text="approval.summary" />
    </div>

    <!-- P1：展开前瞻性 Diff（决策前看将发生什么） -->
    <ApprovalDiffCard v-if="showDiff" :task-id="approval.taskId" :decision-id="approval.id" />

    <!-- L3 内联二次确认：不用 confirm()（SPEC 已禁 —— 模态会盖住上下文，
         而这恰恰需要用户看着"要删的是这个文件"再确认） -->
    <div
      v-if="pending"
      class="mt-3 rounded border border-error/40 bg-error/5 px-3 py-2"
    >
      <div class="text-xs font-medium text-error">
        此操作不可逆，确认{{ pending === 'approve_session' ? '允许并让同类操作在本会话免审' : '允许' }}？
      </div>
      <div class="mt-0.5 text-[11.5px] text-muted">
        <template v-if="pending === 'approve_session'">
          批准后 Agent 立即执行，且本任务剩余时间内同类操作不再问你 —— 后面几次发生什么你不会再看到。不确定就先看 Diff。
        </template>
        <template v-else>
          批准后 Agent 会立即执行，无法撤回。不确定就先看 Diff 或进会话看上下文。
        </template>
      </div>
      <div class="mt-2 flex gap-2">
        <Button variant="danger" size="sm" :loading="loading === pending" @click="act(pending)">确认允许</Button>
        <Button variant="ghost" size="sm" @click="pending = null">取消</Button>
      </div>
    </div>

    <div v-if="!pending" class="mt-3 flex flex-wrap gap-2">
      <Button
        :variant="needsConfirm ? 'danger' : 'primary'"
        size="sm"
        :loading="loading === 'approve'"
        :disabled="!!loading"
        @click="act('approve')"
      >
        允许一次
      </Button>
      <!-- 同类免审排在"允许一次"之后、拒绝之前：它是**更大的授权**，
           视觉上不能比单次批准更抢眼，但必须摸得到 -->
      <Button
        v-if="canApproveSession"
        variant="secondary"
        size="sm"
        :disabled="!!loading"
        title="本任务剩余时间内，同一类操作（ToolKind）不再弹卡；不落盘、不跨任务、不改 agent 权限配置"
        @click="act('approve_session')"
      >
        同类免审
      </Button>
      <Button variant="danger" size="sm" :loading="loading === 'deny'" :disabled="!!loading" @click="act('deny')">
        拒绝
      </Button>
      <Button variant="ghost" size="sm" :disabled="!!loading" @click="showDiff = !showDiff">
        {{ showDiff ? '收起 Diff' : '查看 Diff' }}
      </Button>
      <Button
        variant="ghost"
        size="sm"
        class="ml-auto"
        @click="router.push(`/sessions/${approval.taskId}`)"
      >
        查看会话
      </Button>
    </div>
  </div>
</template>
