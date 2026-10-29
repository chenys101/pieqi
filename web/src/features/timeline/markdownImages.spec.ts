// markdown 正文里本地图片的改写逻辑（2026-10-24"结论中图片无法渲染"回归）。
//
// 这层最容易被"看着对"骗过去：改写逻辑本身没报错，但 marked 会因为反斜杠转义
// 连 <img> 都不生成 —— 所以断言要落到**最终 HTML**上（有没有 img、src 是什么），
// 而不是只断言字符串里出现了 taskId。
import { describe, expect, it } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import { createPinia } from 'pinia'
import MarkdownBody from './components/MarkdownBody.vue'
import { useImageViewer } from '@/composables/useImageViewer'
import {
  degradeFailedLocalImages,
  localImageTarget,
  localPathFromSrc,
  rewriteLocalImageTargets,
} from './markdownImages'
import { renderMarkdown } from './markdown'

const TASK = 'd9a32c85-c2ce-48d9-af64-b4c09e829f9a'

/** 真实形态：结论里原样写的就是 Windows 反斜杠绝对路径 */
const WIN_ABS = '![6套猫头像总览](D:\\code\\go\\pieqi\\.tmp-avatar\\final\\_contact-6up.png)'

describe('localImageTarget：哪些 src 算本地文件', () => {
  const cases: Array<[string, string | null, string]> = [
    ['D:\\code\\a.png', 'D:/code/a.png', 'Windows 反斜杠路径'],
    ['D:/code/a.png', 'D:/code/a.png', 'Windows 正斜杠路径'],
    ['c:\\tmp\\b.jpg', 'c:/tmp/b.jpg', '小写盘符'],
    ['docs/shot.png', 'docs/shot.png', '项目相对路径'],
    ['./out/a.png', './out/a.png', './ 前缀'],
    ['https://x.com/a.png', null, '远程图不动'],
    ['data:image/png;base64,AAA', null, '内联 data 不动'],
    ['blob:http://x/1', null, 'blob 不动'],
    ['/api/tasks/t/attachments/abc', null, '同源绝对路径不动'],
    ['//cdn.x/a.png', null, '协议相对不动'],
    ['', null, '空'],
  ]
  for (const [input, want, why] of cases) {
    it(`${why}：${JSON.stringify(input)} → ${JSON.stringify(want)}`, () => {
      expect(localImageTarget(input)).toBe(want)
    })
  }
})

describe('rewriteLocalImageTargets', () => {
  it('Windows 绝对路径改写成本地图片端点（否则 marked 会把它当转义吃掉）', () => {
    const out = rewriteLocalImageTargets(WIN_ABS, TASK)
    expect(out).toContain(`/api/tasks/${TASK}/local-image?path=`)
    expect(out).not.toContain('\\')
    // path 参数是 URL 编码过的（反斜杠已转正斜杠后再编码）
    const m = /local-image\?path=([^)\s]+)/.exec(out)!
    expect(decodeURIComponent(m[1]).replace(/\+/g, ' ')).toBe(
      'D:/code/go/pieqi/.tmp-avatar/final/_contact-6up.png',
    )
  })

  it('最终 HTML 真的带 src（这条才是用户看到的东西）', () => {
    const html = renderMarkdown(rewriteLocalImageTargets(WIN_ABS, TASK))
    expect(html).toContain('<img')
    expect(html).toContain(`/api/tasks/${TASK}/local-image`)
    // 不做改写时：DOMPurify 把 `D:` 当成未知协议，**整条 src 抹掉** ——
    // 页面上就是一枚"永远加载不出来"的空图（这正是线上"图片无法渲染"的形态）。
    const raw = renderMarkdown(WIN_ABS)
    expect(raw).toContain('<img')
    expect(raw).not.toContain('src=')
  })

  it('远程图 / 已同源的图原样保留（不能把外链也改成端点）', () => {
    const md = '![r](https://x.com/a.png) ![s](/api/tasks/t/attachments/abc)'
    expect(rewriteLocalImageTargets(md, TASK)).toBe(md)
  })

  it('代码围栏内的示例路径一律不动（那是 Agent 展示的源码，改了等于篡改）', () => {
    const md = ['```md', WIN_ABS, '```', '', WIN_ABS].join('\n')
    const out = rewriteLocalImageTargets(md, TASK)
    const lines = out.split('\n')
    expect(lines[1]).toBe(WIN_ABS) // 围栏内原样
    expect(lines[4]).toContain('/local-image') // 围栏外改写
  })

  it('没有 taskId 时原样返回（拼不出端点，别编坏 URL）', () => {
    expect(rewriteLocalImageTargets(WIN_ABS, '')).toBe(WIN_ABS)
  })
})

