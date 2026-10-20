// 失败结论的单元测试（vitest）。
//
// 为什么必须测这个：这段代码**只有红色的时候才执行** —— 全绿时它一行都不跑。
// 也就是说它是"最不容易被日常开发顺手验证到"的那一类代码，而它一旦崩，
// 表现是"测试红了、结论却没有"（比没有 reporter 更糟：人会以为报告系统坏了）。
// 实测踩过：第一版在 `location.file` 缺失时 `undefined.replace` 崩掉，
// 而当时正是**6 个用例红着**，结论文件什么都没有。
//
// 这里钉住三件事：
//   ① 失败能收敛成"实测/期望"一行，且**签名会抹掉数字**（同一断言在不同视口只占一行）；
//   ② `location` 缺失/字段不全时不崩；
//   ③ 跨运行对比能说出「已修好 / 仍红 / 新挂」。

import { describe, expect, it } from 'vitest'
import {
  collectFailures,
  compare,
  renderSummary,
  rowOf,
  type Failure,
  type RunSnapshot,
} from './report'

/**
 * 最小可用的 Playwright JSON reporter 形状 —— **按真实产物校准**（别按直觉构造）。
 *
 * 实测的真实形状（2026-10-20 从 _playwright-report.json 抄的）：
 *   · 带视口的用例名在 `spec.title`，`test.title` **不存在**；
 *   · 出处文件/行在 `results[].error.location`，`test.location` **不存在**；
 *   · 错误消息里带 **ANSI 色码**（`Expected: <= \u001b[32m0\u001b[39m`）。
 *
 * 这三条正是第一版报告取错的地方（"出现 3 次：、、" / 出处全是 `xxx:0`），
 * 而它们**只有红的时候才暴露**，所以必须由这里钉住。
 */
function report(specs: { project?: string; title: string; error?: string; file?: string | null }[]) {
  return {
    stats: { expected: 0, unexpected: specs.length, flaky: 0, skipped: 0, duration: 0 },
    errors: [],
    suites: [
      {
        title: 'e2e',
        file: 'layout/fixture.spec.ts',
        specs: specs.map((s) => ({
          title: s.title,
          ok: false,
          tests: [
            {
              status: 'unexpected',
              projectName: s.project ?? 'layout',
              expectedStatus: 'passed',
              results: [
                {
                  status: 'failed',
                  duration: 10,
                  retry: 0,
                  ...(s.error
                    ? {
                        error: {
                          message: s.error,
                          location: s.file === null ? undefined : { file: s.file ?? 'D:\\code\\go\\pieqi\\web\\e2e\\layout\\fixture.spec.ts', line: 412 },
                        },
                      }
                    : {}),
                },
              ],
            },
          ],
        })),
      },
    ],
  }
}

const ERR_GAP = `Error: 动作行压在 textarea 盒子内部（gap=-61px）

Call log:
  - expect.toBeGreaterThanOrEqual with timeout 5000ms`

/** 一条失败里带**多个**数字：签名只认第一行，否则一次失败会裂成多个签名 */
const ERR_COVER = `Error: 滚动到最底后，动作行仍盖住文字区 41px：可见文字高 66px（行高约 22px，约 3 行可见）

Error Context: test-results/x/test-failed-1.png`

/** 真实产物里错误消息的形态（含 ANSI 色码），从 _playwright-report.json 抄的 */
const REAL_MESSAGE =
  'Error: 滚动到最底后，动作行仍盖住文字区 41px：可见文字高 66px（行高约 22px，约 3 行可见），动作行高 50px\n\n' +
  '\u001b[2mexpect(\u001b[22m\u001b[31mreceived\u001b[39m\u001b[2m).\u001b[22mtoBeLessThanOrEqual\u001b[2m(\u001b[22m\u001b[32mexpected\u001b[39m\u001b[2m)\u001b[22m\n\n' +
  'Expected: <= \u001b[32m0\u001b[39m\nReceived:    \u001b[31m41\u001b[39m'

