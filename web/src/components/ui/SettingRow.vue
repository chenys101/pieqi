<script setup lang="ts">
// 设置项的一行：左「标签 + 说明」，右「控件」。设置页与所有分组卡的基本构件。
//
// locked 表达的是「这项不由你决定」，不是「暂时禁用」——
// 所以它背景下沉、标题降级，而不压透明度：
// 压透明度会把原因文字一起淡化，而原因恰恰是最该读清的部分。
defineProps<{
  /** 主标签（也可用 #label 插槽放徽章等） */
  label?: string
  /** 说明文字；留空则只渲染标签行 */
  desc?: string
  /** 锁定行：不可配置项 */
  locked?: boolean
  /**
   * 控件较宽时置 true（如分段控件）：窄屏改为纵向堆叠。
   * 不这么做的话，左侧说明会被宽控件挤成一条几字宽的窄柱 ——
   * 说明文字是这行的主要内容，不该为控件让位到不可读。
   */
  stack?: boolean
}>()
</script>

<template>
  <div
    class="setting-row flex items-center gap-[14px] px-[14px] py-[11px]"
    :class="[
      locked ? 'bg-surface-inset' : '',
      stack ? 'max-md:flex-col max-md:items-stretch max-md:gap-2' : '',
    ]"
  >
    <div class="min-w-0 flex-1">
      <div
        class="flex items-center gap-1.5 text-[13px]"
        :class="locked ? 'text-text-secondary' : 'text-text'"
      >
        <slot name="label">{{ label }}</slot>
      </div>
      <div
        v-if="desc || $slots.desc"
        class="mt-[3px] text-[11.5px] leading-[1.55] text-text-tertiary"
      >
        <slot name="desc">{{ desc }}</slot>
      </div>
    </div>
    <div class="flex shrink-0 items-center gap-1.5" :class="stack ? 'max-md:justify-start' : ''">
      <slot />
    </div>
  </div>
</template>

<style scoped>
/* 行间分隔线交给相邻兄弟规则：调用方只管按顺序排，不必操心"第一个不加线"。
   组头已自带 border-bottom，所以第一行不能再要上边框 —— 那会叠成 2px。 */
.setting-row + .setting-row {
  border-top: 1px solid rgb(var(--color-border-subtle));
}
</style>
