<script setup lang="ts">
// ModelPicker：嵌在输入框里的模型选择器（触发器 + **自下而上**的弹层）。
//
// 为什么不用现成的 Select.vue（原生 <select>）：原生下拉在桌面端向下展开、
// 在移动端拉起的是系统选择器 —— 两者都无法做成"贴着输入框往上弹"，
// 也无法控制移动端弹层的高度与安全区。要的就是这个弹层形态，所以自己实现。
//
// 三处移动端考量（模板里逐条标注）：
//   1. 贴底弹出 + max-h 由「触发器上方可用空间」与 60dvh 取小：小屏上盖住输入框但不遮满整屏，还能往上滚；
//   2. 贴视口底时避让安全区：iPhone 底部横条/Home 指示器会压住最后一项；
//   3. 每个选项 min-h-11（44px）：满足触控最小命中区。
//
// 取值纪律：value 是 agent 下发的**不透明串**，原样回传，本组件不解析不拼接。
//
// 焦点纪律：触发器与弹层里所有可点元素都 `@mousedown.prevent`。
// 它们现在排在输入框**下方**的动作行里（见 PromptInput），已经不是"盖在文字上"的浮层，
// 但用户的操作顺序没变：先写一半、点一下换个模型、接着写。不拦 mousedown 的话，
// 点触发器会把 input 的焦点抢走 —— 接着敲的字谁也不收（实测：点开再关掉，
// `document.activeElement` 停在触发器上，输入框里一个字都没进去）。
//
// ★★ 层叠（2026-10-23 改）：弹层 **Teleport 到 body**，用触发器 rect 算 fixed 定位。
//
//   旧实现把弹层 `absolute bottom-full z-50` 挂在触发器旁边 —— 它于是活在
//   **输入栏的层叠上下文里**（`composer-bar` 有 `backdrop-blur`，backdrop-filter
//   会自成层叠上下文）。z-50 只在那条输入栏内部有效，与时间线/正文谁在上由
//   输入栏所在那一层决定。一旦被主体盖住，表现就是"只看到露出输入框上方的一条缝"、
//   点选项点到后面的消息（本仓库 2026-10-19 记过同样症状）。
//   Teleport 到 body 后弹层在最外层，不再受任何祖先层叠上下文牵连。
//
//   定位必须自己算：改 fixed 后 `bottom-full` / `left-0` 都失去意义。规则 ——
//   手机**贴视口底部**（两侧留白，弹层比挂在窄触发器上宽得多，长描述不会挤成三行）；
//   宽屏挂在触发器正上方（不左右横移，避免"点哪儿弹哪儿"的错位感）。
//   高度上限 = `min(60dvh, 触发器上方可用空间 - 8)`：保证首项一定可见 ——
//   旧实现实测在小屏上弹层顶被裁到视口外（375×667 时顶边 -287px）。
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import type { AgentModelDto } from '@/types/api'

const props = withDefaults(
  defineProps<{
    /** 会话当前模型（不透明值）。空 = 用 agent 默认。 */
    model?: string
    /** 可选清单。空 = 该 agent 不下发清单。 */
    models?: AgentModelDto[]
    /** 清单是否已尝试加载过（区分"还在读"与"确实没有"） */
    modelsLoaded?: boolean
    disabled?: boolean
  }>(),
  { model: '', models: () => [], modelsLoaded: false, disabled: false },
)

const emit = defineEmits<{ 'update:model': [value: string] }>()

const open = ref(false)
const root = ref<HTMLElement | null>(null)

const catalog = computed(() => props.models ?? [])
const hasCatalog = computed(() => catalog.value.length > 0)

/** 当前模型的人读名。查不到（清单没到 / 该值不在清单里）时退回中性的说法。 */
const currentName = computed(() => {
  if (!props.model) return 'Agent 默认'
  return catalog.value.find((m) => m.value === props.model)?.name ?? '当前模型'
})

