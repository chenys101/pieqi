// filePreview 文件预览的类型判定与产物打开文案。
// TurnCard / Baseline / 时间线产物条据此决定「打开」vs「查看」、以及 viewer 渲染器。

export type PreviewableKind = 'markdown' | 'pdf' | 'code' | 'text'

/** 由文件路径判定是否可应用内预览。不可预览（html 走 URL、未知）返回 null。 */
export function previewKind(path: string): PreviewableKind | null {
  const ext = path.split('.').pop()?.toLowerCase() ?? ''
  if (['md', 'markdown', 'mdown', 'mkd'].includes(ext)) return 'markdown'
  if (ext === 'pdf') return 'pdf'
  if (
    [
      'go',
      'ts',
      'tsx',
      'js',
      'jsx',
      'mjs',
      'cjs',
      'vue',
      'svelte',
      'py',
      'rs',
      'java',
      'kt',
      'kts',
      'c',
      'h',
      'cpp',
      'cc',
      'cxx',
      'hpp',
      'hh',
      'cs',
      'rb',
      'php',
      'swift',
      'scala',
      'r',
      'sh',
      'bash',
      'zsh',
      'fish',
      'ps1',
      'bat',
      'cmd',
      'sql',
      'graphql',
      'gql',
      'proto',
      'css',
      'scss',
      'sass',
      'less',
      'toml',
      'ini',
      'cfg',
      'conf',
    ].includes(ext)
  ) {
    return 'code'
  }
  if (['txt', 'text', 'log', 'csv', 'tsv', 'yaml', 'yml', 'json', 'jsonc', 'json5', 'xml', 'svg'].includes(ext)) {
    return 'text'
  }
  return null
}

/**
 * 产物入口文案。
 *
 * - html →「打开」（新标签 URL，真页面）
 * - image →「查看」（新标签）
 * - markdown / code / text / pdf →「查看」（应用内只读 sheet；pdf 可内嵌）
 * - 未知 → null
 */
export function artifactOpenLabel(kind: string): string | null {
  if (kind === 'html') return '打开'
  if (kind === 'image') return '查看'
  if (kind === 'markdown' || kind === 'code' || kind === 'text' || kind === 'pdf') return '查看'
  return null
}

/** html 走外链；其余可应用内查看（含 image/pdf 也可新标签，由调用方决定） */
export function artifactOpensInApp(kind: string): boolean {
  return kind === 'markdown' || kind === 'code' || kind === 'text' || kind === 'pdf'
}
