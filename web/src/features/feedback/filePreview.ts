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
