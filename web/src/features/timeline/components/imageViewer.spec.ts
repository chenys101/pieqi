// 图片放大（全屏遮罩）回归：2026-10-24 用户反馈"图片无法放大"。
//
// 上一版的 bug 是**尺寸链**问题（figure 的 max-w-[44%] 把宽度锁死，只抬 max-height
// 一个像素都不变），jsdom 量不出尺寸，所以这里只能钉**行为契约**：
//   点击缩略图 → 遮罩打开且 src 正确 → 点图/点 × /按 Esc 都能关。
// 真机上"确实变大了多少"由 web/scripts/self-test.ps1 量（见 ui-change-verification）。
//
// ⚠️ 每个用例必须自己 unmount：Teleport 把节点挪到 body，若先清 body 再让 Vue 更新，
// 它会对着已经消失的目标节点 insertBefore → "Cannot read properties of null"。
import { describe, expect, it, beforeEach, afterEach } from 'vitest'
import { createApp, h, nextTick, type App, type Component } from 'vue'
import ImageViewer from '@/components/ui/ImageViewer.vue'
import EventImages from '@/features/timeline/components/EventImages.vue'
import { useImageViewer } from '@/composables/useImageViewer'

const HASH = 'a'.repeat(64)

let mounted: App[] = []

function mount(comp: Component, props: Record<string, unknown> = {}) {
  const host = document.createElement('div')
  document.body.appendChild(host)
  const app = createApp({ render: () => h(comp, props) })
  app.mount(host)
  mounted.push(app)
  return { host, app }
}

const { current, open, close } = useImageViewer()

/**
 * 等离场动画走完再断言 DOM。
 *
 * `<Transition>` 的 leave 要等 transitionend/超时才会真的摘掉节点；jsdom 没有样式表，
 * 时长靠 Vue 的兜底检测（实测 <60ms 就摘掉了）。这里留足余量，免得断言跑在动画中间。
 */
function flushLeave() {
  return new Promise((r) => setTimeout(r, 120))
}

beforeEach(() => close())
afterEach(() => {
  for (const app of mounted) app.unmount()
  mounted = []
  close()
  document.body.innerHTML = ''
  document.body.style.overflow = ''
})

describe('ImageViewer：全屏看图', () => {
  it('open() 后遮罩里出现同一张图，并锁住背景滚动', async () => {
    mount(ImageViewer)
    expect(document.querySelector('[data-testid="image-viewer"]')).toBeNull()

    open({ src: '/api/tasks/t/attachments/abc', alt: 'A.png', caption: '600 KB' })
    await nextTick()

    const overlay = document.querySelector('[data-testid="image-viewer"]')!
    expect(overlay).not.toBeNull()
    const img = overlay.querySelector('[data-testid="image-viewer-img"]') as HTMLImageElement
    expect(img.getAttribute('src')).toBe('/api/tasks/t/attachments/abc')
    expect(document.querySelector('[data-testid="image-viewer-caption"]')!.textContent).toContain(
      'A.png',
    )
    // 遮罩打开时背景不能跟着滚（否则手指一滑时间线就跑了）
    expect(document.body.style.overflow).toBe('hidden')
  })

  it('点遮罩关闭，并恢复背景滚动', async () => {
    mount(ImageViewer)
    open({ src: '/x.png' })
    await nextTick()
    ;(document.querySelector('[data-testid="image-viewer"]') as HTMLElement).click()
    await flushLeave()
    expect(document.querySelector('[data-testid="image-viewer"]')).toBeNull()
    expect(document.body.style.overflow).toBe('')
  })

  it('点右上角 × 关闭（移动端不依赖"点外面"这一条路径）', async () => {
    mount(ImageViewer)
    open({ src: '/x.png' })
    await nextTick()
    ;(document.querySelector('[data-testid="image-viewer-close"]') as HTMLElement).click()
    await flushLeave()
    expect(document.querySelector('[data-testid="image-viewer"]')).toBeNull()
  })

  it('Esc 关闭', async () => {
    mount(ImageViewer)
    open({ src: '/x.png' })
    await nextTick()
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await flushLeave()
    expect(document.querySelector('[data-testid="image-viewer"]')).toBeNull()
  })
})

describe('EventImages：点击缩略图打开的是全屏图而不是"原地抬 max-height"', () => {
  it('点按钮 → 打开遮罩，且 src 指向该任务的附件端点', async () => {
    const { host } = mount(EventImages, {
      taskId: 't1',
      images: [{ mimeType: 'image/png', bytes: 614400, hash: HASH, name: '总览.png' }],
    })
    await nextTick()

    const btn = host.querySelector('[data-testid="event-image-open"]') as HTMLElement
    expect(btn).not.toBeNull()
    btn.click()
    await nextTick()

    expect(current.value).toMatchObject({
      src: `/api/tasks/t1/attachments/${HASH}`,
      alt: '总览.png',
      caption: '600 KB',
    })
  })
})
