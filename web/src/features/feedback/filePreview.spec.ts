import { describe, expect, it } from 'vitest'
import { artifactOpenLabel, artifactOpensInApp, previewKind } from './filePreview'

describe('previewKind', () => {
  it('识别 markdown / pdf / code / text', () => {
    expect(previewKind('docs/a.md')).toBe('markdown')
    expect(previewKind('x.pdf')).toBe('pdf')
    expect(previewKind('web/src/a.vue')).toBe('code')
    expect(previewKind('notes.txt')).toBe('text')
    expect(previewKind('a.json')).toBe('text')
    expect(previewKind('index.html')).toBe(null)
  })
})

describe('artifactOpenLabel / artifactOpensInApp', () => {
  it('html 打开；其余查看；html 不进应用内 viewer', () => {
    expect(artifactOpenLabel('html')).toBe('打开')
    expect(artifactOpenLabel('markdown')).toBe('查看')
    expect(artifactOpenLabel('code')).toBe('查看')
    expect(artifactOpenLabel('text')).toBe('查看')
    expect(artifactOpensInApp('html')).toBe(false)
    expect(artifactOpensInApp('markdown')).toBe(true)
    expect(artifactOpensInApp('code')).toBe(true)
  })
})
