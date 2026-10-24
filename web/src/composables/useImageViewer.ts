// useImageViewer：全局「点开一张图看清楚」的状态（单例）。
//
// 为什么是全局单例而不是每个组件自己管：时间线里的图和结论正文（v-html 出来的
// markdown）是两套完全不同的渲染路径，但用户要的是同一个动作。共用一份状态意味着
// 只挂一个宿主（components/ui/ImageViewer.vue，挂在 App.vue），两边只管调 open()。
//
// 状态**刻意不放进 pinia store**：它是一次性的 UI 瞬态（谁点开了哪张图），
// 没有跨页面共享价值，也不需要 devtools 追踪；模块级 ref 已经能在所有组件间共享。
import { readonly, ref } from 'vue'

export interface ViewerImage {
  /** 图片地址（同源 URL 或 data:/blob:） */
  src: string
  /** 无障碍文案 / 图注 */
  alt?: string
  /** 图注下方那行（尺寸、文件名等可核对信息） */
  caption?: string
}

const current = ref<ViewerImage | null>(null)

/** 打开某张图（已打开时切换成新的一张） */
function open(img: ViewerImage): void {
  if (!img.src) return
  current.value = img
}

function close(): void {
  current.value = null
}

export function useImageViewer() {
  return { current: readonly(current), open, close }
}
