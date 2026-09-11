<script setup lang="ts">
// Session 页（方案 §17 + SPEC §5.2）：V2 核心页面 —— Header / 审批横幅 / Timeline / 干预输入。
//
// **变更反馈按 SPEC §6.4 分三档摆放**，不是"一个抽屉走天下"：
//   宽屏 ≥1280   常驻右栏，宽度由 grid 驱动（展开 420px ⇄ 收起 46px 贴边条）
//   平板 768–1279 420px 侧栏，靠头部按钮开合
//   移动端 <768  整屏，顶部 SegmentedControl 切换 时间线 / 变更反馈
// 1280 是"反馈怎么摆"的拐点而不是 768 —— 理由见 useResponsive 的注释。
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useTaskStore } from '@/stores/task'
import { useSession } from '@/composables/useSession'
import { useResponsive } from '@/composables/useResponsive'
import { useFeedbackPanelStore } from '@/stores/feedbackPanel'
import { SessionHeader, SessionTimeline, ApprovalBanner, InterveneInput } from '@/features/session'
import { FeedbackPanel } from '@/features/feedback'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import Drawer from '@/components/ui/Drawer.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Button from '@/components/ui/Button.vue'

const route = useRoute()
const router = useRouter()
const taskStore = useTaskStore()
const { isMobile, isWide } = useResponsive()
const fbPanel = useFeedbackPanelStore()

const taskId = computed(() => route.params.id as string)
const { task, canCancel, canSendPrompt, submitPrompt, cancel, approve, deny, consumeForceScroll } = useSession(taskId)

/** 决策横幅：waiting_input 且带 decision 时展示 */
const decision = computed(() => (task.value?.status === 'waiting_input' ? task.value.decision : undefined))
const approvalBusy = ref(false)
/** 冷启动探测完成（详情已尝试拉取），仍无任务 → 视为不存在 */
const probed = ref(false)

/** Agent 执行中禁止回退（静止边界原则）；面板仍可查看 */
const canRewind = computed(() => !!task.value && task.value.status !== 'running' && task.value.status !== 'pending')

/** 平板档：反馈走 420px 侧栏（宽屏常驻、移动端整屏，都不用它） */
const feedbackOpen = ref(false)
/** 移动端整屏：时间线 / 变更反馈 二选一（SPEC §5.2.1：移动端没有"收起"这个概念） */
const MOBILE_PANES = [
  { value: 'timeline', label: '时间线' },
  { value: 'feedback', label: '变更反馈' },
]
const mobilePane = ref('timeline')

/**
 * 头部「反馈」按钮的落点按档位不同 —— 三档的入口本来就不是同一个东西。
 * 宽屏与移动端**不显示这个按钮**：那两档各有自己的入口（贴边条 / 顶部分段），
 * 再挂一个按钮就是同一件事给两个入口，而且位置还都不是它真正生效的地方。
 */
function openFeedback() {
  if (isWide.value) {
    fbPanel.expand()
    return
  }
  if (isMobile.value) {
    mobilePane.value = 'feedback'
    return
  }
  feedbackOpen.value = true
}

// 冷启动兜底：WS 快照未到时按 id 拉详情（含全量 events）
watch(
  taskId,
  (id) => {
    probed.value = false
    if (!id) return
    if (taskStore.byId(id)) {
      probed.value = true
      return
    }
    taskStore
      .refreshTask(id)
      .catch(() => {})
      .finally(() => (probed.value = true))
  },
  { immediate: true },
)

async function onApprove() {
  approvalBusy.value = true
  try {
    await approve()
  } finally {
    approvalBusy.value = false
  }
}

async function onDeny() {
  approvalBusy.value = true
  try {
    await deny()
  } finally {
    approvalBusy.value = false
  }
}

// 删除必需确认，但**不用原生 confirm()**（SPEC 已禁）：它是模态的，会盖住任务标题与上下文，
// 而用户恰恰要看着那些才知道删的是不是这个。改为紧接着头部长出的内联确认条。
const removeConfirming = ref(false)

function askRemove() {
  removeConfirming.value = true
}

async function doRemove() {
  if (!task.value) return
  removeConfirming.value = false
  await taskStore.deleteTask(task.value.id)
  // 删除后回仪表盘而不是 /tasks：任务浏览器页是移动端专用，桌面由侧栏树承接（SPEC §6.1）
  router.replace('/dashboard')
}
</script>