/** 清单能否**解析**当前值：只有这时 currentName 才是真正的名字，而不是"当前模型"兜底 */
const currentResolved = computed(() => !!props.model && catalog.value.some((m) => m.value === props.model))

/**
 * 触发器上的短名：太长的名字会把输入框挤扁，截断并用 title 兜住全名。
 *
 * 阈值从 20 收到 14：动作行里现在还有加图按钮与发送按钮（新建任务页三个控件同行），
 * 20 个字符的名字在 375 宽下会把行挤到换行（实测：`DeepSeek-V4.1-Flash` 19 字符时
 * 新建任务页的动作行从 62px 涨到 104px）。14 字符约 100px，三个控件在同一行放得下；
 * 鼠标悬停有全名，弹层头上也有「当前：全名」。
 *
 * ⚠️ 只在**解析不到**时用这个兜底（清单还没到、或当前值不在清单里）：
 * 那时 `currentName` 是"当前模型"四个字。解析得到时直接用真正的名字 ——
 * 它比不透明串短得多，按字符截反而会把 `DeepSeek-V4.1-Flash` 截成
 * `DeepSeek-V4.1-…` 这种看不出在哪个模型上的样子。
 */
const TRIGGER_MAX_NAME = 14
const currentLabel = computed(() => {
  const n = currentName.value
  if (currentResolved.value) return n
  return n.length > TRIGGER_MAX_NAME ? `${n.slice(0, TRIGGER_MAX_NAME - 1)}…` : n
})

/** 按分组整理（dsh 按 provider 分组；无分组信息时归入空名组，渲染时平铺） */
const groups = computed(() => {
  const out: { name: string; options: AgentModelDto[] }[] = []
  for (const m of catalog.value) {
    const name = m.group || ''
    let g = out.find((x) => x.name === name)
    if (!g) {
      g = { name, options: [] }
      out.push(g)
    }
    g.options.push(m)
  }
  return out
})

function pick(value: string) {
  emit('update:model', value)
  open.value = false
}

// ---- 弹层定位（见文件头「层叠」）----

/** 弹层与触发器之间的视觉间隙（px），与 `mb-1.5` 同一口径 */
const GAP = 6
/** 弹层离视口左右边缘的最小留白（手机贴底形态） */
const EDGE = 8
/** 弹层的最大期望高度；实际再用"触发器上方可用空间"夹一次 */
const POPUP_MAX = 320
/** 宽屏（挂在触发器上方）时的弹层宽度 */
const WIDE_WIDTH = 336

type Placement = { mobile: boolean; left: number; width: number; bottom: number; maxHeight: number }

const placement = ref<Placement | null>(null)

/**
 * 依据触发器当前位置算 fixed 定位。
 *
 * `fixed` 的参照是**视口**，而 iOS 软键盘弹出时 `innerHeight` 会缩小 ——
 * 所以位置在打开时算一次、并跟着 `resize` / `scroll` 重算（见下面的监听）。
 * 算不出来（触发器还没布局）时置 null，模板里退化成"不给内联定位"，
 * 下一帧 `nextTick` 会再算一次。
 *
 * 触发器**本身**已经缩到 ≤140px（见模板上的 max-w），所以弹层需要的最小高度也变小了：
 * 首项 + 两三项就该看得见。`FLOOR` = 这个下限（旧值 120 是按 160px 触发器 + 大内边距
 * 定的）；真正的高度是 min(期望高度, 上方可用空间, 60% 视口高)，见 measure 末尾。
 */
const FLOOR = 96
const VIEW_MARGIN = 8

