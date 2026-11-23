import { describe, expect, it } from 'vitest'
import { canPromoteToRequirement } from './canPromoteToRequirement'

describe('canPromoteToRequirement', () => {
  it('短路径未挂需求 → true', () => {
    expect(canPromoteToRequirement({ requirementId: undefined })).toBe(true)
    expect(canPromoteToRequirement({ requirementId: '' })).toBe(true)
    expect(canPromoteToRequirement({ requirementId: '  ' })).toBe(true)
  })

  it('已挂需求 → false', () => {
    expect(canPromoteToRequirement({ requirementId: 'req-1' })).toBe(false)
  })

  it('无 task → false', () => {
    expect(canPromoteToRequirement(null)).toBe(false)
    expect(canPromoteToRequirement(undefined)).toBe(false)
  })
})
