<script setup lang="ts">
// TurnRail（详情页 UI 优化 · 需求 2）：时间线左侧的 Turn 快速跳转泡泡列。
//
// 仅由父组件在「≥2 个 Turn」时渲染（1 个 Turn / 平铺兜底完全不出现）；
// 移动端（<768px）由自身 `hidden md:flex` 隐藏，避免挤占窄屏。
// 泡泡是 <button>：点击 emit jump(turn)，由父组件负责平滑滚动并同步高亮。
import type { TurnRailItem } from '../groupTurns'

defineProps<{
  /** 可跳转的 Turn 列表（turn > 0），顺序即展示顺序 */
  turns: TurnRailItem[]
  /** 当前所在 Turn（滚动时由父组件跟踪）；null = 尚未确定 */
  activeTurn: number | null
}>()

const emit = defineEmits<{ jump: [turn: number] }>()
</script>

<template>
  <nav
    class="hidden w-10 shrink-0 flex-col items-center justify-center gap-1 py-4 md:flex"
    data-testid="turn-rail"
    aria-label="Turn 快速跳转"
  >
    <!-- 内层单独滚动：Turn 很多时只滚泡泡列，不撑破外层高度 -->
    <div class="flex max-h-full flex-col items-center gap-1 overflow-y-auto">
      <button
        v-for="t in turns"
        :key="t.turn"
        type="button"
        class="grid h-6 w-6 shrink-0 place-items-center rounded-full border font-mono text-[11px] transition-colors"
        :class="
          t.turn === activeTurn
            ? 'border-accent bg-accent text-surface'
            : 'border-border bg-surface text-text-tertiary hover:border-accent hover:text-accent'
        "
        :data-testid="`turn-rail-${t.turn}`"
        :title="`Turn #${t.turn}：${t.text || '（无输入）'}`"
        :aria-current="t.turn === activeTurn ? 'true' : undefined"
        @click="emit('jump', t.turn)"
      >
        {{ t.turn }}
      </button>
    </div>
  </nav>
</template>