function measure() {
  const el = root.value?.querySelector('button')
  if (!el) return
  const r = el.getBoundingClientRect()
  if (!r.width && !r.height) return
  const vw = window.innerWidth
  const vh = window.innerHeight
  const mobile = vw < 768
  const above = r.top - GAP
  // 上方可用空间（扣掉视口顶边留白）
  const room = Math.max(0, above - VIEW_MARGIN)
  placement.value = {
    mobile,
    // 手机：横铺满视口（两侧各留 EDGE）；宽屏：对齐触发器左沿，别超出视口
    left: mobile ? EDGE : Math.min(Math.max(EDGE, r.left), Math.max(EDGE, vw - EDGE - WIDE_WIDTH)),
    width: mobile ? vw - EDGE * 2 : Math.min(WIDE_WIDTH, vw - EDGE * 2),
    // 贴触发器上沿往上弹 —— 与"输入框永远在弹层下方"的视觉关系保持一致
    bottom: vh - above,
    // 天花板 = min(期望高度, 上方可用空间, 视口高度的 60%)。
    // ★ 2026-10-24（移动端多了一条「在「x」新建任务」入口后复测）：输入条整体上移，
    //   触发器离视口顶越来越近，375×667 下原来恒定的 120px 下限会把"上方 99px"
    //   的弹层算成 120px ⇒ 顶边伸到视口外、首项被裁。下限只能**在真有空间时**兜底。
    maxHeight: Math.max(0, Math.min(POPUP_MAX, room, Math.round(vh * 0.6))),
  }
  if (placement.value.maxHeight < FLOOR) {
    // 空间确实极小（矮屏 + 软键盘）：不再硬撑到 FLOOR，而是如实变小 ——
    // 列表自己会滚动，至少首项完整可见（"看得到上面三项的一部分"不如"看得到第一项"）。
    placement.value.maxHeight = Math.max(48, placement.value.maxHeight)
  }
}

const popupStyle = computed(() => {
  const p = placement.value
  if (!p) return undefined
  return {
    left: `${p.left}px`,
    width: `${p.width}px`,
    bottom: `${p.bottom}px`,
    maxHeight: `${p.maxHeight}px`,
  }
})

function onDocPointer(e: MouseEvent) {
  if (!open.value) return
  const t = e.target as Node
  // Teleport 之后弹层已不在 root 里 —— 两处都要判，否则点弹层会被当成"点外部"
  if (root.value?.contains(t)) return
  if ((t as Element)?.closest?.('[data-testid="model-picker-popup"]')) return
  open.value = false
}
function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape' && open.value) open.value = false
}

/**
 * 滚动 / 尺寸变化时重算。
 *
 * 不关掉弹层而是重算：手机上时间线是滚动容器，用户完全可能在弹层开着时
 * 顺手滚一下看上文 —— 直接关掉会让人以为"点错了"。
 * `capture: true` 才能收到任意滚动容器（滚动事件不冒泡）。
 */
function onReflow() {
  if (open.value) measure()
}

watch(open, (v) => {
  if (v) {
    measure()
    nextTick(measure)
    // 打开期间锁住页面滚动：否则拖动弹层内部的列表会把整页一起带走（滚动逃逸）
    document.documentElement.classList.add('overflow-hidden')
    document.addEventListener('mousedown', onDocPointer)
    document.addEventListener('keydown', onKey)
    window.addEventListener('resize', onReflow)
    window.addEventListener('scroll', onReflow, true)
  } else {
    document.documentElement.classList.remove('overflow-hidden')
    placement.value = null
    document.removeEventListener('mousedown', onDocPointer)
    document.removeEventListener('keydown', onKey)
    window.removeEventListener('resize', onReflow)
    window.removeEventListener('scroll', onReflow, true)
  }
})
onBeforeUnmount(() => {
  document.documentElement.classList.remove('overflow-hidden')
  document.removeEventListener('mousedown', onDocPointer)
  document.removeEventListener('keydown', onKey)
  window.removeEventListener('resize', onReflow)
  window.removeEventListener('scroll', onReflow, true)
})

/**
 * 清单到达后自动收起弹层。
 *
 * 触发路径：进来时清单还在拉（models=[]），用户可能先点开弹层 —— 那一刻里面是空的。
 * 清单随后到达，若不关掉，弹层会**在用户没操作的情况下自己把内容换了**，
 * 看起来像是误触。空清单态另有分支（见模板 v-if），这里处理"开着时清单到了"。
 */
