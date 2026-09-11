<script setup lang="ts">
// 设置分组卡（SPEC §5.4）。
//
// 单列 760px 分组卡，**不做左侧二级导航**：组数少、内容短，
// 二级导航会为几个锚点多占一列宽度，而用户仍然要滚动。
//
// `why` 是组头右侧那半句「这组凭什么排在这」——它是排序依据的自白，
// 属于可选信息，窄屏直接隐藏（不是折行，折行会把组头撑成两行）。
defineProps<{
  title: string
  why?: string
}>()
</script>

<template>
  <section
    class="overflow-hidden rounded-lg border border-border-subtle bg-surface shadow-sm dark:border-border"
  >
    <div class="flex items-center gap-2 border-b border-border-subtle bg-surface-subtle px-3.5 py-2.5">
      <h3 class="text-[13px] font-[650] tracking-[-.005em] text-text">{{ title }}</h3>
      <span v-if="why" class="ml-auto text-[11.5px] text-text-tertiary max-md:hidden">{{ why }}</span>
    </div>
    <slot />
  </section>
</template>
