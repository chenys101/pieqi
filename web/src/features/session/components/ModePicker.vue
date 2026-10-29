<script setup lang="ts">
// ModePicker：会话 ACP 运行模式选择器（形态对齐 ModelPicker：贴底 Teleport 弹层）。
//
// 清单来自 Task.modes（session/new 协商），不是全局 agent 探测。
// value = ModeOption.id（不透明 SessionModeId），原样回传。
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import type { ModeOptionDto } from '@/types/api'

const props = withDefaults(
  defineProps<{
    mode?: string
    modes?: ModeOptionDto[]
    disabled?: boolean
  }>(),
  { mode: '', modes: () => [], disabled: false },
)

const emit = defineEmits<{ 'update:mode': [value: string] }>()

const open = ref(false)
const root = ref<HTMLElement | null>(null)
const catalog = computed(() => props.modes ?? [])
const hasCatalog = computed(() => catalog.value.length > 0)

const currentName = computed(() => {
  if (props.mode) {
    return catalog.value.find((m) => m.id === props.mode)?.name ?? props.mode
  }
  // 空 = 尚未选定：展示清单默认项名，避免触发器一直显示空白「模式」
  // （新建页会把 sessionMode 默认写成首项；会话页在 SyncModes 前也可能短暂为空）。
  if (catalog.value.length) {
    const preferred = catalog.value.find((m) => m.id === 'agent') ?? catalog.value[0]
    return preferred.name || preferred.id || '模式'
  }
  return '模式'
})

function pick(id: string) {
  emit('update:mode', id)
  open.value = false
}

const GAP = 6
const EDGE = 8
const POPUP_MAX = 280
const WIDE_WIDTH = 280
const FLOOR = 96
const VIEW_MARGIN = 8

type Placement = { mobile: boolean; left: number; width: number; bottom: number; maxHeight: number }
const placement = ref<Placement | null>(null)

function measure() {
  const el = root.value?.querySelector('button')
  if (!el) return
  const r = el.getBoundingClientRect()
  if (!r.width && !r.height) return
  const vw = window.innerWidth
  const vh = window.innerHeight
  const mobile = vw < 768
  const above = r.top - GAP
  const room = Math.max(0, above - VIEW_MARGIN)
  placement.value = {
    mobile,
    left: mobile ? EDGE : Math.min(Math.max(EDGE, r.left), Math.max(EDGE, vw - EDGE - WIDE_WIDTH)),
    width: mobile ? vw - EDGE * 2 : Math.min(WIDE_WIDTH, vw - EDGE * 2),
    bottom: vh - above,
    maxHeight: Math.max(0, Math.min(POPUP_MAX, room, Math.round(vh * 0.6))),
  }
  if (placement.value.maxHeight < FLOOR) {
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
  if (root.value?.contains(t)) return
  if ((t as Element)?.closest?.('[data-testid="mode-picker-popup"]')) return
  open.value = false
}
function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape' && open.value) open.value = false
}
function onReflow() {
  if (open.value) measure()
}

watch(open, (v) => {
  if (v) {
    measure()
    nextTick(measure)
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
</script>

<template>
  <div ref="root" class="pointer-events-auto relative min-w-0" data-testid="mode-picker">
    <button
      type="button"
      class="flex min-h-8 min-w-0 max-w-[7.5rem] flex-1 basis-[72px] items-center gap-1.5 rounded-[var(--radius-sm)] px-2 py-1 text-xs transition-colors hover:bg-elevated focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-accent/40 disabled:cursor-not-allowed disabled:opacity-50"
      :class="open ? 'bg-elevated text-text' : 'text-muted'"
      :disabled="disabled || !hasCatalog"
      :aria-label="`模式：${currentName}，点击切换`"
      :aria-expanded="open"
      aria-haspopup="listbox"
      data-testid="mode-picker-trigger"
      @click="open = !open"
      @mousedown.prevent
    >
      <span class="shrink-0">◎</span>
      <span class="min-w-0 truncate" :title="currentName">{{ currentName }}</span>
      <span class="shrink-0 text-muted">▾</span>
    </button>

    <Teleport to="body">
      <div
        v-if="open && hasCatalog"
        class="fixed z-[80] flex flex-col overflow-hidden rounded-lg border border-border bg-surface shadow-lg"
        :style="popupStyle"
        role="listbox"
        data-testid="mode-picker-popup"
        @mousedown.prevent
      >
        <div class="border-b border-border/60 px-3 py-2 text-[11px] text-muted">
          当前：{{ currentName }}
        </div>
        <ul class="min-h-0 flex-1 overflow-y-auto py-1" style="padding-bottom: env(safe-area-inset-bottom, 0)">
          <li v-for="m in catalog" :key="m.id">
            <button
              type="button"
              class="flex min-h-11 w-full flex-col items-start gap-0.5 px-3 py-2 text-left text-sm hover:bg-elevated"
              :class="m.id === mode ? 'bg-elevated text-accent' : 'text-text'"
              role="option"
              :aria-selected="m.id === mode"
              @click="pick(m.id)"
            >
              <span class="font-medium">{{ m.name || m.id }}</span>
              <span v-if="m.description" class="text-xs text-muted">{{ m.description }}</span>
            </button>
          </li>
        </ul>
      </div>
    </Teleport>
  </div>
</template>
