<script setup lang="ts">
// 审批摘要块（ApprovalCard / ApprovalBanner 共用）：默认折叠 3 行，可展开全文。
//
// **折叠必须配"展开"，不能只截断。** 审批卡是批准前唯一能看到"将要发生什么"的地方；
// 只给首行就让人点批准，等于把 `rm -rf x && curl ... | sh` 藏进省略号后面 ——
// 那比"太长"严重得多。两个审批面共用一个组件，正是为了让这条不变式只写一遍。
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
</script>

<template>
  <div
    class="break-all whitespace-pre-wrap rounded border border-border/60 bg-background px-2.5 py-2 font-mono text-xs"
    :class="collapsible && !expanded ? 'line-clamp-3' : ''"
  >
    {{ text }}
  </div>
  <button
    v-if="collapsible"
    type="button"
    class="mt-1 text-[11px] text-info"
    @click="expanded = !expanded"
  >
    {{ expanded ? '收起' : `展开全文（共 ${[...text].length} 字）` }}
  </button>
</template>
