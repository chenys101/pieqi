// markdown 正文里的本地产物链接：在送进 marked 之前改写，点开走产物通道。
//
// 对标 markdownImages.ts：围栏外的 `[label](dest)` 若指向本地产物路径，
// html/image → 改成服务端算好的 open URL；md/code/text/pdf → `#pieqi-artifact=<path>`，
// 由 MarkdownBody 点击委托打开只读 viewer（不做编辑）。
import type { ArtifactDto } from '@/types/api'
import { artifactOpensInApp } from '@/features/feedback/filePreview'
import { localImageTarget } from './markdownImages'

const FENCE_RE = /^\s{0,3}(```|~~~)/
/** 普通链接（非图片）：`![` 开头的留给 markdownImages */
const LINK_RE = /\[([^\]]*)\]\(([^)]*)\)/g

export const ARTIFACT_HASH_PREFIX = '#pieqi-artifact='

export function rewriteLocalArtifactLinks(
  md: string,
  artifacts: readonly ArtifactDto[],
): string {
  if (!md || !artifacts.length || !md.includes('](')) return md

  const byPath = new Map<string, ArtifactDto>()
  const byBase = new Map<string, ArtifactDto[]>()
  for (const a of artifacts) {
    const norm = a.path.replace(/\\/g, '/')
    byPath.set(norm, a)
    const base = norm.split('/').pop()!
    const list = byBase.get(base) ?? []
    list.push(a)
    byBase.set(base, list)
  }
  for (const [base, list] of byBase) {
    if (list.length === 1 && !byPath.has(base)) byPath.set(base, list[0])
  }

  const out: string[] = []
  let fence: string | null = null
  for (const line of md.split('\n')) {
    const fenceHit = FENCE_RE.exec(line)
    if (fenceHit) {
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
      line.replace(LINK_RE, (whole, label: string, dest: string, offset: number, src: string) => {
        // 跳过图片语法 `![alt](dest)`：前一个字符是 `!`
        if (offset > 0 && src[offset - 1] === '!') return whole
        const local = localImageTarget(dest)
        if (!local) return whole
        const a = resolveArtifact(local, byPath)
        if (!a) return whole
        if (a.kind === 'html' || a.kind === 'image' || !artifactOpensInApp(a.kind)) {
          return `[${label}](${a.open})`
        }
        return `[${label}](${ARTIFACT_HASH_PREFIX}${encodeURIComponent(a.path)})`
      }),
    )
  }
  return out.join('\n')
}

function resolveArtifact(local: string, byPath: Map<string, ArtifactDto>): ArtifactDto | undefined {
  const norm = local.replace(/\\/g, '/')
  if (byPath.has(norm)) return byPath.get(norm)
  for (const [p, a] of byPath) {
    if (p.includes('/') && (norm.endsWith('/' + p) || norm.endsWith(p))) return a
  }
  const base = norm.split('/').pop()!
  return byPath.get(base)
}

/** 从 hash href 解出产物路径；不是产物链返回 null */
export function artifactPathFromHash(href: string): string | null {
  if (!href.startsWith(ARTIFACT_HASH_PREFIX)) return null
  try {
    return decodeURIComponent(href.slice(ARTIFACT_HASH_PREFIX.length))
  } catch {
    return null
  }
}
