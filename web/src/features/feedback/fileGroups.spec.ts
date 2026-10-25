import { describe, expect, it } from 'vitest'
import { baseNameOf, dirOf, groupByFolder } from './fileGroups'

// 文件清单的分组是**纯展示逻辑**，但它决定用户能不能在长列表里找到东西：
// 顺序不稳会"每次刷新换个位置"，根目录不置顶会让最浅的文件沉到中间。
// 所以这几条钉住的是"顺序与归并"，不是样式。

describe('groupByFolder', () => {
  it('按目录归并，根目录固定最前，其余按目录名升序', () => {
    const groups = groupByFolder([
      { path: 'web/src/a.ts' },
      { path: 'README.md' },
      { path: 'docs/adr/0017.md' },
      { path: 'web/src/b.ts' },
    ])
    expect(groups.map((g) => g.dir)).toEqual(['', 'docs/adr', 'web/src'])
    expect(groups[0].label).toBe('根目录')
    expect(groups[2].files.map((f) => f.path)).toEqual(['web/src/a.ts', 'web/src/b.ts'])
  })

  it('分组头带文件数与 +/- 合计（收起态据此判断值不值得展开）', () => {
    const [g] = groupByFolder([
      { path: 'web/src/a.ts', additions: 3, deletions: 1 },
      { path: 'web/src/b.ts', additions: 5 },
    ])
    expect(g.files).toHaveLength(2)
    expect(g.additions).toBe(8)
    expect(g.deletions).toBe(1)
  })

  it('空清单不造分组（否则界面上会多出一个空文件夹）', () => {
    expect(groupByFolder([])).toEqual([])
  })

  it('Windows 反斜杠路径与正斜杠等价（事件里的路径可能带 \\）', () => {
    const groups = groupByFolder([{ path: 'web\\src\\a.ts' }, { path: 'web/src/b.ts' }])
    expect(groups).toHaveLength(1)
    expect(groups[0].dir).toBe('web/src')
  })
})

describe('dirOf / baseNameOf', () => {
  it('切出目录与文件名，根目录为空串', () => {
    expect(dirOf('a/b/c.md')).toBe('a/b')
    expect(baseNameOf('a/b/c.md')).toBe('c.md')
    expect(dirOf('README.md')).toBe('')
    expect(baseNameOf('README.md')).toBe('README.md')
  })
})
