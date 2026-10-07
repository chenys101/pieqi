<script setup lang="ts">
// Session 页（方案 §17 + SPEC §5.2）：V2 核心页面 —— Header / 审批横幅 / Timeline / 干预输入。
//
// **变更反馈按 SPEC §6.4 分三档摆放**，不是"一个抽屉走天下"：
//   宽屏 ≥1280   常驻右栏，宽度由 grid 驱动（展开 420px ⇄ 收起 46px 贴边条）
//   平板 768–1279 420px 侧栏，靠头部按钮开合
//   移动端 <768  整屏，由头部那一行的反馈图标切换 时间线 / 变更反馈
// 1280 是"反馈怎么摆"的拐点而不是 768 —— 理由见 useResponsive 的注释。
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useTaskStore } from '@/stores/task'
import { useSessionStore } from '@/stores/session'
import { useAgentStore } from '@/stores/agent'
import * as api from '@/services/api/tasks'
import { adaptTask } from '@/services/api/client'
import { useSession } from '@/composables/useSession'
import { useResponsive } from '@/composables/useResponsive'
import { useFeedbackPanelStore } from '@/stores/feedbackPanel'
import { SessionHeader, SessionTimeline, ApprovalBanner, InterveneInput } from '@/features/session'
import { FeedbackPanel } from '@/features/feedback'
import Drawer from '@/components/ui/Drawer.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Button from '@/components/ui/Button.vue'

const route = useRoute()
const router = useRouter()
const taskStore = useTaskStore()
const sessionStore = useSessionStore()
const agentStore = useAgentStore()
const { isMobile, isWide } = useResponsive()
const fbPanel = useFeedbackPanelStore()

const taskId = computed(() => route.params.id as string)
const { task, canCancel, canSendPrompt, submitPrompt, cancel, approve, approveSession, deny, consumeForceScroll } =
  useSession(taskId)

/**
 * 续问可选的模型：按任务自己的 agent 拉清单（store 内幂等 + 失败可重试）。
 * 拉不到就是空数组 → 组件隐藏选择器，续问照常走"沿用当前模型"。
 * 这里不关心探测失败的原因：换模型是可选能力，不该在会话页弹错误。
 */
watch(
  () => task.value?.agent,
  (a) => {
    if (a) void agentStore.loadModels(a)
  },
  { immediate: true },
)
const turnModels = computed(() => agentStore.modelsOf(task.value?.agent || ''))
const turnModelsLoading = computed(() => !!agentStore.modelsLoading[task.value?.agent || ''])

/** 决策横幅：waiting_input 且带 decision 时展示 */
const decision = computed(() => (task.value?.status === 'waiting_input' ? task.value.decision : undefined))
const approvalBusy = ref(false)
/** 冷启动探测完成（详情已尝试拉取），仍无任务 → 视为不存在 */
const probed = ref(false)

/** Agent 执行中禁止回退（静止边界原则）；面板仍可查看 */
const canRewind = computed(() => !!task.value && task.value.status !== 'running' && task.value.status !== 'pending')

/** 平板档：反馈走 420px 侧栏（宽屏常驻、移动端整屏，都不用它） */
const feedbackOpen = ref(false)
/** 移动端整屏：时间线 / 变更反馈 二选一（头部那一行的反馈图标来回切） */
const mobilePane = ref<'timeline' | 'feedback'>('timeline')

/**
 * 反馈入口的落点按档位不同 —— 三档的入口本来就不是同一个东西。
 * 宽屏**没有这个按钮**（贴边条就是它的入口）。
 */
function openFeedback() {
  if (isWide.value) {
    fbPanel.expand()
    return
  }
  if (isMobile.value) {
    // 移动端图标是**开关**：面板里没别的路回时间线，再点一次就是回去
    mobilePane.value = mobilePane.value === 'timeline' ? 'feedback' : 'timeline'
    return
  }
  feedbackOpen.value = true
}

// 详情加载：WS 快照 / 列表只给**轻量视图**（不含 events），所以进入详情页时
// 必须按 id 拉一次完整详情来填充时间线。
//
// ⚠️ 判据是「本地是否已有该会话的事件流」，**不是**「任务是否已在列表里」。
// 早期实现只要 taskStore.byId(id) 命中就跳过拉取 —— 而列表/快照现在必然命中，
// 于是详情页永远拿不到 events：表现是「追加了一次会话，但没展示追加内容」。
// 拉取本身很轻（单任务 300KB 级），且 syncFromTask 是幂等替换 + 去重。
watch(
  taskId,
  (id) => {
    probed.value = false
    // 换任务回到时间线：停在「变更反馈」里换到一个新任务，看到的是上一任务的语境残留
    mobilePane.value = 'timeline'
    if (!id) return
    const hasEvents = sessionStore.events(id).length > 0
    if (taskStore.byId(id) && hasEvents) {
      probed.value = true
      return
    }
    void loadDetail(id)
  },
  { immediate: true },
)

