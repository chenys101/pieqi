// 测试环境能力守卫。
//
// ⚠️ 文件名 `__probe.spec.ts` 是历史遗留 —— agent 的文件工具只能写不能删、
// 也改不了名，请人类把它重命名成 `envGuards.spec.ts`（内容无需改动）。
//
// 为什么值得有这条用例：本机 Node 26 用**实验性全局**遮蔽了 jsdom 的
// `localStorage`（运行时打印 `--localstorage-file was not provided`），
// 于是任何裸调 `localStorage.getItem(...)` 的模块在测试里一挂载就抛
// "Cannot read properties of undefined"。
//
// 这不是理论风险 —— 2026-10-16 实测过一次连锁故障：
// `useResponsive` 直接调用 `window.matchMedia`，该函数在 jsdom 下不存在，
// 于是它在 **mounted 钩子里抛错**，连带把父组件（SessionTimeline）的整棵子树
// 渲染搞崩，一次红 57 条，跨 5 个看似无关的 spec 文件。
//
// 判据：**组件不得假设浏览器全局一定存在** —— 拿不到就降级，而不是抛。
//
// 这条用例的意义是"环境变了要有人知道"：哪天 Node 修好、或升级 jsdom、
// 或显式配了 `--localstorage-file`，它会红，提醒我们回头复核各模块的
// 兜底分支是否还走在"存储不可用"这条路径上。
//
// 注：文件名 `__probe.spec.ts` 是历史遗留（agent 的文件工具只能写不能删）。
// 人类可以把它重命名成 `envGuards.spec.ts`，内容无需改动。
import { describe, expect, it } from 'vitest'
import { useResponsive } from './useResponsive'

describe('测试环境能力守卫', () => {
  it('localStorage 在本环境被 Node 实验性全局遮蔽', () => {
    const g = globalThis as unknown as Record<string, unknown>
    expect(g.localStorage).toBeUndefined()
  })

  it('sessionStorage 正常 —— 说明是 localStorage 被遮蔽，不是 jsdom 整体缺失', () => {
    const g = globalThis as unknown as Record<string, unknown>
    expect(typeof g.sessionStorage).toBe('object')
  })

  /** ★ 这条是那 57 条连锁失败的直接回归：缺 matchMedia 时必须降级，不能抛。 */
  it('matchMedia 缺失时 useResponsive 降级而不抛错', () => {
    const w = globalThis.window as unknown as Record<string, unknown>
    const saved = w.matchMedia
    try {
      delete w.matchMedia
      expect(() => useResponsive()).not.toThrow()
    } finally {
      w.matchMedia = saved
    }
  })
})
