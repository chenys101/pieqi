<script setup lang="ts">
// 干预输入区（方案 §21）：底部输入框，模型选择器与动作按钮一起嵌在框内。
// 运行中（pending/running/waiting_input）→ ■ 中止；终态 → ✈ 发送（续问 Resume）。
//
// 布局（R8 AC-R8-01）：外条满宽承载 border-top / 底色，内层交给 ContentColumn ——
// 与 SessionHeader / SessionTimeline 共用**同一条**左边界（宽屏左对齐 + 可拖拽调宽；
// 平板/移动端居中 max-w-3xl）。这里刻意 `resizable=false`：调宽手柄只在时间线上
// 出现一次（拖这里时看不到正文变化，输入框在最下面），同一动作不该给两个入口。
//
// 模型选择器：**嵌在输入框内**（PromptInput 的 #lead 插槽，与发送按钮同一行，
// 在左），点开是**自下而上**的弹层（ModelPicker）。选完立即落库 —— 会话级切换，
// 不是"下一轮临时用一下"，所以不需要等到发送才生效，理由见 SessionPage.switchModel。
import { computed, ref } from 'vue'
import PromptInput from './PromptInput.vue'
import ModelPicker from './ModelPicker.vue'
import ModePicker from './ModePicker.vue'
import ImagePicker from './ImagePicker.vue'
import UsageBadge from './UsageBadge.vue'
import ContentColumn from '@/components/layout/ContentColumn.vue'
import { useNotificationStore } from '@/stores/notification'
import type { AgentModelDto, ModeOptionDto } from '@/types/api'
import type { TaskUsage } from '@/types/task'
import type { PendingImage } from '@/types/image'

const props = defineProps<{
  canCancel: boolean
  canSend: boolean
  /** 提交在途（防重） */
  busy?: boolean
  /** 该任务 agent 的可选模型（空 = 该 agent 不下发清单） */
  models?: AgentModelDto[]
  /** 模型清单在途（弹层内显示加载中） */
  modelsLoading?: boolean
  /** 清单是否已尝试加载过（区分"还在读"与"确实没有"） */
  modelsLoaded?: boolean
  /** 会话当前模型（不透明值，空 = Agent 默认）。触发器上显示的就是它。 */
  currentModel?: string
  /** 切换请求在途 */
  switching?: boolean
  /** 运行中不可切换（这一轮的模型已经定死了，见后端 setTaskModel） */
  switchDisabled?: boolean
  /** 本会话可选 ACP 模式（空 = 不显示 ModePicker） */
  modes?: ModeOptionDto[]
  /** 会话当前模式 id */
  currentMode?: string
  /** 模式切换在途 */
  switchingMode?: boolean
  /**
   * 该 agent 能否收图（后端 agentCapabilities.promptCapabilities.image）。
   *
   * 为 false 时**整个加图入口都不出现** —— 摆一个按了必然失败的按钮，
   * 比没有这个功能更糟（用户会反复试，而每次都以一句 agent 报错收场）。
   */
  canAttachImages?: boolean
  /**
   * 上下文用量（agent 上报）。undefined = 该 agent 不报，整块不渲染。
   * 从头部下移到输入条（方案 C）：运行中禁用输入时它仍然可见。
   */
  usage?: TaskUsage
  /** 执行 Agent 展示名（Claude Code / Qoder CLI）；与用量、模型并排 */
  agentLabel?: string
  /** Agent 悬停说明：业务名 + transport（显示名相同、通道不同时用来区分） */
  agentTitle?: string
}>()
const emit = defineEmits<{
  send: [text: string, images: PendingImage[]]
  cancel: []
  'switch-model': [value: string]
  'switch-mode': [value: string]
}>()

const notify = useNotificationStore()

/** Agent 胶囊的首字母（模板里不做 undefined 收窄；显示名首字母即可） */
const agentInitial = computed(() => (props.agentLabel || '').slice(0, 1).toUpperCase())

/**
 * 输入框的**文字区**高度上限（px，不含内边距、不含动作行）。
 *
 * 数字跟着输入条的空间预算走：375×667（iPhone SE / 带浏览器工具栏的小屏）下
 * 主区只有 ~141px，底栏 50px 不吃主区。动作行现在排在文字区**下方**
 * （见 PromptInput 的说明），所以整个 composer-bar ≈ 文字区 + 动作行 44 + 内边距 ~30
 * ⇒ 三行文字时约 220px，四行以上全部封顶在 66px 文字区（约三行，其余框内滚动）。
 * 余下的留给时间线 —— 输入条再高就是在挤会话内容。
 *
 * 改这个值必须重跑 layout 用例：`composerBar` 的高度是与基线逐像素比的
 * （e2e/baseline/layout-baseline.json）。
 */
