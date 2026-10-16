<script setup lang="ts">
// ContentColumn：主区正文列的**唯一**宽度口径。
//
// 之前 `max-w-3xl` 硬编码在 5 个文件里（SessionHeader 两处、SessionTimeline、
// InterveneInput、NewTaskPage 三处），另有 max-w-4xl（Dashboard/Agents）。
// 同一条视觉基线由 5 份常量维护，任何一处改了就是错位 —— 收敛到这里的理由
// 不是"少写几个字母"，而是**对齐只能有一个定义**。
//
// 三档行为（与 useResponsive 的三档断点一一对应）：
//   ≥1280 宽屏：**左对齐 + 默认铺满可用宽度** + 可拖拽收窄 + 一次性提示。
//                左对齐是"不别扭"的根因修复：内容贴住正文区左边界，
//                右侧反馈栏收展（420 ⇄ 46）时左边界**不动**。
//                默认铺满：见 useContentWidth 里 `chosen === null` 的注释 ——
//                留 50% 空白当默认态，等于没修用户报的那个症状。
//   768–1279 平板：居中 max-w-3xl（原行为，屏幕就那么大，左对齐更差）
//   <768 移动：满宽（原行为）
//
// 为什么手柄挂在**右**侧而不是左右各一个：内容左对齐后，只有右边界是自由的；
// 左边界被侧栏钉死，给它一个拖拽点等于承诺一个做不到的动作。
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useResponsive } from '@/composables/useResponsive'
import {
  MAX_CONTENT_WIDTH,
  MIN_CONTENT_WIDTH,
  setContentWidth,
  setContentWidthBudget,
  useContentWidth,
} from '@/composables/useContentWidth'

const props = withDefaults(
  defineProps<{
    /** 额外的内边距类，由调用方给（各页面 px 口径略有不同，见各页模板） */
    pad?: string
    /**
     * 是否允许拖拽调宽。默认 true（宽屏才渲染手柄）。
     *
     * 只有**时间线**这一处传 true：头部与输入框传 false，因为拖它们时
     * 用户看不到正文变化（输入框在最下面），同一动作给两个入口会让人
     * 以为"拖了没反应"。仪表盘这类卡片栅格页也不适用（宽度由断点决定）。
     */
    resizable?: boolean
    /** 移动端/平板是否也跟随（默认 false = 只在宽屏左对齐并应用自定义宽度） */
    centerOnNarrow?: boolean
  }>(),
  { pad: '', resizable: true, centerOnNarrow: true },
)

const { isWide } = useResponsive()
const { effectiveWidth, touched, reset } = useContentWidth()

const rootEl = ref<HTMLElement | null>(null)

/**
 * 容量测量：容器自身宽度就是"还给得起多少"。
 *
 * 为什么用 ResizeObserver 而不是监听 window.resize：
 * 反馈栏收展**不改变窗口宽度**，却实实在在改变了这一列的可用宽度。
 * 监听 window 会在用户展开反馈栏后仍按旧容量夹取 —— 表现是正文溢出被裁。
 */
const budget = ref<number | null>(null)

function measure() {
  const el = rootEl.value
  if (!el) return
  // clientWidth 已扣掉父级 padding；再扣掉本组件自己的左右 padding（手柄要占位）
  const cs = getComputedStyle(el)
  const inner = el.clientWidth - parseFloat(cs.paddingLeft || '0') - parseFloat(cs.paddingRight || '0')
  budget.value = inner > 0 ? inner : null
}

let ro: ResizeObserver | null = null

onMounted(() => {
  measure()
  setContentWidthBudget(budget.value)
  if (typeof ResizeObserver !== 'undefined' && rootEl.value) {
    ro = new ResizeObserver(() => {
      measure()
      setContentWidthBudget(budget.value)
    })
    ro.observe(rootEl.value)
  }
})

