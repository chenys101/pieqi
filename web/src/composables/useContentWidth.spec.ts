// 正文列宽度：**判据**验证（纯函数，jsdom 足够）。
//
// ⚠️ 这里刻意只测"决策"，不测"表现"：宽度是否真的落到像素上属于几何，
// jsdom 量不出来（rect 全为 0），必须真机 —— 见 AGENTS.md 的 UI 验收纪律。
// 本文件守的是四件事：
//   1. **默认 = 铺满可用宽度**（不是某个定值）—— 2026-10-16 真机验收后的修正；
//   2. 边界的**契约**（下限/上限/坏值兜底）；
//   3. 持久化往返，含"存储被写脏"的兜底；
//   4. **容量夹取不得污染用户选择** —— 最容易写错、且写错了很难发现的一条。
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  MAX_CONTENT_WIDTH,
  MIN_CONTENT_WIDTH,
  __resetContentWidthForTest,
  clampContentWidth,
  resetContentWidth,
  setContentWidth,
  setContentWidthBudget,
  useContentWidth,
} from './useContentWidth'

const KEY = 'pieqi.content-width'
const TOUCHED_KEY = 'pieqi.content-width.touched'

/**
 * 本环境（Node 26 + vitest/jsdom）下 `localStorage` 是 **undefined**
 * —— jsdom 提供了它，但被 Node 自己的实验性全局遮蔽了
 * （运行时会打印 `localStorage is not available because --localstorage-file was not provided`）。
 * `sessionStorage` 正常。
 *
 * ⚠️ 这不是本模块的问题：`useContentWidth` 对读写**都**做了 try/catch，
 * 存储不可用时应当静默降级（"只在本次会话生效"）。所以这里做两件事：
 *   1. 装一个内存版 localStorage，让"持久化"这类判据**可测**；
 *   2. 单独留一条用例断言"存储完全不可用时也不炸" —— 那才是生产环境
 *      （隐私模式 / 存储被禁）真正会走的路径。
 */
function installMemoryStorage() {
  const map = new Map<string, string>()
  const storage = {
    getItem: (k: string) => (map.has(k) ? map.get(k)! : null),
    setItem: (k: string, v: string) => void map.set(k, String(v)),
    removeItem: (k: string) => void map.delete(k),
    clear: () => void map.clear(),
    key: (i: number) => [...map.keys()][i] ?? null,
    get length() {
      return map.size
    },
  }
  Object.defineProperty(globalThis, 'localStorage', { value: storage, configurable: true, writable: true })
  return storage
}

