// filePreview 文件预览的类型判定（markdown / pdf）。
// TurnCard 与 Baseline 文件行据此决定是否显示「预览」入口；FilePreview 据此分派渲染器。

export type PreviewableKind = 'markdown' | 'pdf'

/** 由文件路径判定是否可预览，及预览类型。不可预览返回 null。 */
export function previewKind(path: string): PreviewableKind | null {
  const ext = path.split('.').pop()?.toLowerCase() ?? ''
  if (['md', 'markdown', 'mdown', 'mkd'].includes(ext)) return 'markdown'
  if (ext === 'pdf') return 'pdf'
  return null
}

/**
 * 产物里哪些类型值得「新标签打开」，以及按钮文案。
 *
 * md / pdf 已有内联预览（FilePreview），再给一个"打开"只会把人带离面板。
 * html 与图片**没有**内联渲染器：html 要的是真页面（样式/脚本/同目录资源都生效，
 * 方案对比页能点、能切），图片浏览器直接显示 —— 这两类才需要新标签。
 *
 * 返回 null = 不给入口（没有产物登记，或该类型已由内联预览覆盖）。
 */
export function artifactOpenLabel(kind: string): string | null {
  if (kind === 'html') return '打开'
  if (kind === 'image') return '查看'
  return null
}