const COMPOSER_MAX_HEIGHT = 66

/**
 * 可写可发时用「发送态」行高（文字区封顶）；仅中止（pending / 等审批）保留旧 `rows=2`。
 * running 现在 canSend=true：中止与发送并存，输入框保持可写以便排队带图。
 */
const composing = computed(() => props.canSend)

const text = ref('')
const images = ref<PendingImage[]>([])
const submitting = ref(false)
const imagePicker = ref<{ open: (data: DataTransfer) => void } | null>(null)

// 有图就算有内容 —— "只发一张图、不写字"是正当用法（让 agent 描述图片）。
const sendDisabled = computed(
  () => (!text.value.trim() && images.value.length === 0) || !props.canSend || submitting.value,
)

const placeholder = computed(() => {
  if (!props.canSend) return '运行中… 可中止'
  if (props.canCancel) return '发送补充（本轮结束后排队）… 可带图'
  return '发送补充… 输入 / 触发命令/Skill'
})

async function onSend() {
  if (sendDisabled.value) return
  submitting.value = true
  try {
    emit('send', text.value.trim(), images.value)
    text.value = ''
    // 图发出去就清空：留在框里会让人以为"还没发"，且下一轮会重复带上
    // （而后端是**按轮**的，不会替我们记住）。
    images.value = []
  } finally {
    submitting.value = false
  }
}

function onPick(value: string) {
  emit('switch-model', value)
}

/**
 * 输入框里的粘贴：转交 ImagePicker（截图直接贴进来）。
 *
 * 能发（canSend）时入口已渲染；不能发时若 agent 本可收图，给一句人话，
 * 避免静默失败（用户会以为图已经贴上了）。
 */
function onPaste(data: DataTransfer | null) {
  if (!props.canSend) {
    if (props.canAttachImages) {
      notify.push('当前无法追加图片，请先处理审批或等可发送后再试', 'info')
    }
    return
  }
  if (data) imagePicker.value?.open(data)
}
</script>

