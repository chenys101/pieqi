// highlight.ts：只读文件查看器的**行内**语法着色（纯函数、无依赖）。
//
// 为什么自己写而不是引 highlight.js / shiki：
//   · 只需要「看起来是代码」的层次，不需要语义精确（着色不参与搜索/复制/回退）；
//   · 引一个 highlighter 会给单二进制再加几十到几百 KB，而这里真正需要覆盖的
//     只有仓库里常见的 vue/ts/go/py/css/md 几种；
//   · 纯函数能单测：输入一行 + 语言 + 跨行状态，输出 token 数组，
//     组件只负责把 token 渲染成 <span>，**不走 v-html**（Agent 产出不可信）。
//
// 已知取舍（都是显示层，不影响正确性）：
//   · 不解析模板字符串跨行、不解析 Python 三引号 —— 跨行字符串按普通字符串处理；
//   · `#` 只在明确支持 # 注释的语言里当注释（CSS 的 #fff 不能被吃掉）；
//   · 关键字表是"常见并集"，不按语言精确区分（多亮一个词不是 bug）。

export type CodeTokenKind = 'plain' | 'kw' | 'str' | 'num' | 'cm' | 'tag' | 'fn'

export interface CodeToken {
  text: string
  kind: CodeTokenKind
}

/** 跨行状态：块注释（/* … *\/）是否还没闭合 */
export interface HighlightState {
  inBlockComment: boolean
}

/** 常见语言的关键字并集（含 Go / Python / Rust 的常用词） */
const KEYWORDS = new Set(
  (
    'import from export default const let var function return if else for while do switch case break continue ' +
    'new class extends implements interface type enum public private protected readonly static async await try catch finally throw ' +
    'typeof instanceof in of as is keyof void null undefined true false super this yield ' +
    'package func struct map chan go defer range select fallthrough ' +
    'def elif except with lambda pass raise global nonlocal assert del and or not None True False ' +
    'fn mut pub impl trait match use mod crate self Self where loop unsafe'
  ).split(' '),
)

/** `#` 是注释的语言（CSS 不在此列：`#fff` 是颜色，不是注释） */
const HASH_COMMENT_LANGS = new Set([
  'py', 'python', 'sh', 'bash', 'zsh', 'fish', 'ps1', 'yaml', 'yml',
  'toml', 'ini', 'cfg', 'conf', 'rb', 'ruby', 'r',
])

/** `--` 是注释的语言 */
const DASH_COMMENT_LANGS = new Set(['sql', 'lua'])

const NUMBER_RE = /^\d+(?:\.\d+)?/
const TAG_RE = /^<\/?[A-Za-z][\w:.-]*/
const WORD_RE = /^[A-Za-z_$][\w$]*/

/**
 * 把一行拆成 token。
 *
 * 调用方按行循环并复用同一个 `state`（块注释跨行必须靠它）。
 * 返回的 token 文本拼起来**必须等于原行** —— 这是复制/搜索/行号能对上的前提。
 */