describe('collectFailures', () => {
  it('把断言消息收敛成一行（取带数字的那行，丢掉 Call log）', () => {
    const { failures } = collectFailures(report([{ title: 'detail @ mobile (375×667)', error: ERR_GAP }]))
    expect(failures).toHaveLength(1)
    expect(failures[0].assertion).toBe('动作行压在 textarea 盒子内部（gap=-61px）')
    expect(failures[0].assertion).not.toContain('Call log')
  })

  it('优先取我们手写的中文诊断，而不是 Playwright 拆开的 Expected/Received', () => {
    // 真实形态：第一行是手写诊断，后面才是 Expected/Received（还带 ANSI 色码）
    const { failures } = collectFailures(report([{ title: 'detail @ mobile', error: REAL_MESSAGE }]))
    expect(failures).toHaveLength(1)
    expect(failures[0].assertion).toContain('动作行仍盖住文字区 41px')
    expect(failures[0].assertion, '不能只剩 Expected/Received').not.toMatch(/^Expected:/)
    expect(failures[0].assertion, 'ANSI 色码必须被剥掉').not.toContain('\u001b')
  })

  it('没有手写诊断时，把 Expected / Received 合成一行', () => {
    const auto = 'expect(received).toBe(expected)\n\nExpected: 0\nReceived: 41'
    const { failures } = collectFailures(report([{ title: 'detail @ mobile', error: auto }]))
    expect(failures[0].assertion).toBe('Expected: 0 / Received: 41')
  })

  it('标题取 spec.title（带视口），出处取 error.location（不是 test.location）', () => {
    const { failures } = collectFailures(report([{ title: 'detail @ mobile (375×667)', error: ERR_GAP }]))
    expect(failures[0].where, '标题为空是实测踩过的坑（"出现 3 次：、、"）').toBe('detail @ mobile (375×667)')
    expect(failures[0].location).toBe('D:/code/go/pieqi/web/e2e/layout/fixture.spec.ts:412')
  })

  it('同一断言在不同视口只产生**一个**签名（数字被抹掉）', () => {
    const { failures } = collectFailures(
      report([
        { title: 'detail @ mobile (375×667)', error: 'Error: gap=-61px' },
        { title: 'detail @ desktop (1440×900)', error: 'Error: gap=-103px' },
      ]),
    )
    expect(failures).toHaveLength(2)
    expect(new Set(failures.map((f) => f.key)).size, '两个视口的同一条断言必须同签名').toBe(1)
  })

  it('不同断言不会因为抹掉数字而被合并', () => {
    const { failures } = collectFailures(
      report([
        { title: 'a @ mobile', error: ERR_GAP },
        { title: 'a @ mobile', error: ERR_COVER },
      ]),
    )
    expect(new Set(failures.map((f) => f.key)).size).toBe(2)
  })

  it('location.file 缺失时不崩（此前这里崩过）', () => {
    const { failures } = collectFailures(report([{ title: 'detail @ mobile', error: ERR_GAP, file: null }]))
    expect(failures).toHaveLength(1)
    expect(failures[0].location).toMatch(/:\d+$/)
  })

  it('没有错误对象的失败也不崩（给一句明确的中文占位）', () => {
    const { failures } = collectFailures(report([{ title: 'detail @ mobile' }]))
    expect(failures[0].assertion).toContain('无错误信息')
  })

  it('title / projectName 缺失时也不崩（运行级错误、超时、夹具崩溃的形态）', () => {
    // ⚠️ 实测踩过：这类失败在 Playwright 的 JSON 里结构不完整，
    //    第一版在 `title.replace` 上崩掉，整份结论文件都没生成。
    const broken = {
      stats: { expected: 0, unexpected: 1, flaky: 0, skipped: 0, duration: 0 },
      errors: [],
      suites: [
        {
          title: 'e2e',
          specs: [
            {
              title: undefined,
              ok: false,
              tests: [
                {
                  status: 'unexpected',
                  projectName: undefined,
                  expectedStatus: 'passed',
                  results: [{ status: 'failed', duration: 1, retry: 0 }],
                },
              ],
            },
          ],
        },
      ],
    }
    const { failures } = collectFailures(broken as never)
    expect(failures).toHaveLength(1)
    expect(failures[0].key).toContain('未命名用例')
    expect(failures[0].location).toMatch(/:\d+$/)
  })
})

function snap(failures: Failure[]): RunSnapshot {
  return {
    at: new Date().toISOString(),
    keys: [...new Set(failures.map((f) => f.key))],
    rows: Object.fromEntries(failures.map((f) => [f.key, rowOf(f)])),
  }
}

