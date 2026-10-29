<script setup lang="ts">
// Session 页（方案 §17 + SPEC §5.2）：只做布局拼装（web/README「pages 只做路由与布局拼装」）。
//
// **变更反馈按 SPEC §6.4 分三档摆放**：
//   宽屏 ≥1280   常驻右栏，宽度由 grid 驱动（展开 420px ⇄ 收起 46px 贴边条）
//   平板 768–1279 420px 侧栏，靠头部按钮开合
//   移动端 <768  整屏，由头部那一行的反馈图标切换 时间线 / 变更反馈
// 三档判据、详情加载、决策动作、模型切换都在 features/session/useSessionPage.ts。
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { useSessionPage } from '@/features/session/useSessionPage'
import { SessionHeader, SessionTimeline, ApprovalBanner, InterveneInput } from '@/features/session'
import { FeedbackPanel, ArtifactViewer } from '@/features/feedback'
import PlanList from '@/features/timeline/components/PlanList.vue'
import Drawer from '@/components/ui/Drawer.vue'
import ContentColumn from '@/components/layout/ContentColumn.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Button from '@/components/ui/Button.vue'

const route = useRoute()
const taskId = computed(() => route.params.id as string)

const {
  task,
  canCancel,
  canSendPrompt,
  cancel,
  consumeForceScroll,
  isMobile,
  isWide,
  fbPanel,
  artifactsStore,
  viewing,
  decision,
  approvalBusy,
  onApprove,
  onApproveSession,
  onDeny,
  loadError,
  probed,
  retryLoadDetail,
  canRewind,
  feedbackOpen,
  mobilePane,
  openFeedback,
  onFocusTurn,
  turnModels,
  turnModelsLoading,
  turnModelsLoaded,
  currentModel,
  switchingModel,
  canSwitchModel,
  switchModel,
  currentMode,
  sessionModes,
  switchingMode,
  switchMode,
  agentLabel,
  agentTitle,
  canAttachImages,
  onSendPrompt,
  removeConfirming,
  askRemove,
  doRemove,
} = useSessionPage(taskId)
</script>

<template>
  <div v-if="task" class="flex h-full flex-col">
    <!-- 宽屏用 grid（**反馈栏**宽度由 grid-template-columns 驱动，展开/收起只切 class，
         动画交给浏览器排版 —— 不写内联宽度、不测量像素，SPEC §5.2.1）；
         其余档位是普通 flex 排布。
         注：**正文列**的宽度口径不在这里，在 ContentColumn（宽屏左对齐 + 可拖拽调宽）。
         两者是正交的两件事：grid 管"反馈栏占多宽"，ContentColumn 管"正文能多宽"。 -->
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
          <div v-if="removeConfirming" class="shrink-0 px-3 pb-2 md:px-4">
            <ContentColumn :resizable="false">
              <div class="flex flex-wrap items-center gap-2 rounded-lg border border-error/40 bg-error/5 px-3 py-2">
                <span class="min-w-0 flex-1 text-xs text-error">删除该任务？删除后不可恢复。</span>
                <Button variant="ghost" size="sm" @click="removeConfirming = false">取消</Button>
                <Button variant="danger" size="sm" @click="doRemove">删除</Button>
              </div>
            </ContentColumn>
          </div>

          <!-- 当前计划快照（Task.plan）：Cursor create_plan / ACP session/update(plan)
               写入；刷新后仍在。时间线里另有逐次 EventPlan 卡片保留历史。 -->
          <div
            v-if="task.plan?.entries?.length"
            class="shrink-0 px-3 pb-2 md:px-4"
            data-testid="session-plan-sticky"
          >
            <ContentColumn :resizable="false">
              <PlanList :entries="task.plan.entries" />
            </ContentColumn>
          </div>

          <SessionTimeline
            :task-id="task.id"
            :consume-force-scroll="consumeForceScroll"
            @focus-turn="onFocusTurn"
          />

          <!-- 决策横幅：在输入区上方，手机免滚动直接操作（方案 §20）
               shrink-0 必需：横幅里的审批摘要展开后可能很高，不加它会被 flex 压缩
               （内容被裁、按钮被挤走）；同时给它限高 + 内部滚动，
               保证「批准/拒绝」与下方输入框始终留在可视区。 -->
          <div v-if="decision" class="shrink-0 px-3 pb-2 md:px-4">
            <ContentColumn :resizable="false">
              <div class="max-h-[45vh] overflow-y-auto overscroll-contain">
                <ApprovalBanner
                  :decision="decision"
                  :loading="approvalBusy"
                  @approve="onApprove"
                  @approve-session="onApproveSession"
                  @deny="onDeny"
                />
              </div>
            </ContentColumn>
          </div>

          <InterveneInput
            :can-cancel="canCancel"
            :can-send="canSendPrompt || !!decision"
            :models="turnModels"
            :models-loading="turnModelsLoading"
            :models-loaded="turnModelsLoaded"
            :current-model="currentModel"
            :switching="switchingModel"
            :switch-disabled="!canSwitchModel"
            :modes="sessionModes"
            :current-mode="currentMode"
            :switching-mode="switchingMode"
            :can-attach-images="canAttachImages"
            :usage="isMobile ? task.usage : undefined"
            :agent-label="isMobile ? agentLabel : ''"
            :agent-title="agentTitle"
            @send="onSendPrompt"
            @cancel="cancel"
            @switch-model="switchModel"
            @switch-mode="switchMode"
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

    <!-- 产物只读 viewer（时间线 / 概览证据卡共用；html 不进这里） -->
    <ArtifactViewer
      :open="!!viewing && viewing.taskId === task.id"
      :task-id="task.id"
      :path="viewing?.path ?? null"
      :kind="viewing?.kind"
      @close="artifactsStore.closeViewer()"
    />
  </div>

  <!-- 没有任务可渲染时的三种真实情形，**不要说成同一句**：
       · 还在拉            → 加载中…
       · 拉挂了（超时/断网/5xx）→ 说清楚原因 + 重试入口（它并不等于任务没了）
       · 服务端说没有这个任务  → 任务不存在或已删除 -->
  <div v-else class="flex h-full items-center justify-center">
    <EmptyState
      :title="!probed ? '加载中…' : loadError ? '任务详情加载失败' : '任务不存在或已删除'"
      :hint="!probed ? '' : loadError ? loadError : '任务可能已被删除，或链接失效'"
    >
      <div class="flex items-center gap-3">
        <button
          v-if="probed && loadError"
          type="button"
          class="rounded-md bg-accent px-2.5 py-1 text-xs font-medium text-white transition-opacity hover:opacity-90"
          @click="retryLoadDetail"
        >
          重新加载
        </button>
        <RouterLink to="/dashboard" class="text-xs text-accent hover:underline">← 返回仪表盘</RouterLink>
      </div>
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