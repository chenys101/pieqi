// Agent 文本输出的 markdown 渲染（需求 3：输出结果支持 markdown 渲染）。
//
// **安全边界与 FilePreview 同源**：Agent 产物是不可信输入，marked 只负责把
// markdown 变成 HTML，消毒一律交给 DOMPurify。两条纪律：
//   1. 必须 sanitize —— v-html 直出等于把 agent 的任意 HTML 注入本页
//      （本页能驱动 agent 执行命令，等于自提权）；
//   2. **流式友好**：每次文本增量都重新渲染。markdown 的半截语法（未闭合的
//      ``` 代码围栏、** 强调）在流式过程中会短暂解析成"平文本 + 残缺标签"，
//      这是渲染粒度决定的固有现象，代价可接受；反过来缓存 token 流式追加
//      会把渲染逻辑复杂化，且末尾块永远要重算。
import { marked } from 'marked'
import DOMPurify from 'dompurify'

/**
 * markdown → 消毒后的 HTML。
 *
 * `breaks: true`（GFM 换行 = `<br>`）是**为对话流拿的主意**：agent 输出的正文
 * 绝大多数是"一句话一段"的松散文本，按严格 CommonMark 单换行会被折成一行，
 * 在聊天气泡里读起来是糊的。代码块不受影响（围栏内是原文）。
 */
export function renderMarkdown(text: string): string {
  const raw = marked.parse(text, { async: false, gfm: true, breaks: true }) as string
  return DOMPurify.sanitize(raw)
}

/**
 * 是否是"值得按 markdown 渲染"的文本 —— 纯文本肉眼可读性更好，
 * 而把一句话按 markdown 过一遍只会多出 `<p>` 包裹与块级外边距。
 *
 * 判据保守：只有出现**块级结构**（标题 / 列表 / 代码围栏 / 引用 / 表格 / 分割线）
 * 或**粗体链接等行内标记**时才渲染。宁可漏判成纯文本，也不要误判把
 * 正常对话里随手写的 `a * b` / `file_name` 渲染成强调与斜体。
 */
const MARKDOWN_HINT =
  /(^|\n)\s{0,3}(#{1,6}\s|[-*+]\s|\d+[.)]\s|>\s|```|~~~|\|.*\|| {4}\S)|(\*\*|__|`[^`\n]+`|\[[^\]\n]+\]\([^)\s]+\))/

export function looksLikeMarkdown(text: string): boolean {
  return MARKDOWN_HINT.test(text)
}