describe('compare + renderSummary', () => {
  const f1 = collectFailures(report([{ title: 'detail @ mobile', error: ERR_GAP }])).failures
  const f2 = collectFailures(report([{ title: 'detail @ mobile', error: ERR_COVER }])).failures

  it('同一段多行错误只算**一条**失败签名（防"一次失败裂成多条"）', () => {
    expect(f1).toHaveLength(1)
    expect(f2, '一条错误消息里出现多个数字时，也必须是 1 条签名').toHaveLength(1)
  })

  /**
   * ⚠️ 这条测的是"两个签名互不干扰"，所以**必须用两条内容不同的错误**。
   *
   * 踩过的坑（自己写测试时踩的）：一开始用同一份 ERR_GAP 当"上次的失败"、用 ERR_COVER
   * 当"这次的失败"，但 `snap()` 收的是一组 failure —— 于是上次那边被算成 2 个 key，
   * 断言看起来像"compare 错了"，其实是测试数据构造错了。**测试数据本身就是判据**。
   */
  it('能说出「已修好 / 仍红 / 新挂」', () => {
    const prev = snap([f1[0], f2[0]])
    const now = snap([f2[0]])
    const cmp = compare(prev, now)
    expect(cmp.fixed).toEqual([f1[0].key])
    expect(cmp.still).toEqual([f2[0].key])
    expect(cmp.added).toEqual([])
  })

  it('结果完全没变时明确提示"别再原样重跑"', () => {
    const prev = snap([f1[0]])
    const now = snap([f1[0]])
    const text = renderSummary(now, compare(prev, now), [f1[0]], { total: 1, passed: 0, unexpected: 1 })
    expect(text).toContain('仍红（1）')
    expect(text).toContain('完全一样')
  })

  it('新挂的失败排在最前，并标出来', () => {
    const prev = snap([f1[0]])
    const now = snap([f1[0], f2[0]])
    const text = renderSummary(now, compare(prev, now), [f1[0], f2[0]], { total: 2, passed: 0, unexpected: 2 })
    const newIdx = text.indexOf('**[新挂]**')
    const oldIdx = text.indexOf(gap_line(text))
    expect(newIdx).toBeGreaterThan(-1)
    expect(newIdx).toBeLessThan(oldIdx)
  })

  it('没有基线（首次运行）时不出现对比段，也不崩', () => {
    const text = renderSummary(snap([f1[0]]), null, [f1[0]], { total: 1, passed: 0, unexpected: 1 })
    expect(text).toContain('FAIL')
    expect(text).not.toContain('与上一次运行相比')
  })

  it('全绿时也报"已修好"（迭代最想要的那句），且用的是上次的行内容', () => {
    const prev = snap([f1[0]])
    const now: RunSnapshot = { at: '', keys: [], rows: {} }
    const text = renderSummary(now, compare(prev, now), [], { total: 18, passed: 18, unexpected: 0 }, prev.rows)
    expect(text).toContain('PASS  18/18')
    expect(text).toContain('已修好 1 条')
    // 不能是裸 key（`layout | … |` 里的断言段必须来自上次的真实行）
    expect(text).toContain('动作行压在 textarea 盒子内部')
    expect(text).not.toMatch(/^- layout \| /)
  })

  it('绿→再绿：上一轮修好的那几条要显示"保持绿"', () => {
    const now: RunSnapshot = { at: '', keys: [], rows: {}, lastFixed: { [f1[0].key]: rowOf(f1[0]) } }
    const text = renderSummary(now, compare(now, now), [], { total: 18, passed: 18, unexpected: 0 })
    expect(text).toContain('保持绿')
    expect(text).toContain('动作行压在 textarea 盒子内部')
  })

  it('全绿时给出"不用看图"的一句话结论', () => {
    const text = renderSummary({ at: '', keys: [], rows: {} }, null, [], { total: 5, passed: 5, unexpected: 0 })
    expect(text).toContain('PASS  5/5')
    expect(text).toContain('不需要看截图')
  })
})

/** 找出没被标 [新挂] 的那条的行首，用来比较顺序 */
function gap_line(text: string): string {
  const line = text
    .split('\n')
    .find((l) => l.startsWith('- ') && !l.includes('[新挂]'))
  return line ?? ''
}