describe('localPathFromSrc：加载失败时能说清是哪个文件', () => {
  it('从端点 URL 里取回原始路径；普通图片 URL 返回 null', () => {
    expect(localPathFromSrc(`/api/tasks/${TASK}/local-image?path=D%3A%2Fcode%2Fa.png`)).toBe(
      'D:/code/a.png',
    )
    expect(localPathFromSrc('/api/tasks/t/attachments/abc')).toBeNull()
    expect(localPathFromSrc('https://x/a.png')).toBeNull()
  })
})

describe('degradeFailedLocalImages：404 时降级成可读说明而不是破图', () => {
  it('img 触发 error 后替换成带路径的一行说明', () => {
    const host = document.createElement('div')
    host.innerHTML = `<img src="/api/tasks/${TASK}/local-image?path=D%3A%2Fcode%2Fmissing.png">`
    degradeFailedLocalImages(host)
    const img = host.querySelector('img')!
    img.dispatchEvent(new Event('error'))
    expect(host.querySelector('img')).toBeNull()
    expect(host.textContent).toContain('D:/code/missing.png')
    expect(host.querySelector('.md-image-missing')).not.toBeNull()
  })

  it('非本地图片（工具图/外链）不挂降级：它们有自己的失败语义', () => {
    const host = document.createElement('div')
    host.innerHTML = '<img src="/api/tasks/t/attachments/abc">'
    degradeFailedLocalImages(host)
    host.querySelector('img')!.dispatchEvent(new Event('error'))
    expect(host.querySelector('img')).not.toBeNull()
  })
})

// ---- 端到端一档：MarkdownBody 真的把结论里的本地图渲染出来，且能点开 ----
describe('MarkdownBody：结论正文里的本地图片', () => {
  function mountBody(props: { text: string; taskId?: string }) {
    const host = document.createElement('div')
    document.body.appendChild(host)
    const app = createApp({ render: () => h(MarkdownBody, props) })
    // MarkdownBody 现在还要读 artifactsStore（本地产物链接）——
    // 不装 pinia 的话 setup 会直接抛 getActivePinia，整棵正文都渲染不出来。
    app.use(createPinia())
    app.mount(host)
    return { host, app }
  }

  it('渲染出可点击的 <img>，src 走本地图片端点（带 taskId）', async () => {
    const { host, app } = mountBody({ text: `# 交付\n\n${WIN_ABS}`, taskId: TASK })
    await nextTick()
    const img = host.querySelector('[data-testid="markdown-body"] img') as HTMLImageElement
    expect(img).not.toBeNull()
    expect(img.getAttribute('src')).toContain(`/api/tasks/${TASK}/local-image?path=`)
    app.unmount()
  })

  it('点正文里的图 → 打开全屏遮罩（与工具图共用同一个 viewer）', async () => {
    const { host, app } = mountBody({ text: `# 交付\n\n${WIN_ABS}`, taskId: TASK })
    await nextTick()
    const img = host.querySelector('[data-testid="markdown-body"] img') as HTMLImageElement
    img.click()
    await nextTick()
    expect(useImageViewer().current.value?.src).toBe(img.getAttribute('src'))
    useImageViewer().close()
    app.unmount()
  })
})

