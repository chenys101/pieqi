<script setup lang="ts">
// 事件随附的图片（缩略图 + 点击放大）。
//
// 当前唯一来源是读图类工具（read_image）的**结果**：dsh 把它放在 ACP
// ToolCall/ToolCallUpdate.Content 的 image 块里，后端落盘后事件上只留元数据
//（见 model.TaskImage 与 core/attachments.go）。图片本体按 hash 从
// /api/tasks/:id/attachments/:hash 取。
//
// 三条刻意的取舍：
//  1. **只在 stored=true 时才到得了这里**（归一化层已滤，见 normalizer.adaptEventImages）：
//     历史事件里"记了元数据但没存字节"的图不会渲染成破图。
//  2. 缩略图**按固定高度横排**而不是按原图尺寸：一张 1000×1000 与一张 200×80
//     排在同一行时，前者会把正文挤成一列。高 120px、宽度自适应、最多 44% 宽。
//  3. 放大是**原位内联展开**（再点收起），不弹整屏遮罩：手机上没有"点外面关闭"
//     之外的退出路径，而时间线本身就在滚 —— 遮罩会让人以为页面卡住了。
import { computed, ref } from 'vue'
import type { AgentEventImage } from '@/types/event'
import { taskAttachmentURL } from '@/services/api/feedback'

const props = defineProps<{ taskId: string; images: AgentEventImage[] }>()

/** 正在放大的那张（hash）；空 = 全是缩略图 */
const zoomed = ref<string>('')

const items = computed(() =>
  props.images.map((img) => ({
    ...img,
    url: taskAttachmentURL(props.taskId, img.hash),
    label: img.name || '图片',
    sizeText: formatBytes(img.bytes),
  })),
)

/** 字节数 → 人读大小（与 ImagePicker 的口径一致，一行的量级不必精确） */
function formatBytes(n: number): string {
  if (!n) return ''
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}

function toggle(hash: string) {
  zoomed.value = zoomed.value === hash ? '' : hash
}
</script>

<template>
  <div class="flex flex-wrap items-start gap-2" data-testid="event-images">
    <figure
      v-for="img in items"
      :key="img.hash"
      class="min-w-0 max-w-[44%] overflow-hidden rounded-[var(--radius-sm)] border border-border bg-surface-subtle"
      data-testid="event-image"
    >
      <button
        type="button"
        class="block w-full cursor-zoom-in focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-accent/40"
        :title="zoomed === img.hash ? '收起图片' : `点击放大 ${img.label}`"
        :aria-label="zoomed === img.hash ? `收起 ${img.label}` : `放大 ${img.label}`"
        :aria-expanded="zoomed === img.hash"
        @click="toggle(img.hash)"
      >
        <!-- width/height 写死成 120：加载完成前后占位一致，时间线不会因为图到了而跳一下 -->
        <img
          :src="img.url"
          :alt="img.label"
          class="block w-full object-contain transition-[max-height] duration-200"
          :class="zoomed === img.hash ? 'max-h-[70vh] cursor-zoom-out' : 'max-h-[7.5rem] cursor-zoom-in'"
          width="160"
          height="120"
          loading="lazy"
          decoding="async"
        />
      </button>
      <figcaption
        v-if="img.name || img.sizeText"
        class="flex items-center gap-1 px-1.5 py-0.5 text-[11px] text-text-tertiary"
      >
        <span class="min-w-0 truncate" :title="img.label">{{ img.label }}</span>
        <span v-if="img.sizeText" class="ml-auto shrink-0 font-mono">{{ img.sizeText }}</span>
      </figcaption>
    </figure>
  </div>
</template>
