<script setup lang="ts">
// TurnCard：Feedback 的单个 Turn 卡片。
// 折叠态 = 一行摘要；展开态 = 文件变更列表（每项再展开懒加载 diff）。
// 回退按钮：恢复到「Turn N 开始之前」（时间线事件永不删除，仅改文件）。
//
// 2026-10 起两处语义变化：
//   1. **目录分组默认全展开**（不再"默认收起"）：用户要的是"点进变更就看到文件"；
//      收起由面板顶部的「全部收起」一键完成，而不是每个目录先点一次。
//   2. **展开与否由面板受控**（`expanded` + `toggle`）：否则「全部收起」管不到
//      各个 TurnCard 的局部状态，会出现"面板说收了、卡片还开着"。
// 筛选（`filter`）命中时强制展开，并且只渲染命中的目录/文件 —— 见 visibleGroups。
import { computed, ref, watch } from 'vue'
import type { ArtifactDto, FileChangeDto, TurnInfoDto } from '@/types/api'
import Button from '@/components/ui/Button.vue'
import DiffView from './DiffView.vue'
import { previewKind } from '@/utils/filePreview'
import { baseNameOf, groupByFolder } from '@/utils/fileGroups'
import { useArtifactsStore } from '@/stores/artifacts'

const props = withDefaults(
  defineProps<{
    taskId: string
    turn: TurnInfoDto
    /** 该 Turn 是否已有磁盘快照（checkpoint） */
    checkpointed: boolean
    /** 是否允许回退（Agent 执行中禁止，静止边界原则） */
    canRewind: boolean
    /** Timeline「查看本轮变更」联动高亮（AC-R3-06）：描边（展开由面板负责） */
    highlighted?: boolean
    /**
     * 产物索引（path → artifact），用于给 html / 图片提供「打开」入口。
     * 缺省 = 不给入口（不猜 URL，见 FeedbackPanel.openLinkOf）。
     */
    artifacts?: Map<string, ArtifactDto>
    /** 受控展开：面板的「全部收起」要能连 Turn 一起收 */
    expanded?: boolean
    /** 当前筛选词（空 = 不筛）；命中时强制展开并只显示命中的目录/文件 */
    filter?: string
    /** 面板的批量开合信号：seq 变化时按 open 重置所有目录分组 */
    groupReset?: { seq: number; open: boolean }
  }>(),
  {
    highlighted: false,
    expanded: false,
    filter: '',
    groupReset: () => ({ seq: 0, open: true }),
  },
)

const emit = defineEmits<{
  rewind: [turn: number]
  /** P2：文件级回退（仅恢复单个文件到本轮开始之前） */
  rewindFile: [turn: number, path: string]
  /** 受控展开：点卡片头 */
  toggle: []
}>()

const artifactsStore = useArtifactsStore()

/** 展开的文件路径（null = 无） */
const openPath = ref<string | null>(null)
/** 已收起的目录（默认空 = 全展开） */
const closedDirs = ref(new Set<string>())

const q = computed(() => props.filter.trim().toLowerCase())

/** 操作类型 → 徽章样式与人读文案 */
const opMeta: Record<FileChangeDto['operation'], { label: string; cls: string }> = {
  create: { label: '新建', cls: 'text-success border-success/40' },
  modify: { label: '修改', cls: 'text-accent border-accent/40' },
  delete: { label: '删除', cls: 'text-error border-error/40' },
  rename: { label: '重命名', cls: 'text-warning border-warning/40' },
}

/**
 * 本轮文件按目录分组，**默认全展开**；只有一个目录时不套分组头
 * （分组的价值是收掉长列表，一组还能收起等于多点一次才看到内容）。
 */
const groups = computed(() => {
  const gs = groupByFolder(props.turn.changes ?? [])
  const grouped = gs.length > 1
  return gs.map((g) => ({
    ...g,
    grouped,
    rows: g.files.map((fc) => ({
      fc,
      name: grouped ? baseNameOf(fc.path) : fc.path,
      open: openLinkOf(fc.path),
      canView: !!previewKind(fc.path),
    })),
  }))
})

/**
 * 筛选：目录命中 → 该目录下文件全留；只有文件命中 → 只留命中的行。
 * 目录/文件都不命中的分组整个不渲染（Turn 本身也不渲染，见根节点 v-if）。
 */
const visibleGroups = computed(() => {
  if (!q.value) return groups.value
  return groups.value
    .map((g) => {
      const dirMatch = g.dir.toLowerCase().includes(q.value)
      const rows = dirMatch ? g.rows : g.rows.filter((r) => r.fc.path.toLowerCase().includes(q.value))
      return { ...g, rows, visible: dirMatch || rows.length > 0 }
    })
    .filter((g) => g.visible)
})

/** 筛选命中时强制展开：否则"筛出来了却看不到" */
const isExpanded = computed(() => props.expanded || (!!q.value && visibleGroups.value.length > 0))

function isDirOpen(dir: string): boolean {
  if (q.value) return true
  return !closedDirs.value.has(dir)
}

function toggleDir(dir: string): void {
  const next = new Set(closedDirs.value)
  if (next.has(dir)) next.delete(dir)
  else next.add(dir)
  closedDirs.value = next
}

/** 面板「全部收起 / 全部展开」：按信号重置本卡所有目录 */
watch(
  () => props.groupReset.seq,
  () => {
    closedDirs.value = props.groupReset.open ? new Set() : new Set(groups.value.map((g) => g.dir))
  },
)

function toggleFile(path: string) {
  openPath.value = openPath.value === path ? null : path
}

/** html / 图片的「打开」入口；md/code/text/pdf 走应用内查看层 */
function openLinkOf(path: string): { href: string; label: string } | null {
  const a = props.artifacts?.get(path)
  if (!a) return null
  if (a.kind === 'html') return { href: a.open, label: '打开' }
  if (a.kind === 'image') return { href: a.open, label: '查看' }
  return null
}

