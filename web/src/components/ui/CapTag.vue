<script setup lang="ts">
// 能力标记（SPEC §5.4 / §5.4.1）。
//
// **不复用状态徽章（Badge）** —— 「隧道 / API」是**权限**不是**状态**。
// 借 Badge 的语义色会把两套词汇混在一起：读的人分不清"这个点说的是它现在怎么样"
// 还是"它被允许做什么"。所以它是一个独立组件、独立类名、独立槽位（warning）。
//
// 语义是「**承载**管理员能力」，不是"拥有" —— 特权的归属是**绑定的飞书账号（人）**，
// 机器人只是转达者（见 IMPLEMENTATION-PLAN §4.2）。这句区别不是措辞问题：
// 写成"只有管理员机器人可发起"会让用户对"谁能开隧道"形成错误预期，
// 而那个预期在多机器人下会直接变成误操作。
withDefaults(
  defineProps<{
    label?: string
    /** 说明文字；默认给出「承载」语义的完整表述 */
    hint?: string
  }>(),
  {
    label: '隧道 / API',
    hint: '承载管理员能力 · 隧道与 API 特权由该机器人绑定的飞书账号行使',
  },
)
</script>

<template>
  <span
    class="cap-tag inline-flex shrink-0 items-center gap-1 rounded border border-warning/40 bg-warning/10 px-1.5 py-0.5 text-[11px] font-medium leading-4 text-warning"
    :title="hint"
  >
    <svg
      viewBox="0 0 24 24"
      class="h-3 w-3"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
      aria-hidden="true"
    >
      <path d="M13 2 3 14h7l-1 8 10-12h-7l1-8z" />
    </svg>
    {{ label }}
  </span>
</template>
