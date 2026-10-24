// markdown 正文里的图片：本地路径改写 + 加载失败降级。
//
// 背景（2026-10-24 用户反馈"结论中图片无法渲染"）：Agent 常把磁盘上的图直接写进结论：
//
//     ![6套猫头像总览](D:\code\go\pieqi\.tmp-avatar\final\_contact-6up.png)
//
// 这条 markdown 在浏览器里**注定是坏的**，而且是两层坏（两层都实测过）：
//   ① **marked 按转义规则改写路径**：`\.` 里的点是标点、转义符被吃掉，`.tmp-avatar`
//      变成 `tmp-avatar`；反斜杠还会被百分号编码成 `%5C`。也就是说光标点所在的那段
//      src 已经**不是原路径**了（实测：`D:%5Ccode%5Cgo%5Cpieqi.tmp-avatar%5Cfinal_...png`）；
//   ② 即便路径没被改坏（写成正斜杠 `D:/code/...`），DOMPurify 也会把 `D:` 当成
//      **未知协议**而抹掉整个 src 属性 —— 最终 DOM 里是一枚 `<img>` 没有 src，
//      页面上什么都不显示（用户看到的"结论中图片无法渲染"就是这个）。
//      顺带一提：它也不会被当成同源相对路径去请求，所以连 404 都不会有。
//
// 所以修法必须在**送进 marked 之前**动手：把这类 dest 换成后端端点
// （/api/tasks/:id/local-image?path=...）。这样 marked 与 DOMPurify 看到的都是
// 一个干净的、同源的 URL，两层都不会动它。
//
// 纪律：只在**代码围栏之外**改写。围栏里的示例命令/配置原样保留 —— 改动它等于篡改
// Agent 展示的源码。
import { taskLocalImageURL } from '@/services/api/feedback'

/** 三个反引号或三个波浪号开头的行 = 围栏边界 */
const FENCE_RE = /^\s{0,3}(```|~~~)/

/** `![alt](dest)` —— 只处理图片，普通链接的本地路径另有语义（下载/打开文件），不在本次范围 */
const IMAGE_RE = /(!\[[^\]\n]*\]\()([^)\n]*)(\))/g

/**
 * 把正文里"指向本地文件"的图片改写成本地图片端点 URL。
 *
 * `taskId` 为空时原样返回：没有任务上下文就拼不出端点，此时保持原状比编一个坏 URL 好。
 */
export function rewriteLocalImageTargets(md: string, taskId: string): string {
  if (!taskId || !md.includes('![')) return md

  const out: string[] = []
  let fence: string | null = null
  for (const line of md.split('\n')) {
    const fenceHit = FENCE_RE.exec(line)
    if (fenceHit) {
      // 开/闭围栏都原样输出：` ``` ` 与 `~~~` 各自只能关闭自己
      if (!fence) fence = fenceHit[1]
      else if (fenceHit[1] === fence) fence = null
      out.push(line)
      continue
    }
    if (fence) {
      out.push(line)
      continue
    }
    out.push(
      line.replace(IMAGE_RE, (whole, head: string, dest: string, tail: string) => {
        const target = localImageTarget(dest)
        return target === null ? whole : `${head}${taskLocalImageURL(taskId, target)}${tail}`
      }),
    )
  }
  return out.join('\n')
}

/**
 * 解析 markdown 图片的 dest，返回"本地文件路径"；不是本地文件时返回 null。
 *
 * 判定顺序（顺序有讲究，不能调换）：
 *  1. 远程/内联地址（http(s): / data: / blob: / //host / 同源绝对路径 /api/...）→ null；
 *  2. 其余都算本地：Windows 绝对路径（`D:\` `D:/`）、Unix 绝对路径（`/root/a.png`）、
 *     以及相对路径（`./out/a.png` `docs/a.png`）—— 后两类在浏览器里同样是 SPA 兜底页。
 */
export function localImageTarget(dest: string): string | null {
  let d = dest.trim()
  if (!d) return null
  // markdown 允许用 <...> 包住含空格的 dest
  if (d.startsWith('<') && d.endsWith('>')) d = d.slice(1, -1).trim()
  // 去掉可选 title（`path "title"` 形态）：只取第一段
  d = d.split(/\s+/)[0]
  if (!d) return null

  // 远程 / 内联 / 同源绝对路径：交给浏览器自己处理
  if (/^(?:[a-z][a-z0-9+.-]*:|\/\/|\/)/i.test(d)) {
    // 但 `D:\...` `C:/...` 是盘符，不是协议 —— 单独捞回来
    if (!/^[a-z]:[\\/]/i.test(d)) return null
  }
  // Windows 路径统一成反斜杠无关的形态：后端两种都认，这里只做"反斜杠→正斜杠"
  return d.replace(/\\/g, '/')
}

/** 从改写后的 URL 里取回原始本地路径（加载失败时用来告诉用户"是哪个文件没读到"） */
export function localPathFromSrc(src: string): string | null {
  try {
    const u = new URL(src, 'http://localhost')
    if (!u.pathname.endsWith('/local-image')) return null
    return u.searchParams.get('path')
  } catch {
    return null
  }
}

/**
 * 给渲染出来的图片挂上"加载失败"降级。
 *
 * 为什么非要有：这层失败**天生是静默的** —— 后端 404（文件没生成/被清理）时浏览器只画
 * 一枚破图图标，用户看不出是"路径写错了"还是"文件没了"。这里把它换成一行可读的说明。
 *
 * 用 `{ once: true }`：每次正文重渲染都会生成新的 <img> 节点，监听不会累积。
 */
export function degradeFailedLocalImages(root: HTMLElement): void {
  const imgs = root.querySelectorAll('img')
  for (const img of imgs) {
    const src = img.getAttribute('src') ?? ''
    const path = localPathFromSrc(src)
    if (!path) continue
    img.addEventListener(
      'error',
      () => {
        const note = document.createElement('span')
        note.className = 'md-image-missing'
        note.textContent = `🖼 本地图片未能加载：${path}`
        img.replaceWith(note)
      },
      { once: true },
    )
  }
}