function viewFile(path: string): void {
  artifactsStore.openPath(props.taskId, path)
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
  <div
    v-if="!q || visibleGroups.length"
    :data-turn="turn.turn"
    class="rounded-lg border bg-surface/60"
    :class="highlighted ? 'border-accent ring-2 ring-accent/30' : 'border-border/60'"
  >
    <!-- 折叠摘要行：Turn 号 + prompt + 统计 -->
    <button
      class="flex w-full items-center gap-2 px-3 py-2 text-left text-xs hover:text-text"
      :aria-expanded="isExpanded"
      @click="emit('toggle')"
    >
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
      <span class="shrink-0 text-muted transition-transform" :class="isExpanded ? '' : '-rotate-90'">▾</span>
    </button>

    <div v-if="isExpanded" class="border-t border-border/40">
      <!-- 文件变更列表：按目录分组、默认全展开（多目录才有分组头） -->
      <div v-if="!turn.changes?.length" class="px-3 py-2 text-xs text-muted">本轮无文件变更</div>
      <!-- 每个目录一个包裹层：sticky 组头只在自己的目录范围内粘住，
           否则同级的多个组头会全部叠在滚动区顶部 -->
      <div v-for="g in visibleGroups" :key="g.dir || '.'" :data-dir="g.dir">
        <button
          v-if="g.grouped"
          type="button"
          class="sticky top-0 z-[1] flex min-h-11 w-full items-center gap-2 border-b border-border/40 bg-surface px-3 py-2 text-left text-[11.5px] hover:bg-surface-muted"
          :aria-expanded="isDirOpen(g.dir)"
          :title="g.dir ? `展开/收起 ${g.dir}` : '展开/收起仓库根目录下的文件'"
          data-dir-toggle
          @click="toggleDir(g.dir)"
        >
          <span class="shrink-0 text-muted transition-transform" :class="isDirOpen(g.dir) ? '' : '-rotate-90'">▾</span>
          <span class="min-w-0 flex-1 truncate font-mono" :class="isDirOpen(g.dir) ? 'text-text' : 'text-muted'">{{ g.label }}</span>
          <span class="shrink-0 text-muted">{{ g.files.length }} 个</span>
          <span v-if="g.additions" class="shrink-0 font-mono text-success">+{{ g.additions }}</span>
          <span v-if="g.deletions" class="shrink-0 font-mono text-error">-{{ g.deletions }}</span>
        </button>

        <div v-if="!g.grouped || isDirOpen(g.dir)">
          <div v-for="row in g.rows" :key="row.fc.path" class="border-b border-border/30 last:border-b-0" :data-file-row="row.fc.path">
            <div class="flex items-center gap-1.5">
              <button
                class="flex min-w-0 flex-1 items-center gap-2 px-3 py-1.5 text-left text-xs hover:bg-surface-muted"
                @click="toggleFile(row.fc.path)"
              >
                <span class="shrink-0 rounded border px-1 text-[10px]" :class="opMeta[row.fc.operation].cls">
                  {{ opMeta[row.fc.operation].label }}
                </span>
                <span class="min-w-0 flex-1 truncate font-mono text-muted" :title="row.fc.path">{{ row.name }}</span>
                <span v-if="row.fc.additions" class="shrink-0 font-mono text-success">+{{ row.fc.additions }}</span>
                <span v-if="row.fc.deletions" class="shrink-0 font-mono text-error">-{{ row.fc.deletions }}</span>
                <span class="shrink-0 text-muted transition-transform" :class="openPath === row.fc.path ? '' : '-rotate-90'">▾</span>
              </button>
              <!-- 产物「新标签打开」：html 是真页面，图片浏览器直接显示 -->
              <a
                v-if="row.open"
                :href="row.open.href"
                target="_blank"
                rel="noopener"
                class="shrink-0 px-2.5 py-1 text-xs text-accent hover:underline"
                :title="`新标签打开 ${row.fc.path}`"
              >{{ row.open.label }}</a>
              <!-- 应用内只读查看（md/code/text/pdf）：不再在 420px 面板里内联铺代码 -->
              <button
                v-if="row.canView"
                type="button"
                class="shrink-0 px-2.5 py-1 text-xs text-text-tertiary hover:text-accent"
                :title="`查看 ${row.fc.path}`"
                :data-view="row.fc.path"
                @click="viewFile(row.fc.path)"
              >查看</button>
            </div>
            <!-- 单文件 diff（懒加载：展开时才请求） -->
            <DiffView v-if="openPath === row.fc.path" :task-id="taskId" :path="row.fc.path" :turn="row.fc.turn" />
            <!-- P2：文件级回退入口（展开审视单文件 diff 的时刻提供，不影响其他文件） -->
            <div v-if="pendingFile === row.fc.path" class="flex flex-wrap items-center gap-2 px-3 pb-1.5">
              <span class="min-w-0 flex-1 text-[11px] text-warning">
                仅回退此文件到 Turn #{{ turn.turn }} 之前？其他文件不受影响。
              </span>
              <Button variant="ghost" size="sm" @click="pendingFile = null">取消</Button>
              <Button variant="danger" size="sm" @click="confirmRewindFile">确认回退</Button>
            </div>
            <div v-else-if="openPath === row.fc.path" class="flex justify-end px-3 pb-1.5">
              <Button
                variant="ghost"
                size="sm"
                :disabled="!canRewind"
                :title="canRewind ? '仅回退此文件到本轮开始之前' : 'Agent 执行中，暂不可回退'"
                @click="askRewindFile(row.fc.path)"
              >↩ 仅回退此文件</Button>
            </div>
          </div>
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
