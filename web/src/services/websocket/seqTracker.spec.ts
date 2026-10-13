// SeqTracker 单测：gap detection 的判据边界。
//
// 这块的错判代价不对称：
//   - 漏报（该报没报）→ 时间线永久缺一段，用户只能手动刷新（就是那个生产 bug）
//   - 误报（不该报却报）→ 一次多余的全量补拉，用户几乎无感
// 所以判据刻意偏保守：拿不准就**不报**，但一旦确知有洞必须报。
import { describe, expect, it } from 'vitest'
import { SeqTracker } from './seqTracker'

describe('SeqTracker', () => {
  it('连续序号不报 gap', () => {
    const t = new SeqTracker()
    expect(t.observe(1)).toBe(false)
    expect(t.observe(2)).toBe(false)
    expect(t.observe(3)).toBe(false)
    expect(t.lastSeq).toBe(3)
  })

  it('首次收到不报 gap（还没有游标可比）', () => {
    const t = new SeqTracker()
    expect(t.observe(100)).toBe(false)
    expect(t.lastSeq).toBe(100)
  })

  it('序号跳跃 → 报 gap', () => {
    const t = new SeqTracker()
    t.observe(1)
    expect(t.observe(5)).toBe(true) // 2,3,4 丢了
  })

  it('重复序号不报 gap（重连后重放旧事件属正常）', () => {
    const t = new SeqTracker()
    t.observe(1)
    t.observe(2)
    expect(t.observe(2)).toBe(false)
    expect(t.observe(1)).toBe(false)
    // 回退的序号把基线拉到该处（见下方"基线重置"用例）
    expect(t.lastSeq).toBe(1)
  })

  it('seq 缺失/非法一律不判定（旧服务端不能当成丢事件）', () => {
    const t = new SeqTracker()
    t.observe(1)
    expect(t.observe(undefined)).toBe(false)
    expect(t.observe(0)).toBe(false)
    expect(t.observe(-5)).toBe(false)
    // 游标不被非法值污染
    expect(t.lastSeq).toBe(1)
  })

  // --- 基线重置（服务端重启 / 计数器归零）---
  //
  // 这是第一版的**静默失效**根因：EventBus.seq 是进程内计数器，服务端一重启就归零。
  // 旧实现把回退的 seq 判为"乱序"后**不更新游标**，于是游标永远钉在高位，
  // 之后低号段里的任何空洞都检测不出来 —— 不报错、测试也不红，补拉却再不会触发。

  it('基线重置：序号回退后必须重设基线，不能把游标钉在高位', () => {
    const t = new SeqTracker()
    t.observe(100) // 上一进程的序号
    expect(t.observe(1)).toBe(false) // 服务端重启，计数器归零
    expect(t.lastSeq).toBe(1) // 游标必须跟着回落
  })

  it('基线重置后，低号段内的空洞仍必须被发现', () => {
    const t = new SeqTracker()
    t.observeSnapshot(467) // 服务端已有历史
    t.observe(1)
    t.observe(2)
    // 3、4 丢了
    expect(t.observe(5)).toBe(true)
  })

  it('快照中途重设基线：后续事件从新基线继续判定', () => {
    const t = new SeqTracker()
    t.observe(50)
    t.observeSnapshot(3) // 服务端重启后重连，快照报小号
    expect(t.lastSeq).toBe(3)
    expect(t.observe(4)).toBe(false)
    expect(t.observe(6)).toBe(true)
  })

  // --- observeSnapshot 的职责边界 ---

  it('快照只建立基线，不负责判定断裂', () => {
    const t = new SeqTracker()
    t.observe(1)
    t.observe(2)
    // 断线期间服务端涨到 10；快照重设基线（不判定）
    expect(t.observeSnapshot(10)).toBe(false)
    expect(t.lastSeq).toBe(10)
  })

  it('首次快照不报 gap，并建立基线', () => {
    const t = new SeqTracker()
    expect(t.observeSnapshot(42)).toBe(false)
    expect(t.lastSeq).toBe(42)
  })

  it('快照 seq 非法时不改动既有基线', () => {
    const t = new SeqTracker()
    t.observe(7)
    expect(t.observeSnapshot(undefined)).toBe(false)
    expect(t.observeSnapshot(0)).toBe(false)
    expect(t.lastSeq).toBe(7)
  })

  it('reset 后回到"无游标"状态，不凭旧游标误报', () => {
    const t = new SeqTracker()
    t.observe(100)
    t.reset()
    expect(t.lastSeq).toBeNull()
    expect(t.observe(5)).toBe(false)
  })

  it('快照建立基线后，后续事件从基线继续判定', () => {
    const t = new SeqTracker()
    t.observeSnapshot(10)
    expect(t.observe(11)).toBe(false)
    expect(t.observe(13)).toBe(true)
  })
})
