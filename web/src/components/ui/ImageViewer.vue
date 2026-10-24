<script setup lang="ts">
// ImageViewer：全屏看图宿主（全局唯一，挂在 App.vue）。
//
// 为什么改成遮罩层而不是"原位内联放大"（上一版的做法）：
// 上一版靠 `max-h` 从 10rem 切到 80vh 来"放大"，但缩略图外面套了一层
// `max-w-[44%]` 的 figure，图片自身又是 `max-w-full` —— **宽度先被锁死**，
// 只抬 max-height 对正方图完全没有像素变化。用户点了没反应，是真正的
// "点了等于没点"（2026-10-24 反馈）。
//
// 这一版的取舍：
//  1. **点哪儿都能关**（遮罩、图片本身、右上角 ×、Esc）：上一版担心的
//     "手机上找不到关闭路径"由此解决 —— 关闭路径有四个，不依赖"点外面"这一条；
//  2. 打开期间锁 body 滚动，否则手指在遮罩上滑会带着底下时间线一起滚；
//  3. 小图**不放大**（max-w/max-h 只做上限）：位图放大只会糊，这与缩略图那条纪律同源。
import { onBeforeUnmount, onMounted, watch } from 'vue'
import { useImageViewer } from '@/composables/useImageViewer'

const { current, close } = useImageViewer()

/** Esc 关闭：桌面端唯一符合直觉的退出键；移动端靠点按 */
function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape' && current.value) {
    e.stopPropagation()
    close()
  }
}

/**
 * 锁背景滚动。直接改 body.style 而不是加 class：本组件是唯一使用者，
 * 关闭时恢复成空串即可（不引入第二个"谁在锁"的记账）。
 */
let prevOverflow = ''
watch(current, (v) => {
  if (typeof document === 'undefined') return
  if (v) {
    prevOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
  } else {
    document.body.style.overflow = prevOverflow
  }
})

onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKey)
  if (typeof document !== 'undefined') document.body.style.overflow = prevOverflow
})
</script>

<template>
  <Teleport to="body">
    <Transition
      enter-active-class="transition-opacity duration-150"
      leave-active-class="transition-opacity duration-150"
      enter-from-class="opacity-0"
      leave-to-class="opacity-0"
    >
      <div
        v-if="current"
        class="fixed inset-0 z-[80] flex flex-col items-center justify-center bg-black/85 p-3"
        role="dialog"
        aria-modal="true"
        :aria-label="current.alt || '图片预览'"
        data-testid="image-viewer"
        @click="close"
      >
        <img
          :src="current.src"
          :alt="current.alt || ''"
          class="block max-h-[86vh] max-w-full object-contain"
          data-testid="image-viewer-img"
          decoding="async"
        />
        <p
          v-if="current.alt || current.caption"
          class="mt-2 max-w-full truncate text-center text-xs text-white/80"
          data-testid="image-viewer-caption"
        >
          {{ current.alt }}<span v-if="current.caption"> · {{ current.caption }}</span>
        </p>
        <!-- × 在最上层：即便图铺满屏也有固定的退出点 -->
        <button
          type="button"
          class="absolute right-2 top-2 flex h-9 w-9 items-center justify-center rounded-full bg-black/40 text-lg leading-none text-white/90 hover:bg-black/60"
          aria-label="关闭"
          data-testid="image-viewer-close"
          @click.stop="close"
        >
          ×
        </button>
      </div>
    </Transition>
  </Teleport>
</template>