beforeEach(() => {
  installMemoryStorage()
  __resetContentWidthForTest()
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('clampContentWidth：边界契约', () => {
  it('区间内原样（含取整）', () => {
    expect(clampContentWidth(1000)).toBe(1000)
    expect(clampContentWidth(1000.6)).toBe(1001)
  })

  it('夹取到下限 / 上限', () => {
    expect(clampContentWidth(100)).toBe(MIN_CONTENT_WIDTH)
    expect(clampContentWidth(99999)).toBe(MAX_CONTENT_WIDTH)
    expect(clampContentWidth(MIN_CONTENT_WIDTH)).toBe(MIN_CONTENT_WIDTH)
    expect(clampContentWidth(MAX_CONTENT_WIDTH)).toBe(MAX_CONTENT_WIDTH)
  })

  it('非有限数退回下限，而不是让 NaN 泄漏出去', () => {
    expect(clampContentWidth(NaN)).toBe(MIN_CONTENT_WIDTH)
    expect(clampContentWidth(Infinity)).toBe(MIN_CONTENT_WIDTH)
    expect(clampContentWidth(-Infinity)).toBe(MIN_CONTENT_WIDTH)
  })

  it('下限低于上限，且下限没有离谱到不可用', () => {
    expect(MIN_CONTENT_WIDTH).toBeLessThan(MAX_CONTENT_WIDTH)
    expect(MIN_CONTENT_WIDTH).toBeGreaterThanOrEqual(480)
  })
})

describe('★ 默认态 = 铺满可用宽度（不是定值）', () => {
  /**
   * 这条是本文件最重要的回归之一。
   *
   * 前一个版本把默认值定成 768（= 原来的 `max-w-3xl`），理由是"升级无跳变"。
   * 但用户报的症状恰恰是"正文太窄、右边空一大片"：1888px 视口下时间线可用
   * 宽度 1546px、内容只占 768px ⇒ 右侧空 778px（50%），默认态与投诉的那张图
   * 几乎一样。所以默认必须是**铺满**（`chosen === null` ⇒ 取可用宽度）。
   */
  it('没设过时 width 为 null（= 铺满），不是 768', () => {
    expect(useContentWidth().width.value).toBeNull()
  })

  it('未测量时 effectiveWidth 为 null（= 不写 max-width，交给 CSS 铺满）', () => {
    expect(useContentWidth().effectiveWidth.value).toBeNull()
  })

  it('测量到可用宽度后，生效宽度 = 可用宽度（铺满）', () => {
    setContentWidthBudget(1546)
    expect(useContentWidth().effectiveWidth.value).toBe(1546)
  })

  it('用户显式选过的值优先（铺满只对"没选过"的人生效）', () => {
    setContentWidth(768)
    setContentWidthBudget(1546)
    expect(useContentWidth().effectiveWidth.value).toBe(768)
  })
})

describe('setContentWidth：夹取 + 持久化', () => {
  it('写入后持久化到 localStorage', () => {
    setContentWidth(1100)
    expect(localStorage.getItem(KEY)).toBe('1100')
  })

  it('越界值先夹取再落盘（盘上不留越界值）', () => {
    setContentWidth(99999)
    expect(localStorage.getItem(KEY)).toBe(String(MAX_CONTENT_WIDTH))
  })

  it('同值重复设置不重复写盘', () => {
    setContentWidth(1000)
    const spy = vi.spyOn(localStorage, 'setItem')
    setContentWidth(1000)
    expect(spy).not.toHaveBeenCalled()
  })

  it('存储写失败（配额满）不影响本次生效', () => {
    vi.spyOn(localStorage, 'setItem').mockImplementation(() => {
      throw new Error('quota')
    })
    expect(() => setContentWidth(1200)).not.toThrow()
    expect(useContentWidth().width.value).toBe(1200)
  })

  /**
   * 生产环境真正会走的路径：隐私模式 / 存储被禁 ⇒ `localStorage` 读写都抛。
   * 早期版本只在 `setItem` 上做了 try/catch，`getItem` 是裸调用 ——
   * 那种环境下一挂载就炸。这条用例钉住"读写两侧都必须兜住"。
   */
  it('localStorage 完全不可用（连读都抛）时模块仍能加载且可写', async () => {
    vi.resetModules()
    Object.defineProperty(globalThis, 'localStorage', {
      configurable: true,
      get() {
        throw new Error('storage disabled')
      },
    })
    const mod = await import('./useContentWidth')
    // 取不到历史值 ⇒ 当作没设过 ⇒ 铺满
    expect(mod.useContentWidth().width.value).toBeNull()
    // 设置也不能抛
    expect(() => mod.setContentWidth(1200)).not.toThrow()
    expect(mod.useContentWidth().width.value).toBe(1200)
    installMemoryStorage() // 还原，避免污染后续用例
  })
})

describe('resetContentWidth：回到铺满', () => {
  it('复位后 width 变回 null，且清掉存储里的值', () => {
    setContentWidth(1200)
    expect(localStorage.getItem(KEY)).toBe('1200')
    resetContentWidth()
    expect(useContentWidth().width.value).toBeNull()
    expect(localStorage.getItem(KEY)).toBeNull()
  })

  it('复位后生效宽度回到可用宽度', () => {
    setContentWidthBudget(1546)
    setContentWidth(1000)
    expect(useContentWidth().effectiveWidth.value).toBe(1000)
    resetContentWidth()
    expect(useContentWidth().effectiveWidth.value).toBe(1546)
  })
})

describe('touched：一次性提示的开关依据', () => {
  it('初始为 false（没调过 ⇒ 该提示）', () => {
    expect(useContentWidth().touched.value).toBe(false)
  })

  it('调过一次后变 true 并持久化（提示不再出现）', () => {
    setContentWidth(1100)
    expect(useContentWidth().touched.value).toBe(true)
    expect(localStorage.getItem(TOUCHED_KEY)).toBe('true')
  })

  it('复位也算"调过"（用户已经知道有这么回事了）', () => {
    resetContentWidth()
    expect(useContentWidth().touched.value).toBe(true)
  })
})

describe('容量夹取：不得污染用户的选择', () => {
  /**
   * ★ 另一个最重要的回归。
   *
   * 「用户选择」与「当前窗口还给得起多少」是**两个**值，混成一个就会丢选择：
   * 把 min(选择, 容量) 写回 chosen，则窗口缩小一次，用户的 1200 就被永久改成
   * 800，再把窗口放大也回不去了 —— 而用户从没要求过 800。
   */
  it('窗口缩小再放大后，用户的选择原样恢复', () => {
    setContentWidth(1200)
    const { effectiveWidth } = useContentWidth()

    setContentWidthBudget(800)
    expect(effectiveWidth.value).toBe(800) // 生效值被夹住，不溢出

    setContentWidthBudget(1600)
    expect(effectiveWidth.value).toBe(1200) // 选择未被改成 800
    expect(localStorage.getItem(KEY)).toBe('1200')
  })

  it('未测量（null）时按用户选择来', () => {
    setContentWidth(1200)
    setContentWidthBudget(null)
    expect(useContentWidth().effectiveWidth.value).toBe(1200)
  })
})

describe('从 localStorage 恢复', () => {
  it('坏数据（空串）退回"铺满"', async () => {
    localStorage.setItem(KEY, '')
    vi.resetModules()
    const mod = await import('./useContentWidth')
    expect(mod.useContentWidth().width.value).toBeNull()
  })

  it('非数字也退回"铺满"', async () => {
    localStorage.setItem(KEY, 'abc')
    vi.resetModules()
    const mod = await import('./useContentWidth')
    expect(mod.useContentWidth().width.value).toBeNull()
  })

  it('越界的历史值被夹回区间', async () => {
    localStorage.setItem(KEY, '99999')
    vi.resetModules()
    const mod = await import('./useContentWidth')
    expect(mod.useContentWidth().width.value).toBe(MAX_CONTENT_WIDTH)
  })
})
