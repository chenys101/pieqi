<script setup lang="ts">
// 审批摘要块（ApprovalCard / ApprovalBanner 共用）：默认折叠 3 行，可展开全文。
//
// **折叠必须配"展开"，不能只截断。** 审批卡是批准前唯一能看到"将要发生什么"的地方；
// 只给首行就让人点批准，等于把 `rm -rf x && curl ... | sh` 藏进省略号后面 ——
// 那比"太长"严重得多。两个审批面共用一个组件，正是为了让这条不变式只写一遍。
//
// **展开还必须能收起**（详情页体验问题修复）：早期实现把「收起」按钮放在内容**下方**，
// 而审批摘要展开后可能极长（实测有几千字符的命令 JSON），按钮被推到可视区之外 ——
// 用户展开后被困住，只能刷新页面。而"内容越长越想收起"恰恰是最需要它的时刻。
// 故展开态把按钮**移到顶部**，并给内容块**限高 + 内部滚动**，避免一条长命令
// 把下方的批准/拒绝/输入框整段顶出屏幕（手机上尤其致命）。
import { computed, ref } from 'vue'

const props = defineProps<{ text: string }>()

const expanded = ref(false)

/**
 * 短文本不给展开按钮（多一个控件是噪音）。
 *
 * 阈值按**字符数 / 是否含换行**判定，不做 DOM 测高：观测式判断会让"展开按钮出不出现"
 * 依赖渲染时机，而"该展开却没有按钮"恰好等于悄悄盲批 —— 宁可长一点还给按钮。
 */
const collapsible = computed(() => props.text.includes('\n') || [...props.text].length > 120)

/** 展开态的字符数上限说明：让用户知道"还有多少没看"，而不是无声地给一大坨 */
const charCount = computed(() => [...props.text].length)
</script>

<template>
  <!-- 展开态：收起按钮在**顶部**（内容长了也够得着） -->
  <button
    v-if="collapsible && expanded"
    type="button"
    class="mb-1 text-[11px] text-info hover:underline"
    data-testid="summary-collapse"
    @click="expanded = false"
  >
    ▲ 收起（共 {{ charCount }} 字）
  </button>

  <!-- 限高 + 内部滚动：展开也不把下方的批准/拒绝/输入框顶出可视区。
       40vh 是"手机上仍能同时看到操作按钮"的量级；min-h 避免短内容时抖动。 -->
  <div
    class="break-all whitespace-pre-wrap rounded border border-border/60 bg-background px-2.5 py-2 font-mono text-xs"
    :class="[
      collapsible && !expanded ? 'line-clamp-3' : '',
      expanded ? 'max-h-[40vh] overflow-y-auto overscroll-contain' : '',
    ]"
  >
    {{ text }}
  </div>

  <!-- 折叠态：展开按钮在下方（视线顺着内容往下走，是"看更多"的自然方向） -->
  <button
    v-if="collapsible && !expanded"
    type="button"
    class="mt-1 text-[11px] text-info hover:underline"
    data-testid="summary-expand"
    @click="expanded = true"
  >
    展开全文（共 {{ charCount }} 字）
  </button>
</template>
