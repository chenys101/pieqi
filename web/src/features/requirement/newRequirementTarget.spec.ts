import { describe, expect, it } from 'vitest'
import { newRequirementTarget } from './newRequirementTarget'

describe('newRequirementTarget（ADR-0021 双入口约定）', () => {
  it('有 project_path 时带 query.project', () => {
    expect(newRequirementTarget('D:\\code\\go\\pieqi')).toEqual({
      path: '/requirements/new',
      query: { project: 'D:\\code\\go\\pieqi' },
    })
    expect(newRequirementTarget('D:/code/go/pieqi')).toEqual({
      path: '/requirements/new',
      query: { project: 'D:/code/go/pieqi' },
    })
  })

  it('空 / 空白 / null / undefined 时省略 query（仍可进新建页）', () => {
    expect(newRequirementTarget('')).toBe('/requirements/new')
    expect(newRequirementTarget('   ')).toBe('/requirements/new')
    expect(newRequirementTarget(null)).toBe('/requirements/new')
    expect(newRequirementTarget(undefined)).toBe('/requirements/new')
  })
})
