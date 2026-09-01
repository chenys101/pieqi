// 重连策略单测（方案 §12）：指数退避 1s→30s 封顶，成功后 reset
import { describe, expect, it } from 'vitest'
import { ReconnectPolicy } from './reconnect'

describe('ReconnectPolicy', () => {
  it('按 1s/2s/4s/8s/16s 指数退避', () => {
    const p = new ReconnectPolicy()
    expect(p.next()).toBe(1000)
    expect(p.next()).toBe(2000)
    expect(p.next()).toBe(4000)
    expect(p.next()).toBe(8000)
    expect(p.next()).toBe(16000)
  })

  it('30s 封顶后不再增长', () => {
    const p = new ReconnectPolicy()
    for (let i = 0; i < 10; i++) p.next()
    expect(p.next()).toBe(30000)
    expect(p.next()).toBe(30000)
  })

  it('reset 后回到 1s（网络恢复）', () => {
    const p = new ReconnectPolicy()
    p.next()
    p.next()
    p.reset()
    expect(p.next()).toBe(1000)
    expect(p.attempts).toBe(1)
  })
})
