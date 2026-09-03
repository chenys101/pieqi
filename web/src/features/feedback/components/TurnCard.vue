<script setup lang="ts">
// TurnCard：Feedback 总览里的单个 Turn 卡片（p0-design.md §5.1）。
// 折叠态 = 一行摘要；展开态 = 文件变更列表（每项再展开懒加载 diff）。
// 回退按钮：恢复到「Turn N 开始之前」（时间线事件永不删除，仅改文件）。
import { ref } from 'vue'
import type { FileChangeDto, TurnInfoDto } from '@/types/api'
import Button from '@/components/ui/Button.vue'
import DiffView from './DiffView.vue'

const props = defineProps<{
  taskId: string
  turn: TurnInfoDto
  /** 该 Turn 是否已有磁盘快照（checkpoint） */
  checkpointed: boolean
  /** 是否允许回退（Agent 执行中禁止，静止边界原则） */
  canRewind: boolean
}>()

const emit = defineEmits<{ rewind: [turn: number] }>()

const expanded = ref(false)
/** 展开的文件路径（null = 无） */
const openPath = ref<string | null>(null)

/** 操作类型 → 徽章样式与人读文案 */
const opMeta: Record<FileChangeDto['operation'], { label: string; cls: string }> = {
  create: { label: '新建', cls: 'text-success border-success/40' },
  modify: { label: '修改', cls: 'text-accent border-accent/40' },
  delete: { label: '删除', cls: 'text-error border-error/40' },
  rename: { label: '重命名', cls: 'text-warning border-warning/40' },
}

function toggleFile(path: string) {
  openPath.value = openPath.value === path ? null : path
}

function onRewind() {
  const n = props.turn.turn
  if (!confirm(`回退到 Turn #${n} 开始之前？此后各轮的代码改动将被恢复/删除（时间线不受影响）。`)) return
  emit('rewind', n)
}
</script>

<template>
  <div class="rounded-lg border border-border/60 bg-surface/60">
    <!-- 折叠摘要行：Turn 号 + prompt + 统计 -->
    <button class="flex w-full items-center gap-2 px-3 py-2 text-left text-xs hover:text-text" @click="expanded = !expanded">
      <span class="shrink-0 font-mono font-semibold">Turn #{{ turn.turn }}</span>
      <span class="min-w-0 flex-1 truncate text-muted">{{ turn.user_prompt || '（无输入）' }}</span>
      <span class="shrink-0 font-mono">
        <span class="text-success">+{{ turn.summary.additions }}</span>
        <span class="text-error ml-1">-{{ turn.summary.deletions }}</span>
      </span>
      <span
        v-if="checkpointed"
        class="shrink-0 rounded border border-border px-1 text-[10px] text-muted"
        title="该轮已有快照，可回退"
      >快照</span>
      <span class="shrink-0 text-muted transition-transform" :class="expanded ? '' : '-rotate-90'">▾</span>
    </button>

    <div v-if="expanded" class="border-t border-border/40">
      <!-- 文件变更列表 -->
      <div v-if="!turn.changes?.length" class="px-3 py-2 text-xs text-muted">本轮无文件变更</div>
      <div v-for="fc in turn.changes" :key="fc.path" class="border-b border-border/30 last:border-b-0">
        <button
          class="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs hover:bg-elevated"
          @click="toggleFile(fc.path)"
        >
          <span class="shrink-0 rounded border px-1 text-[10px]" :class="opMeta[fc.operation].cls">
            {{ opMeta[fc.operation].label }}
          </span>
          <span class="min-w-0 flex-1 truncate font-mono text-muted" :title="fc.path">{{ fc.path }}</span>
          <span class="shrink-0 font-mono">
            <span v-if="fc.additions || fc.deletions" class="text-success">+{{ fc.additions ?? 0 }}</span>
            <span v-if="fc.additions || fc.deletions" class="ml-1 text-error">-{{ fc.deletions ?? 0 }}</span>
          </span>
          <span class="shrink-0 text-muted transition-transform" :class="openPath === fc.path ? '' : '-rotate-90'">▾</span>
        </button>
        <!-- 单文件 diff（懒加载：展开时才请求） -->
        <DiffView v-if="openPath === fc.path" :task-id="taskId" :path="fc.path" :turn="fc.turn" />
      </div>

      <!-- 回退：恢复到本轮开始之前 -->
      <div class="flex items-center justify-between px-3 py-2">
        <span class="text-[11px] text-muted">时间线保留，仅恢复文件</span>
        <Button variant="danger" size="sm" :disabled="!canRewind" :title="canRewind ? '' : 'Agent 执行中，暂不可回退'" @click="onRewind">
          回退到此轮之前
        </Button>
      </div>
    </div>
  </div>
</template>