<template>
  <!-- `shrink-0` 是**硬约束**，不是排版偏好。
       生产里本组件是 `SessionPage` 那条 flex 列的**最后一项**：时间线吃
       `flex-1`，输入区若允许收缩，矮视口下就会被压缩 —— 而压缩只会作用在
       没写 shrink 的子块上：让位区不会缩、按钮行是绝对定位，于是被压掉的是
       **发送按钮所在的那一段**，表现是按钮落到容器外、被移动端底栏的
       `RouterLink` 拦住点击（Playwright 的 click 直接报 intercepts pointer events）。
       浏览器工具栏占位的 375×667 下主区只有 ~141px，这条实测踩过两轮。
       高度已经由内容自适应控制（见下方 rows/max-height），不需要再靠收缩腾地方。 -->
  <div class="shrink-0 border-t border-border bg-surface/80 px-3 py-2.5 backdrop-blur md:px-4" data-testid="composer-bar">
    <!-- 宽度口径由 ContentColumn 统一给（宽屏左对齐 + 可调，窄屏居中 max-w-3xl），
         与 SessionHeader / SessionTimeline 共用同一条左边界（AC-R8-01）。
         手柄只在时间线上出现一次 —— 输入框这里再摆一个会是同一动作两个入口，
         且拖这里时用户看不到正文变化（输入框在最下面）。 -->
    <ContentColumn :resizable="false">
      <div class="w-full" data-testid="composer-inner">
        <!-- 高度口径：空框一行、随内容长高、**文字区封顶 66px**（约三行），
             其余在框内翻页（↑↓）。动作行在文字区**下方**、按内容自己撑高，
             不再从文字区里"让位" —— 两处的数字不再互相牵制。
             为什么封顶而不无限长高：输入条在 flex 列末尾，长高就是挤掉时间线；
             框内翻页比"看不见时间线"更可接受。 -->
        <PromptInput
          v-model="text"
          :rows="composing ? 1 : 2"
          :max-height="composing ? COMPOSER_MAX_HEIGHT : undefined"
          aria-label="补充指令"
          :placeholder="placeholder"
          :disabled="!composing"
          @submit="composing && onSend()"
          @paste="onPaste"
        >
          <!-- #lead 行：加图按钮 + 模型选择器（都靠左，与右侧发送/中止同一行）。
               ⚠️ 必须放在 #lead 而不是 #actions：#actions 整行是 justify-end，
               塞进去会被推到最右、和发送按钮挤在一起。 -->
          <template #lead>
            <ImagePicker
              v-if="canAttachImages && canSend"
              ref="imagePicker"
              v-model="images"
              :disabled="submitting"
            />
            <!-- 上下文用量：从头部下移到这里，紧跟图片按钮（方案 C 需求 2）。
                 没有上报的 agent 整块不渲染；运行中图片按钮消失时它自然落到行首。 -->
            <UsageBadge v-if="usage" :usage="usage" dense class="text-[10px] text-muted" />
            <!-- 执行 Agent：始终可见（终态/运行中都显示）；与模型选择器并排。
                 不是按钮 —— 它只回答"是谁在跑"，不承担切换动作。 -->
            <span
              v-if="agentLabel"
              class="inline-flex min-w-0 max-w-[96px] items-center gap-1 rounded-[var(--radius-sm)] border border-border bg-surface-subtle px-1.5 py-0.5 text-[10px] text-text-secondary"
              :title="agentTitle"
              data-testid="composer-agent"
            >
              <span
                class="grid h-3.5 w-3.5 shrink-0 place-items-center rounded-[3px] bg-accent/10 text-[8px] font-bold text-accent"
                aria-hidden="true"
              >{{ agentInitial }}</span>
              <span class="truncate">{{ agentLabel }}</span>
            </span>
            <!-- 模式可在 running 中切换（ACP session/set_mode 随时可切；Cursor agent/plan/ask）。
                 模型仍跟 switchDisabled：本轮 prompt 已发出就不能换模型。 -->
            <ModePicker
              v-if="modes?.length"
              :mode="currentMode"
              :modes="modes"
              :disabled="switchingMode"
              @update:mode="(v) => emit('switch-mode', v)"
            />
            <ModelPicker
              :model="currentModel"
              :models="models"
              :models-loaded="modelsLoaded"
              :disabled="switchDisabled || switching"
              @update:model="onPick"
            />
          </template>

        <!-- 动作行右侧：中止 ■ 与发送 ✈ 可并存（running 排队带图时）。
             视觉 36×36，命中区用伪元素外扩到 44×44（AC-R8-04 允许视觉 36）。
             `@mousedown.prevent`：点按钮不该把输入框的焦点抢走（判据见 ModelPicker）。
             仅中止时中止钮占 `composer-send`（布局锚点）；可发时发送钮占该锚点，
             中止改用 `composer-cancel`，避免两个主按钮抢同一个 testid。 -->
        <template #actions>
          <button
            v-if="canCancel"
            class="pointer-events-auto relative shrink-0 rounded-[var(--radius-sm)] bg-error/15 p-2.5 text-error transition-colors after:absolute after:-inset-1 after:content-[''] hover:bg-error/25 focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-accent/40"
            title="中止生成"
            aria-label="中止"
            :data-testid="canSend ? 'composer-cancel' : 'composer-send'"
            @click="emit('cancel')"
            @mousedown.prevent
          >
            <svg viewBox="0 0 24 24" class="h-4 w-4" aria-hidden="true">
              <rect x="6" y="6" width="12" height="12" rx="2" fill="currentColor" />
            </svg>
          </button>
          <button
            v-if="canSend"
            class="pointer-events-auto relative shrink-0 rounded-[var(--radius-sm)] bg-accent p-2.5 text-white transition-opacity after:absolute after:-inset-1 after:content-[''] hover:opacity-90 focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-accent/40 disabled:cursor-not-allowed disabled:opacity-40"
            :disabled="sendDisabled"
            title="发送 (Ctrl+Enter)"
            aria-label="发送"
            data-testid="composer-send"
            @click="onSend"
            @mousedown.prevent
          >
            <svg viewBox="0 0 24 24" class="h-4 w-4" aria-hidden="true">
              <path d="M22 2 11 13M22 2l-7 20-4-9-9-4 20-7z" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round" />
            </svg>
          </button>
        </template>
      </PromptInput>

      <!-- 该 agent 没有可选模型时的说明。
           为什么放在输入框**外面**而不是弹层里：这种情况根本点不开弹层
           （弹层只在有清单时才有内容），说明必须另有出处，否则用户永远看不到。 -->
      <p
        v-if="modelsLoaded && !models?.length"
        class="mt-1 px-1 text-xs text-muted"
        data-testid="composer-model-unsupported"
      >
        该 Agent 不提供可选模型{{ currentModel ? '，沿用其默认模型' : '' }}。
      </p>
      </div>
    </ContentColumn>
  </div>
</template>