export function tokenizeLine(line: string, lang: string, state: HighlightState): CodeToken[] {
  const out: CodeToken[] = []
  const push = (text: string, kind: CodeTokenKind) => {
    if (!text) return
    const last = out[out.length - 1]
    if (last && last.kind === kind) last.text += text
    else out.push({ text, kind })
  }

  const normalized = lang.toLowerCase()
  let i = 0
  while (i < line.length) {
    // 1) 块注释延续：整段都算注释，直到 `*/`
    if (state.inBlockComment) {
      const end = line.indexOf('*/', i)
      if (end === -1) {
        push(line.slice(i), 'cm')
        return out
      }
      push(line.slice(i, end + 2), 'cm')
      state.inBlockComment = false
      i = end + 2
      continue
    }

    const rest = line.slice(i)

    // 2) 注释：块 / 行 / HTML
    if (rest.startsWith('/*')) {
      const end = line.indexOf('*/', i + 2)
      if (end === -1) {
        push(line.slice(i), 'cm')
        state.inBlockComment = true
        return out
      }
      push(line.slice(i, end + 2), 'cm')
      i = end + 2
      continue
    }
    if (rest.startsWith('//')) {
      push(rest, 'cm')
      return out
    }
    if (rest.startsWith('<!--')) {
      const end = line.indexOf('-->', i + 4)
      if (end === -1) {
        push(rest, 'cm')
        return out
      }
      push(line.slice(i, end + 3), 'cm')
      i = end + 3
      continue
    }
    if (line[i] === '#' && HASH_COMMENT_LANGS.has(normalized)) {
      push(rest, 'cm')
      return out
    }
    if (rest.startsWith('--') && DASH_COMMENT_LANGS.has(normalized)) {
      push(rest, 'cm')
      return out
    }

    // 3) 字符串（引号内的 //、#、/* 都不再当注释）
    const ch = line[i]
    if (ch === '"' || ch === "'" || ch === '`') {
      let j = i + 1
      while (j < line.length) {
        if (line[j] === '\\') {
          j += 2
          continue
        }
        if (line[j] === ch) {
          j += 1
          break
        }
        j += 1
      }
      push(line.slice(i, j), 'str')
      i = j
      continue
    }

    // 4) 数字
    if (ch >= '0' && ch <= '9') {
      const m = NUMBER_RE.exec(rest)
      if (m) {
        push(m[0], 'num')
        i += m[0].length
        continue
      }
    }

    // 5) 模板标签（Vue / HTML）
    if (ch === '<') {
      const m = TAG_RE.exec(rest)
      if (m) {
        push(m[0], 'tag')
        i += m[0].length
        continue
      }
    }

    // 6) 标识符：关键字 / 函数调用 / 普通
    if (/[A-Za-z_$]/.test(ch)) {
      const m = WORD_RE.exec(rest)
      if (m) {
        const word = m[0]
        const next = line[i + word.length]
        if (KEYWORDS.has(word)) push(word, 'kw')
        else if (next === '(') push(word, 'fn')
        else push(word, 'plain')
        i += word.length
        continue
      }
    }

    push(ch, 'plain')
    i += 1
  }
  return out
}

/** 由路径取着色语言标识（与 tokenizeLine 的 lang 参数同口径） */
export function langOfPath(path: string): string {
  const name = path.split('/').pop() ?? ''
  const dot = name.lastIndexOf('.')
  return dot >= 0 ? name.slice(dot + 1).toLowerCase() : ''
}

/** 头部展示用的语言名 */
export function langLabelOf(path: string): string {
  const ext = langOfPath(path)
  const map: Record<string, string> = {
    vue: 'Vue', ts: 'TS', tsx: 'TSX', js: 'JS', jsx: 'JSX', mjs: 'JS', cjs: 'JS',
    go: 'Go', py: 'Python', rs: 'Rust', java: 'Java', kt: 'Kotlin', kts: 'Kotlin',
    c: 'C', h: 'C', cpp: 'C++', cc: 'C++', cxx: 'C++', hpp: 'C++', hh: 'C++',
    cs: 'C#', rb: 'Ruby', php: 'PHP', swift: 'Swift', scala: 'Scala', r: 'R',
    sh: 'Shell', bash: 'Shell', zsh: 'Shell', fish: 'Shell', ps1: 'PowerShell',
    bat: 'Batch', cmd: 'Batch', sql: 'SQL', graphql: 'GraphQL', gql: 'GraphQL',
    proto: 'Proto', css: 'CSS', scss: 'SCSS', sass: 'Sass', less: 'Less',
    toml: 'TOML', ini: 'INI', cfg: 'Config', conf: 'Config',
    md: 'Markdown', markdown: 'Markdown', mdown: 'Markdown', mkd: 'Markdown',
    yaml: 'YAML', yml: 'YAML', json: 'JSON', jsonc: 'JSON', json5: 'JSON',
    xml: 'XML', svg: 'SVG', txt: 'Text', text: 'Text', log: 'Log', csv: 'CSV', tsv: 'TSV',
    pdf: 'PDF', html: 'HTML', htm: 'HTML',
  }
  return map[ext] ?? (ext ? ext.toUpperCase() : '只读')
}
