import { describe, expect, it } from 'vitest'
import type { ArtifactDto } from '@/types/api'
import {
  ARTIFACT_HASH_PREFIX,
  artifactPathFromHash,
  rewriteLocalArtifactLinks,
} from './markdownArtifactLinks'

const arts: ArtifactDto[] = [
  {
    path: 'proposals/index.html',
    name: 'index.html',
    kind: 'html',
    open: '/preview/t1/index.html',
  },
  {
    path: 'docs/notes.md',
    name: 'notes.md',
    kind: 'markdown',
    open: '/api/tasks/t1/file?path=docs%2Fnotes.md',
  },
  {
    path: 'web/src/Login.vue',
    name: 'Login.vue',
    kind: 'code',
    open: '/api/tasks/t1/file?path=web%2Fsrc%2FLogin.vue',
  },
]

describe('rewriteLocalArtifactLinks', () => {
  it('html 链到 preview URL；md/code 链到 pieqi-artifact hash', () => {
    const md = '见 [方案](proposals/index.html) 与 [说明](docs/notes.md) 和 [组件](web/src/Login.vue)'
    const out = rewriteLocalArtifactLinks(md, arts)
    expect(out).toContain('[方案](/preview/t1/index.html)')
    expect(out).toContain(`[说明](${ARTIFACT_HASH_PREFIX}${encodeURIComponent('docs/notes.md')})`)
    expect(out).toContain(`[组件](${ARTIFACT_HASH_PREFIX}${encodeURIComponent('web/src/Login.vue')})`)
  })

  it('围栏内路径不改写', () => {
    const md = '```\n[x](docs/notes.md)\n```'
    expect(rewriteLocalArtifactLinks(md, arts)).toBe(md)
  })

  it('图片语法不改写（留给 markdownImages）', () => {
    const md = '![shot](shot.png)'
    expect(rewriteLocalArtifactLinks(md, arts)).toBe(md)
  })

  it('唯一 basename 可匹配', () => {
    const out = rewriteLocalArtifactLinks('打开 [页](index.html)', arts)
    expect(out).toContain('/preview/t1/index.html')
  })
})

describe('artifactPathFromHash', () => {
  it('解出路径；非产物 hash 返回 null', () => {
    expect(artifactPathFromHash(`${ARTIFACT_HASH_PREFIX}docs%2Fnotes.md`)).toBe('docs/notes.md')
    expect(artifactPathFromHash('#other')).toBe(null)
  })
})