watch(hasCatalog, (has) => {
  if (has) open.value = false
})
</script>

<template>
  <!-- pointer-events-auto：触发器与弹层都嵌在 PromptInput 的动作行里，
       而那一行整条是 pointer-events-none（好让让出的 padding 仍能点进 textarea）。
       不在这里显式打开，触发器和弹层都点不动。 -->
  <!-- ⚠️ testid 分两层，别再混用：
         · 这层 div 是「选择器整体」（`model-picker`）—— 量位置、判断"用户能不能点到"
           都以它为准（它是 `pointer-events-auto` 的那个盒子）；
         · 里面的 `<button>` 是「触发器」（`model-picker-trigger`）。
       ModelPicker 通过 `<Button>` 渲染触发器，而 Button 是多根组件 —— 非 props 属性只能
       落到 Button 的根节点，也就是 `<button>` 本身，于是这个 testid **不会**出现在这层
       div 上（2026-10-19 曾误以为在 div 上，写出过"被自己拦截"的假红）。 -->
  <!-- `min-w-0`：可压缩（理由见下面按钮的注释），否则窄屏时它撑住宽度、把动作行顶成两行 -->
  <div ref="root" class="pointer-events-auto relative min-w-0" data-testid="model-picker">
    <!-- 布局口径（2026-10-24 调整，为"详情页新建任务"入口腾位）：
           · 触发器**固定一个中性上限** `max-w-[8.75rem]`（140px），而不是随文字收缩；
           · 容器给一个较小的 `basis`（84px）并允许拉伸：**行内还有空间时它长大到上限**，
             空间不够时才被压缩 —— 这就是"长名字模型（如 DeepSeek-V4.1-Flash）平时仍读得出"
             与"窄屏 + 多一个按钮也不会把行顶成两行"这两件事的唯一同时解。
         ⚠️ 旧值 `max-w-[10rem]`（160px）+ 不压缩的触发器，在 375 宽下多一枚 36px 按钮时
         **不会换行而是溢出**：模型名与右侧按钮的命中区外扩（after:-inset-1）共占 39px，
         实测"模型选择器"与"发送"重叠 7px（修法见 e2e/layout/fixture.spec.ts 的
         pickerClearance）。 -->
    <button
      type="button"
      class="flex min-h-8 min-w-0 max-w-[8.75rem] flex-1 basis-[84px] items-center gap-1.5 rounded-[var(--radius-sm)] px-2 py-1 text-xs transition-colors hover:bg-elevated focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-accent/40 disabled:cursor-not-allowed disabled:opacity-50"
      :class="open ? 'bg-elevated text-text' : 'text-muted'"
      :disabled="disabled"
      :aria-label="`模型：${currentName}，点击切换`"
      :aria-expanded="open"
      aria-haspopup="listbox"
      data-testid="model-picker-trigger"
      @click="open = !open"
      @mousedown.prevent
    >
      <!-- ⚠️「◈」与「▾」必须 `shrink-0`、只有文字 `truncate`：动作行变窄时若让
           箭头先被压没，用户就看不到"这里能展开"。文字截断有 title 兜住全名。 -->
      <span class="shrink-0">◈</span>
      <span class="min-w-0 truncate" :title="currentName">{{ currentLabel }}</span>
      <!-- 展开时箭头朝上：弹层在触发器上方，朝下会指错方向 -->
      <span class="shrink-0 text-[9px] transition-transform" :class="open ? 'rotate-180' : ''">▾</span>
    </button>

    <!-- 弹层：**Teleport 到 body** 后 fixed 定位（判据见文件头「层叠」）。
         不再是 `absolute bottom-full` —— 那会把弹层关进输入栏的层叠上下文。
         样式三处刻意为之（"看不清"的直接原因）：
           · 底色 `bg-surface`（浅色 #FFF / 深色 #14161B）而不是旧的 `bg-elevated`
             （= `surface-muted` #F0F2F5）：旧色与输入框底 `surface-subtle` #F8F9FB
             只差 8 个色阶，弹层边界在屏幕上几乎看不出来；
           · `border-border-strong` + `shadow-2xl`：浮层要"明显浮在正文之上"；
           · `divide-y`：选项之间有线，数得清有几项、点到的是哪一项。 -->
    <Teleport to="body">
      <div
        v-if="open"
        class="fixed z-[100] flex flex-col overflow-hidden rounded-[var(--radius-md)] border border-border-strong bg-surface text-text shadow-2xl"
        :style="popupStyle"
        role="dialog"
        aria-label="选择模型"
        data-testid="model-picker-popup"
      >
        <!-- 空清单：说明为什么没有可选项，而不是给一个空壳弹层 -->
        <p
          v-if="!hasCatalog"
          class="px-3 py-2.5 text-xs leading-relaxed text-text-secondary"
          data-testid="model-picker-empty"
          @mousedown.prevent
        >
          {{ modelsLoaded ? '该 Agent 不提供可选模型，沿用其默认模型。' : '正在读取可选模型…' }}
        </p>

        <template v-else>
          <div class="flex shrink-0 items-center justify-between gap-2 border-b border-border px-3 py-2">
            <span class="shrink-0 text-xs font-semibold text-text-secondary">模型</span>
            <span class="min-w-0 truncate text-xs text-text-tertiary" :title="currentName">当前：{{ currentName }}</span>
          </div>

          <!-- 移动端 ①：高度由外层 maxHeight 夹（触发器上方可用空间）；③：每项 min-h-11（44px 触控命中区）。
               ②：pb-[env(safe-area-inset-bottom)] 避让 Home 指示器（手机贴底时最后一项紧挨屏幕底边）。
               `min-h-0` + `flex-1`：这是唯一可滚的区域，滚动只发生在弹层内部，不会带走后面的正文。 -->
          <div
            role="listbox"
            class="min-h-0 flex-1 divide-y divide-border-subtle overflow-y-auto overscroll-contain pb-[env(safe-area-inset-bottom)]"
          >
            <!-- 「Agent 默认」= 清空选择，交还 agent 自己的路由 -->
            <button
              type="button"
              role="option"
              :aria-selected="model === ''"
              class="flex min-h-11 w-full items-center gap-2 px-3 py-2 text-left text-sm transition-colors hover:bg-surface-muted"
              :class="model === '' ? 'bg-accent/10 font-medium' : ''"
              data-testid="model-picker-default"
              @click="pick('')"
              @mousedown.prevent
            >
              <span class="w-3.5 shrink-0 text-accent">{{ model === '' ? '✓' : '' }}</span>
              <span class="min-w-0 flex-1 break-words">Agent 默认</span>
            </button>

            <template v-for="g in groups" :key="g.name">
              <div
                v-if="g.name"
                class="shrink-0 bg-surface-subtle px-3 py-1 text-xs font-medium text-text-secondary"
              >
                {{ g.name }}
              </div>
              <button
                v-for="m in g.options"
                :key="m.value"
                type="button"
                role="option"
                :aria-selected="m.value === model"
                class="flex min-h-11 w-full items-center gap-2 px-3 py-2 text-left transition-colors hover:bg-surface-muted"
                :class="m.value === model ? 'bg-accent/10' : ''"
                @click="pick(m.value)"
                @mousedown.prevent
              >
                <span class="w-3.5 shrink-0 text-accent">{{ m.value === model ? '✓' : '' }}</span>
                <!-- min-w-0 + break-words：模型名含 "/" 且可能很长，不能横向溢出弹层 -->
                <span class="min-w-0 flex-1">
                  <span class="block break-words text-sm">{{ m.name }}</span>
                  <span v-if="m.description" class="block break-words text-xs text-text-tertiary">{{ m.description }}</span>
                </span>
              </button>
            </template>
          </div>
        </template>
      </div>
    </Teleport>
  </div>
</template>