<template>
  <div v-if="task" class="flex h-full flex-col">
    <!-- 移动端：整屏切换的两个入口。放在最顶是因为下面那两屏各自占满高度，
         没有别的位置能同时够到它们。 -->
    <div v-if="isMobile" class="shrink-0 border-b border-border bg-surface px-3 py-2">
      <SegmentedControl v-model="mobilePane" :options="MOBILE_PANES" fill aria-label="会话视图" />
    </div>

    <!-- 宽屏用 grid（宽度由 grid-template-columns 驱动，展开/收起只切 class，
         动画交给浏览器排版 —— 不写内联宽度、不测量像素，SPEC §5.2.1）；
         其余档位是普通 flex 排布。 -->
    <div class="min-h-0 flex-1" :class="isWide ? ['se-grid', { 'is-fb-collapsed': fbPanel.collapsed }] : 'flex'">
      <div
        class="flex min-h-0 min-w-0 flex-1 flex-col"
        :class="{ hidden: isMobile && mobilePane === 'feedback' }"
      >
        <SessionHeader
          :task="task"
          :can-cancel="canCancel"
          :show-feedback="!isWide && !isMobile"
          @cancel="cancel"
          @remove="askRemove"
          @feedback="openFeedback"
        />

        <!-- 内联确认条：紧贴任务头部，用户看着任务名确认删的是不是它 -->
        <div
          v-if="removeConfirming"
          class="mx-auto w-full max-w-3xl shrink-0 px-3 pb-2 md:px-4"
        >
          <div class="flex flex-wrap items-center gap-2 rounded-lg border border-error/40 bg-error/5 px-3 py-2">
            <span class="min-w-0 flex-1 text-xs text-error">删除该任务？删除后不可恢复。</span>
            <Button variant="ghost" size="sm" @click="removeConfirming = false">取消</Button>
            <Button variant="danger" size="sm" @click="doRemove">删除</Button>
          </div>
        </div>

        <SessionTimeline :task-id="task.id" :consume-force-scroll="consumeForceScroll" />

        <!-- 决策横幅：在输入区上方，手机免滚动直接操作（方案 §20） -->
        <div v-if="decision" class="mx-auto w-full max-w-3xl px-3 pb-2 md:px-4">
          <ApprovalBanner :decision="decision" :loading="approvalBusy" @approve="onApprove" @deny="onDeny" />
        </div>

        <InterveneInput
          :can-cancel="canCancel"
          :can-send="canSendPrompt || !!decision"
          @send="submitPrompt"
          @cancel="cancel"
        />
      </div>

      <!-- 宽屏：常驻右栏（收起时就是那条 46px 贴边条，宽度由上面的 grid 给） -->
      <FeedbackPanel v-if="isWide" mode="dock" :task-id="task.id" :can-rewind="canRewind" />

      <!-- 移动端：整屏。用 v-show 而不是 v-if，切回来时不重新挂载、不重拉。 -->
      <FeedbackPanel
        v-else-if="isMobile"
        v-show="mobilePane === 'feedback'"
        mode="full"
        :active="mobilePane === 'feedback'"
        :task-id="task.id"
        :can-rewind="canRewind"
      />

      <!-- 平板：420px 侧栏（Drawer 就是那个 420px 的框；面板自带头部与滚动，
           所以 flush 让 Drawer 别再加内边距和第二层滚动） -->
      <Drawer v-else :open="feedbackOpen" width="420px" flush @close="feedbackOpen = false">
        <FeedbackPanel
          mode="drawer"
          :task-id="task.id"
          :can-rewind="canRewind"
          @close="feedbackOpen = false"
        />
      </Drawer>
    </div>
  </div>

  <!-- 任务不存在（已删除 / 链接失效 / 探测中） -->
  <div v-else class="flex h-full items-center justify-center">
    <EmptyState
      :title="probed ? '任务不存在或已删除' : '加载中…'"
      :hint="probed ? '任务可能已被删除，或链接失效' : ''"
    >
      <RouterLink to="/dashboard" class="text-xs text-accent hover:underline">← 返回仪表盘</RouterLink>
    </EmptyState>
  </div>
</template>

<style scoped>
/* 右列宽度由 grid-template-columns 驱动：展开 420px ⇄ 收起 46px。
   展开/收起只切 class，动画交给浏览器排版 —— 不写内联宽度、不测量像素（SPEC §5.2.1）。
   贴边条的 46px 不是随手取的：它要容下竖排文字 + 一枚数字徽章，
   再窄会截字，再宽就开始真的吃掉时间线。 */
.se-grid {
  display: grid;
  grid-template-columns: 1fr 420px;
  transition: grid-template-columns 200ms ease-out;
}
.se-grid.is-fb-collapsed {
  grid-template-columns: 1fr 46px;
}
</style>
