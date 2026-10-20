<script setup lang="ts">
// 多行文本输入（SPEC §4.4）。
//
// 与 Input 同规格（surface-subtle 底 / --radius-sm / 3px ring / text-tertiary placeholder），
// 差异只在：高度由 rows 决定、可纵向 resize；autoResize 时随内容长高且封顶（原型 composer
// 的 max-height:120px 行为 —— 输入条不能因为多写两行就把时间线顶走）。
import { nextTick, onMounted, ref, watch } from 'vue'

const props = withDefaults(
  defineProps<{
    modelValue?: string
    placeholder?: string
    disabled?: boolean
    invalid?: boolean
    rows?: number
    /** 随内容自动长高（不出现滚动条），上限由 maxHeight 控制 */
    autoResize?: boolean
    /**
     * autoResize 的高度上限（px）—— 口径是**文字区**高度，**不含 padding**。
     *
     * 输入条场景传的是 `66`（约三行文字），默认 120 是给独立表单输入框的。
     *
     * ⚠️ 这个口径很关键（2026-10-18 踩过）：若把 maxHeight 当成"盒子高度"，
     *    再叠加让位 padding，可视文字区会被压到只剩 2px —— 用户永远只能看到半行字。
     *    所以：
     *      · 上限按**文字区**比；
     *      · 盒子高度 = 文字区 + padding，由 resize 自己加回来。
     */
    maxHeight?: number
    /** 允许用户拖拽改高。输入条场景传 false —— 拖高会把时间线顶走 */
    resizable?: boolean
    ariaLabel?: string
  }>(),
  { rows: 3, invalid: false, disabled: false, autoResize: false, maxHeight: 120, resizable: true },
)

const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

const el = ref<HTMLTextAreaElement | null>(null)

// 供需要**光标位置**的上层使用（PromptInput 的斜杠补全要读 selectionStart、回填后要
// setSelectionRange）。只交出元素本身 + 聚焦，不另包一层 API —— 包装只会在下次需要
// 新能力时再改一次组件（而这里已经是"内部元素"这个最小暴露面了）。
defineExpose({ el, focus: () => el.value?.focus() })

/**
 * 按内容定高（autoResize 模式）。
 *
 * ## 三个必须拆开的前提
 *
 * ① `scrollHeight` = 内容 + 上下 padding，而 `style.height` 设的是**盒子高度**
 *    （`border-box` 下 padding 也算在内）。直接把 scrollHeight 当 height 用不会出错，
 *    但把它和**上限**比较就会错 —— 上限（66px）是"文字区"口径，同样的三行
 *    scrollHeight 是 66 + py-2*2 = 82px。所以先减出"纯内容高度"再比。
 *    （2026-10-18 线上复量/移动端反馈的"输入框内容太多看不到字"，根因之一就是它。）
 *
 * ② **空框时量不到"一行字要多高"**。`rows=1` 的空 textarea 实测只有 39.7px
 *    （border-box，含 py-2 的 16px），而一行文字需要 21.7 × 1.25 ≈ 27px 内容高 ——
 *    Chrome 会把空框压到能显示的极限，于是 `scrollHeight - padding`（≈23.7）**小于**
 *    真实的一行需求（≈27）。拿它当底线，打字就缩、那一行字被切掉一截。
 *    所以底线用 `行高 × rows` **算**出来，不用量的。
 *
 * ③ 量高的时机。设置值之后到"CSS 真正重新布局"之间有一步延迟：`nextTick` 时
 *    样式/布局未必已经 flush，此时读到的 scrollHeight 是**过渡态**（甚至缺省行高
 *    的 24px）。实测后果：`rows=1` 的输入条永远停在单行高度上，打一行字就溢出。
 *    所以量之前先读一次 `offsetHeight` **强制同步布局**。
 *
 * 另外：量之前必须先把 `height` 设为 `auto` —— 否则 scrollHeight 至少等于
 * clientHeight（= 上一次定的高度），删掉内容后不会缩回去。
 */
function resize() {
  const t = el.value
  if (!t) return
  // 空内容：保持 `auto`（高度由 `rows` 决定）—— 必须先清掉显式高度再返回，
  // 否则「多行 → 删光」会停在旧高度上（曾实测：清空后输入框仍是 144px 的大空块）。
  if (!t.value) {
    t.style.height = 'auto'
    return
  }
  t.style.height = 'auto'
  void t.offsetHeight // 强制一次同步布局，见上面 ③
  const cs = getComputedStyle(t)
  const paddingY = parseFloat(cs.paddingTop || '0') + parseFloat(cs.paddingBottom || '0')
  // 边框也算进盒子（`box-sizing: border-box`）：漏掉它，输入框会始终比内容矮 2px，
  // 于是**每一行**都有一小截被切掉 —— 凑近看像"字贴边被裁"，远看又"好像没问题"。
  const borderY = parseFloat(cs.borderTopWidth || '0') + parseFloat(cs.borderBottomWidth || '0')
  const boxExtra = paddingY + borderY
  const lineHeightRaw = parseFloat(cs.lineHeight || '0')
  const lineHeight = Number.isFinite(lineHeightRaw) && lineHeightRaw > 0 ? lineHeightRaw : 0
  const rowsContent = lineHeight * Math.max(1, props.rows)
  const content = Math.max(0, t.scrollHeight - boxExtra)
  // 上限是**文字区**口径（调用方传的是"能装几行字"），与 content 同一口径直接比。
  //
  // ⚠️ 2026-10-19 修正两处：
  //   · 以前写成 `finalContent + paddingY`，等于把 maxHeight 也当盒子高再加一遍
  //     padding（传 66 实际得到 82px 的框，多出 16px 空缺）；现在只加 padding；
  //   · 上限取 max(maxHeight, rows 对应高度)：`rows=3` 的表单框在输入一行时不会
  //     塌成 1 行高（只增不减，打字不跳）。
  const maxContent = Math.max(props.maxHeight ?? Number.POSITIVE_INFINITY, rowsContent)
  const finalContent = Math.min(Math.max(content, rowsContent), maxContent)
  t.style.height = finalContent + boxExtra + 'px'
}

watch(
  () => props.modelValue,
  () => {
    if (props.autoResize) nextTick(resize)
  },
)

// 挂载时量一次：处理"有初值"的情况（有值就会直接按内容长高）。
onMounted(() => {
  if (props.autoResize) resize()
})

function onInput(e: Event) {
  emit('update:modelValue', (e.target as HTMLTextAreaElement).value)
  if (props.autoResize) resize()
}
</script>

<template>
  <textarea
    ref="el"
    :value="modelValue"
    :placeholder="placeholder"
    :rows="rows"
    :disabled="disabled"
    :aria-label="ariaLabel"
    :aria-invalid="invalid || undefined"
    class="w-full border bg-surface-subtle px-3 py-2 text-sm leading-[1.55] text-text transition-[border-color,box-shadow] placeholder:text-text-tertiary focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-accent/40 disabled:cursor-not-allowed disabled:bg-surface-muted disabled:text-text-disabled"
    :class="[
      'rounded-[var(--radius-sm)]',
      autoResize ? 'resize-none overflow-hidden' : resizable ? 'resize-y' : 'resize-none',
      invalid
        ? 'border-error focus-visible:border-error focus-visible:ring-error/30'
        : 'border-border hover:border-border-strong focus-visible:border-accent',
    ]"
    @input="onInput"
  />
</template>
