// highlight.ts 的单测：着色只是观感，但**拼回原文必须一字不差** ——
// 查看器的复制、搜索、行号都建立在"token 文本 == 原行"上。
import { describe, expect, it } from 'vitest'
import { langLabelOf, langOfPath, tokenizeLine, type HighlightState } from './highlight'

function fresh(): HighlightState {
  return { inBlockComment: false }
}

function kinds(line: string, lang = 'ts', state = fresh()) {
  return tokenizeLine(line, lang, state).map((t) => `${t.kind}:${t.text}`)
}

function roundTrip(line: string, lang = 'ts', state = fresh()) {
  return tokenizeLine(line, lang, state).map((t) => t.text).join('')
}

describe('tokenizeLine', () => {
  it('关键字 / 字符串 / 数字 / 函数名', () => {
    expect(kinds('const n = add(42, "x")')).toEqual([
      'kw:const',
      'plain: n = ',
      'fn:add',
      'plain:(',
      'num:42',
      'plain:, ',
      'str:"x"',
      'plain:)',
    ])
  })

  it('字符串里的 // 与 # 不被当成注释', () => {
    expect(kinds('const u = "https://a/b#c" // real', 'ts')).toEqual([
      'kw:const',
      'plain: u = ',
      'str:"https://a/b#c"',
      'plain: ',
      'cm:// real',
    ])
    expect(kinds('const x = "#fff"', 'ts').some((t) => t.startsWith('cm:'))).toBe(false)
  })

  it('# 只在支持的语言里是注释；CSS 的 #fff 保持普通文本', () => {
    expect(kinds('# 注释', 'py')).toEqual(['cm:# 注释'])
    expect(kinds('color: #fff;', 'css')).toEqual(['plain:color: #fff;'])
  })

  it('块注释跨行：状态延续到下一行', () => {
    const state = fresh()
    expect(kinds('/* 第一行', 'ts', state)).toEqual(['cm:/* 第一行'])
    expect(state.inBlockComment).toBe(true)
    expect(kinds(' * 第二行', 'ts', state)).toEqual(['cm: * 第二行'])
    expect(state.inBlockComment).toBe(true)
    expect(kinds(' */ const a = 1', 'ts', state)).toEqual([
      'cm: */',
      'plain: ',
      'kw:const',
      'plain: a = ',
      'num:1',
    ])
    expect(state.inBlockComment).toBe(false)
  })

  it('Vue 模板标签与 HTML 注释', () => {
    expect(kinds('<script setup lang="ts">', 'vue')).toEqual([
      'tag:<script',
      'plain: setup lang=',
      'str:"ts"',
      'plain:>',
    ])
    expect(kinds('<!-- 注释 -->', 'vue')).toEqual(['cm:<!-- 注释 -->'])
  })

  it('单行块注释闭合后继续着色', () => {
    expect(kinds('/* a */ const x = 1')).toEqual([
      'cm:/* a */',
      'plain: ',
      'kw:const',
      'plain: x = ',
      'num:1',
    ])
  })

  it('SQL 的 -- 注释；TS 的 -- 是普通文本', () => {
    expect(kinds('-- 注释', 'sql')).toEqual(['cm:-- 注释'])
    expect(kinds('let a = b--', 'ts')).toEqual(['kw:let', 'plain: a = b--'])
  })

  it.each([
    ['const a = 1', 'ts'],
    ['<div class="x">{{ a }}</div>', 'vue'],
    ['func main() { fmt.Println("hi") }', 'go'],
    ['def f(x): return x # c', 'py'],
    ['color: #fff; /* x */', 'css'],
    ['<!-- a --> <b>', 'vue'],
    ['a/*b*/c', 'ts'],
    ['`hello ${name}`', 'ts'],
  ])('token 拼回原文与输入一致：%s', (line, lang) => {
    expect(roundTrip(line, lang)).toBe(line)
  })
})

describe('langOfPath / langLabelOf', () => {
  it('取扩展名并映射展示名', () => {
    expect(langOfPath('web/src/a.vue')).toBe('vue')
    expect(langOfPath('internal/api/artifacts.go')).toBe('go')
    expect(langOfPath('noext')).toBe('')
    expect(langLabelOf('a.vue')).toBe('Vue')
    expect(langLabelOf('a.go')).toBe('Go')
    expect(langLabelOf('a.unknown')).toBe('UNKNOWN')
    expect(langLabelOf('noext')).toBe('只读')
  })
})