onBeforeUnmount(() => {
  ro?.disconnect()
  ro = null
  // 卸载后清空容量，避免影响下一次挂载前的求值（模块级单例，不清会残留旧页面的数字）
  setContentWidthBudget(null)
})

watch(budget, (v) => setContentWidthBudget(v))

/** 宽屏才启用"左对齐 + 自定义宽度"；窄屏沿用原居中口径 */
const capped = computed(() => isWide.value)

/**
 * 内联 max-width。
 *
 * `effectiveWidth` 为 null = **用户没选过 ⇒ 铺满**：此时**不写** max-width，
 * 让内层 `w-full` 自然占满容器。写一个具体 px 会是错的 —— 我们并不知道
 * （也不该猜）容器到底多宽，CSS 自己最清楚。
 */
const style = computed(() => {
  if (!capped.value) return undefined
  const w = effectiveWidth.value
  return w === null ? undefined : { maxWidth: `${w}px` }
})

// ---- 一次性提示：告诉用户"这条线能拖" ----
//
// 为什么需要：默认铺满后，手柄**贴在正文区最右侧**、视觉上只是一条 1px 的
// 浅灰线（未悬停时完全透明）。用户不会去试一个看不见的东西 —— 而"可调节
// 宽度"正是这次改动的卖点，藏起来等于没做。
//
// 只在**用户从没调过宽度**时显示一次（touched 存 localStorage），
// 且支持手动关掉；一旦真调过就永久不再出现（不再打扰）。
const hintDismissed = ref(false)
const showHint = computed(
  () => props.resizable && capped.value && !touched.value && !hintDismissed.value && !dragging.value,
)

// ---- 拖拽 ----

const dragging = ref(false)
/** 拖动起点信息：指针 x + 起始宽度。用 ref 之外的对象存是为了避免每帧触发渲染 */
let drag: { startX: number; startWidth: number } | null = null

function onPointerMove(e: PointerEvent) {
  if (!drag) return
  // 向右拖 = 变宽；手柄在右边界，所以位移方向与宽度同号
  setContentWidth(drag.startWidth + (e.clientX - drag.startX))
}

function onPointerUp() {
  if (!drag) return
  drag = null
  dragging.value = false
  window.removeEventListener('pointermove', onPointerMove)
  window.removeEventListener('pointerup', onPointerUp)
  window.removeEventListener('pointercancel', onPointerUp)
}

/**
 * 开始拖拽。
 *
 * ⚠️ 监听挂在 **window** 而不是手柄上：指针一旦移出手柄（拖快一点必然发生），
 * 挂手柄上就再也收不到 move，宽度会卡在半途 —— 这是拖拽交互最常见的 bug。
 * setPointerCapture 也能解决，但它对"拖到窗口外再松开"的处理各浏览器不一，
 * window 监听是这里唯一稳妥的写法。
 */
function onPointerDown(e: PointerEvent) {
  if (!isWide.value || e.button !== 0) return
  e.preventDefault()
  // 起始宽度取**当前生效宽度**：铺满态下 = 容器宽度，这样第一下拖动不会跳变
  drag = { startX: e.clientX, startWidth: currentPx() }
  dragging.value = true
  window.addEventListener('pointermove', onPointerMove)
  window.addEventListener('pointerup', onPointerUp)
  window.addEventListener('pointercancel', onPointerUp)
}

onBeforeUnmount(onPointerUp)

/** 当前实际像素宽度（铺满态下取测量到的容器宽度），用于拖拽/键盘的起点 */
function currentPx(): number {
  return effectiveWidth.value ?? budget.value ?? MIN_CONTENT_WIDTH
}

/** 双击复位到"铺满" */
function onDoubleClick() {
  reset()
}

/**
 * 键盘可达性：手柄是 `role="separator"`，用 ←/→ 调整。
 * 只做键盘不做 roving tabindex —— 它是页面里唯一的这类控件。
 */
