<script setup lang="ts">
// 事件随附的图片（缩略图 + 点击全屏看清楚）。
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
//     排在同一行时，前者会把正文挤成一列。高 10rem、宽度自适应、最多 44% 宽。
//  3. 缩略图**只缩小不放大**（w-auto + max-h），放大交给全屏遮罩（useImageViewer）。
//     ⚠️ 上一版是"原位内联放大"（切 max-h 到 80vh），但外层 figure 的 `max-w-[44%]`
//     已经把宽度锁死，正方图抬 max-height 后**一个像素都没变** —— 用户点了以为坏了。
//     尺寸类问题只看代码是看不出来的，改前必须实测（ui-change-verification §4）。
import { computed } from 'vue'
import type { AgentEventImage } from '@/types/event'
import { taskAttachmentURL } from '@/services/api/feedback'
import { useImageViewer } from '@/composables/useImageViewer'

const props = defineProps<{ taskId: string; images: AgentEventImage[] }>()

const { open } = useImageViewer()

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

function zoom(item: { url: string; label: string; sizeText: string }) {
  open({ src: item.url, alt: item.label, caption: item.sizeText })
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
      <!-- 按钮宽 `w-fit`：跟着图片自身的显示宽度走，而不是拉满 figure。
           拉满时点击区会落在图片右边那片空白上，看着像"点了没反应"。 -->
      <button
        type="button"
        class="block w-fit cursor-zoom-in focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-accent/40"
        :title="`查看大图 ${img.label}`"
        :aria-label="`查看大图 ${img.label}`"
        data-testid="event-image-open"
        @click="zoom(img)"
      >
        <!-- ★ 绝不让浏览器**放大**位图：解码后的像素是固定的，放大只会糊
             （2026-10-23 用户反馈"图糊了/看着像被拉过"就是这个原因：位图只有几十~一两百像素宽，
             却被 w-full 拉到容器宽度再显示）。
             所以这里是 `w-auto max-w-full` + `max-h-*`：**只缩小、不放大** ——
             小图按自身像素显示（可能很小，但清晰），大图等比缩进容器。
             不用 width/height 属性占位：那组值同时参与渲染尺寸，见上。 -->
        <img
          :src="img.url"
          :alt="img.label"
          class="block w-auto max-w-full cursor-zoom-in object-contain"
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
