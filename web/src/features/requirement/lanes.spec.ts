import { describe, expect, it } from 'vitest'
import { isLongPrompt, promptLenLabel, roleToLane } from './lanes'

describe('roleToLane', () => {
  it('maps aliases', () => {
    expect(roleToLane('frontend')).toBe('fe')
    expect(roleToLane('fe')).toBe('fe')
    expect(roleToLane('backend')).toBe('be')
    expect(roleToLane('be')).toBe('be')
    expect(roleToLane('design')).toBe('design')
    expect(roleToLane('test')).toBe('test')
    expect(roleToLane('review')).toBe('other')
    expect(roleToLane('')).toBe('design')
  })
})

describe('prompt helpers', () => {
  it('detects long prompt by chars or lines', () => {
    expect(isLongPrompt('short')).toBe(false)
    expect(isLongPrompt('x'.repeat(80))).toBe(true)
    expect(isLongPrompt('a\nb\nc')).toBe(true)
  })

  it('formats length label', () => {
    expect(promptLenLabel('abc')).toBe('3 字')
    expect(promptLenLabel('a\nb')).toBe('3 字 · 2 行')
  })
})