function onKeydown(e: KeyboardEvent) {
  const step = e.shiftKey ? 64 : 16
  if (e.key === 'ArrowLeft') setContentWidth(currentPx() - step)
  else if (e.key === 'ArrowRight') setContentWidth(currentPx() + step)
  else if (e.key === 'Home') reset()
  else return
  e.preventDefault()
}
</script>

<template>
  <div ref="rootEl" class="flex w-full min-w-0 justify-center" :class="props.pad" data-testid="content-column">
    <!-- 内层：宽度口径的唯一落点。
         宽屏 `mr-auto`（左对齐）+ 可选 max-width；窄屏 `mx-auto`（居中）+ max-w-3xl。
         ⚠️ 外层 `justify-center` 与内层 `mr-auto` 并存**已真机量过**（2026-10-16，CDP）：
         1888px 视口下把 max-width 依次设成 768/1100/1400，内层左边界恒为 272px
         （= 侧栏 256 + 16 padding），宽度随设置变化 ⇒ 左对齐确实生效、right 留白随宽度走。
         所以窄屏的居中与宽屏的左对齐可以共存于这一处 class 切换，不要"顺手"删掉其中一个。 -->
    <div
      class="relative w-full min-w-0"
      :class="capped ? 'mr-auto' : 'mx-auto max-w-3xl'"
      :style="style"
      data-testid="content-inner"
    >
      <slot />

      <!-- 拖拽手柄：贴在内层右边界外沿。
           只在宽屏 + resizable 时渲染 —— 窄屏没有"可调"这回事，
           摆一个拖不动的控件就是死控件。 -->
      <div
        v-if="resizable && capped"
        class="group/resizer absolute inset-y-0 -right-3 flex w-3 cursor-col-resize touch-none select-none items-center justify-center"
        role="separator"
        aria-orientation="vertical"
        aria-label="调整正文宽度"
        :aria-valuenow="Math.round(currentPx())"
        :aria-valuemin="MIN_CONTENT_WIDTH"
        :aria-valuemax="MAX_CONTENT_WIDTH"
        tabindex="0"
        title="拖动调整正文宽度（双击复位）"
        data-testid="content-resizer"
        @pointerdown="onPointerDown"
        @dblclick="onDoubleClick"
        @keydown="onKeydown"
      >
        <!-- 视觉 1px 竖线，命中区 12px（w-3）—— 与「视觉 36 / 命中 44」同一套判据：
             线要细，但抓得住。拖动中/悬停/聚焦时变 accent 并加宽，给出反馈。 -->
        <span
          class="h-full w-px transition-colors"
          :class="
            dragging
              ? 'bg-accent'
              : 'bg-border/0 group-hover/resizer:bg-accent/50 group-focus-visible/resizer:bg-accent'
          "
        />
      </div>

      <!-- 一次性提示：默认铺满后手柄是一条几乎看不见的线，不说一句用户不会发现它。
           位置在正文区右上角（不压内容、不抢时间线的注意力），关闭后永久不再出现。 -->
      <div
        v-if="showHint"
        class="absolute -top-1 right-0 z-20 flex max-w-[min(100%,22rem)] items-start gap-2 rounded-lg border border-accent/40 bg-accent/10 px-2.5 py-1.5 text-[11.5px] leading-snug text-text-secondary shadow-sm"
        data-testid="content-width-hint"
      >
        <span class="min-w-0">
          正文已铺满。把右侧竖线<b class="font-semibold text-accent">左右拖动</b>可调宽度，双击复位。
        </span>
        <button
          type="button"
          class="shrink-0 cursor-pointer rounded px-1 leading-none text-text-tertiary transition-colors hover:bg-surface-muted hover:text-text"
          title="知道了"
          aria-label="关闭提示"
          data-testid="content-width-hint-close"
          @click="hintDismissed = true"
        >
          ×
        </button>
      </div>
    </div>
  </div>
</template>