/**
 * 拉取任务完整详情（含事件流）并按 id 同步进 store。
 *
 * 统一走这个函数而不是直接调 taskStore.refreshTask：会话事件流由 sessionStore 持有，
 * 只刷新 taskStore 会让任务状态更新、时间线却还是旧的（两处状态各自为政）。
 */
async function loadDetail(id: string) {
  try {
    const dto = await api.getTaskDto(id)
    taskStore.upsertTask(adaptTask(dto))
    sessionStore.syncFromTask(dto)
  } catch {
    // 拉取失败：保持现状，由 probed 决定是否显示"任务不存在"
  } finally {
    probed.value = true
  }
}

/** 决策请求期间的忙态：三个动作共用同一段收尾，各写一份 try/finally 才会漏掉一个 */
async function runDecision(fn: () => Promise<void>) {
  approvalBusy.value = true
  try {
    await fn()
  } finally {
    approvalBusy.value = false
  }
}

const onApprove = () => runDecision(approve)
const onApproveSession = () => runDecision(approveSession)
const onDeny = () => runDecision(deny)

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
    <!-- 宽屏用 grid（宽度由 grid-template-columns 驱动，展开/收起只切 class，
         动画交给浏览器排版 —— 不写内联宽度、不测量像素，SPEC §5.2.1）；
         其余档位是普通 flex 排布。 -->
    <div class="min-h-0 flex-1" :class="isWide ? ['se-grid', { 'is-fb-collapsed': fbPanel.collapsed }] : 'flex'">
      <div class="flex min-h-0 min-w-0 flex-1 flex-col">
        <SessionHeader
          :task="task"
          :can-cancel="canCancel"
          :compact="isMobile"
          :show-feedback="!isWide"
          :feedback-active="mobilePane === 'feedback'"
          @cancel="cancel"
          @remove="askRemove"
          @feedback="openFeedback"
        />

        <!-- 移动端切到「变更反馈」时，时间线整段让位（v-show：不卸载，切回来保留滚动位置） -->
        <div v-show="!isMobile || mobilePane === 'timeline'" class="flex min-h-0 flex-1 flex-col">
          <!-- 内联确认条：紧贴任务头部，用户看着任务名确认删的是不是它 -->
          <div v-if="removeConfirming" class="mx-auto w-full max-w-3xl shrink-0 px-3 pb-2 md:px-4">
            <div class="flex flex-wrap items-center gap-2 rounded-lg border border-error/40 bg-error/5 px-3 py-2">
              <span class="min-w-0 flex-1 text-xs text-error">删除该任务？删除后不可恢复。</span>
              <Button variant="ghost" size="sm" @click="removeConfirming = false">取消</Button>
              <Button variant="danger" size="sm" @click="doRemove">删除</Button>
            </div>
          </div>

          <SessionTimeline :task-id="task.id" :consume-force-scroll="consumeForceScroll" />

          <!-- 决策横幅：在输入区上方，手机免滚动直接操作（方案 §20）
               shrink-0 必需：横幅里的审批摘要展开后可能很高，不加它会被 flex 压缩
               （内容被裁、按钮被挤走）；同时给它限高 + 内部滚动，
               保证「批准/拒绝」与下方输入框始终留在可视区。 -->
          <div v-if="decision" class="mx-auto w-full max-w-3xl shrink-0 px-3 pb-2 md:px-4">
            <div class="max-h-[45vh] overflow-y-auto overscroll-contain">
              <ApprovalBanner
                :decision="decision"
                :loading="approvalBusy"
                @approve="onApprove"
                @approve-session="onApproveSession"
                @deny="onDeny"
              />
            </div>
          </div>

          <InterveneInput
            :can-cancel="canCancel"
            :can-send="canSendPrompt || !!decision"
            :models="turnModels"
            :models-loading="turnModelsLoading"
            @send="submitPrompt"
            @cancel="cancel"
          />
        </div>

        <!-- 移动端：整屏反馈。放在左列内部（而不是它的兄弟）是因为那唯一一行
             头部要一直留着 —— 否则切进反馈后就没有回时间线的路了。
             用 v-show 而不是 v-if，切回来时不重新挂载、不重拉。 -->
        <FeedbackPanel
          v-if="isMobile"
          v-show="mobilePane === 'feedback'"
          class="min-h-0 flex-1"
          mode="full"
          :active="mobilePane === 'feedback'"
          :task-id="task.id"
          :can-rewind="canRewind"
        />
      </div>

      <!-- 宽屏：常驻右栏（收起时就是那条 46px 贴边条，宽度由上面的 grid 给） -->
      <FeedbackPanel v-if="isWide" mode="dock" :task-id="task.id" :can-rewind="canRewind" />

      <!-- 平板：420px 侧栏（Drawer 就是那个 420px 的框；面板自带头部与滚动，
           所以 flush 让 Drawer 别再加内边距和第二层滚动） -->
      <Drawer v-if="!isWide && !isMobile" :open="feedbackOpen" width="420px" flush @close="feedbackOpen = false">
        <FeedbackPanel mode="drawer" :task-id="task.id" :can-rewind="canRewind" @close="feedbackOpen = false" />
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
