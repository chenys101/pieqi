// 格式化工具单测：标题截断 / 路径归一 / 工具参数格式化
import { describe, expect, it } from 'vitest'
import { truncateTitle, groupKey, formatToolInput, shortId } from './format'

describe('truncateTitle', () => {
  it('短文本原样返回', () => {
    expect(truncateTitle('修复登录 bug')).toBe('修复登录 bug')
  })

  it('空值兜底为空串', () => {
    expect(truncateTitle(undefined)).toBe('')
    expect(truncateTitle(null)).toBe('')
  })

  it('首个断句短句优先', () => {
    expect(truncateTitle('修复订单创建的 bug。然后重构整个模块并补充测试')).toBe('修复订单创建的 bug。…')
  })

  it('超长无断句文本截断加省略号', () => {
    const out = truncateTitle('a'.repeat(50))
    expect(out.length).toBeLessThanOrEqual(16)
    expect(out.endsWith('…')).toBe(true)
  })
})

describe('groupKey', () => {
  it('反斜杠归一为正斜杠（Windows 路径）', () => {
    expect(groupKey('G:\\ws\\erp')).toBe('g:/ws/erp')
  })

  it('重复斜杠合并 / 去尾斜杠 / 小写', () => {
    expect(groupKey('G://WS//ERP//')).toBe('g:/ws/erp')
  })

  it('空值兜底', () => {
    expect(groupKey(undefined)).toBe('')
  })

  it('大小写与分隔符混用的同一项目得到相同 key', () => {
    expect(groupKey('G:\\WS\\Erp')).toBe(groupKey('g:/ws/erp'))
  })
})

describe('formatToolInput', () => {
  it('JSON 字符串解析为 key/value 行', () => {
    expect(formatToolInput('{"file":"a.ts","line":1}')).toEqual([
      { key: 'file', value: 'a.ts' },
      { key: 'line', value: '1' },
    ])
  })

  it('对象直接展开；非 JSON 字符串原样返回', () => {
    expect(formatToolInput({ cmd: 'ls' })).toEqual([{ key: 'cmd', value: 'ls' }])
    expect(formatToolInput('plain text')).toEqual([{ key: '', value: 'plain text' }])
  })

  it('超长 value 截断到 200 字符', () => {
    const [{ value }] = formatToolInput({ data: 'x'.repeat(300) })
    expect(value.length).toBe(203)
    expect(value.endsWith('...')).toBe(true)
  })

  it('空输入返回空数组', () => {
    expect(formatToolInput(undefined)).toEqual([])
    expect(formatToolInput('')).toEqual([])
  })
})

describe('shortId', () => {
  it('取前 8 位', () => {
    expect(shortId('abcdefghijklmnop')).toBe('abcdefgh')
  })
})
