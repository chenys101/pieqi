<script setup lang="ts">
// TurnCard：Feedback 总览里的单个 Turn 卡片（p0-design.md §5.1）。
// 折叠态 = 一行摘要；展开态 = 文件变更列表（每项再展开懒加载 diff）。
// 回退按钮：恢复到「Turn N 开始之前」（时间线事件永不删除，仅改文件）。
import { ref } from 'vue'
import type { FileChangeDto, TurnInfoDto } from '@/types/api'
import Button from '@/components/ui/Button.vue'
import DiffView from './DiffView.vue'
import FilePreview from './FilePreview.vue'
import { previewKind } from '../filePreview'

const props = defineProps<{
  taskId: string
  turn: TurnInfoDto
  /** 该 Turn 是否已有磁盘快照（checkpoint） */
  checkpointed: boolean
  /** 是否允许回退（Agent 执行中禁止，静止边界原则） */
  canRewind: boolean
}>()

const emit = defineEmits<{
  rewind: [turn: number]
  /** P2：文件级回退（仅恢复单个文件到本轮开始之前） */
  rewindFile: [turn: number, path: string]
}>()

const expanded = ref(false)
/** 展开的文件路径（null = 无） */
const openPath = ref<string | null>(null)
/** 展开预览的文件路径（null = 无） */
const previewPath = ref<string | null>(null)

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

function togglePreview(path: string) {
  previewPath.value = previewPath.value === path ? null : path
}

/** 整轮回退的待确认态（null = 未发起） */
const pendingRewind = ref(false)
/** 单文件回退的待确认路径（null = 未发起） */
const pendingFile = ref<string | null>(null)

// 回退是**不可逆**的操作，必须确认 —— 但用内联确认条而不是原生 confirm()：
// confirm() 是模态的，会盖住用户正看着的那个文件 / 那一行 diff，
// 而这恰恰是需要他确认"是不是它"的唯一依据（SPEC 已明令禁止原生 confirm）。

function askRewind() {
  pendingRewind.value = true
}

function confirmRewind() {
  pendingRewind.value = false
  emit('rewind', props.turn.turn)
}

/** P2 文件级回退：只恢复该文件到本轮开始之前，其他文件不动（p2-design.md §7） */
function askRewindFile(path: string) {
  pendingFile.value = path
}

function confirmRewindFile() {
  const path = pendingFile.value
  pendingFile.value = null
  if (path) emit('rewindFile', props.turn.turn, path)
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
        <div class="flex items-center gap-2">
          <button
            class="flex min-w-0 flex-1 items-center gap-2 px-3 py-1.5 text-left text-xs hover:bg-elevated"
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
          <!-- markdown/pdf 预览入口 -->
          <button
            v-if="previewKind(fc.path)"
            class="shrink-0 px-2.5 py-1 text-xs"
            :class="previewPath === fc.path ? 'text-accent' : 'text-muted hover:text-text'"
            @click="togglePreview(fc.path)"
          >预览</button>
        </div>
        <!-- 单文件 diff（懒加载：展开时才请求） -->
        <DiffView v-if="openPath === fc.path" :task-id="taskId" :path="fc.path" :turn="fc.turn" />
        <!-- 文件预览（markdown/pdf） -->
        <FilePreview v-if="previewPath === fc.path" :task-id="taskId" :path="fc.path" />
        <!-- P2：文件级回退入口（展开审视单文件 diff 的时刻提供，不影响其他文件） -->
        <div v-if="pendingFile === fc.path" class="flex flex-wrap items-center gap-2 px-3 pb-1.5">
          <span class="min-w-0 flex-1 text-[11px] text-warning">
            仅回退此文件到 Turn #{{ turn.turn }} 之前？其他文件不受影响。
          </span>
          <Button variant="ghost" size="sm" @click="pendingFile = null">取消</Button>
          <Button variant="danger" size="sm" @click="confirmRewindFile">确认回退</Button>
        </div>
        <div v-else-if="openPath === fc.path" class="flex justify-end px-3 pb-1.5">
          <Button
            variant="ghost"
            size="sm"
            :disabled="!canRewind"
            :title="canRewind ? '仅回退此文件到本轮开始之前' : 'Agent 执行中，暂不可回退'"
            @click="askRewindFile(fc.path)"
          >↩ 仅回退此文件</Button>
        </div>
      </div>

      <!-- 回退：恢复到本轮开始之前 -->
      <div v-if="pendingRewind" class="flex flex-wrap items-center gap-2 px-3 py-2">
        <span class="min-w-0 flex-1 text-[11px] text-warning">
          回退到 Turn #{{ turn.turn }} 之前？此后各轮改动将被恢复/删除，时间线不受影响。
        </span>
        <Button variant="ghost" size="sm" @click="pendingRewind = false">取消</Button>
        <Button variant="danger" size="sm" @click="confirmRewind">确认回退</Button>
      </div>
      <div v-else class="flex items-center justify-between px-3 py-2">
        <span class="text-[11px] text-muted">时间线保留，仅恢复文件</span>
        <Button variant="danger" size="sm" :disabled="!canRewind" :title="canRewind ? '' : 'Agent 执行中，暂不可回退'" @click="askRewind">
          回退到此轮之前
        </Button>
      </div>
    </div>
  </div>
</template>
