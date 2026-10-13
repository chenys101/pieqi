// 重连补拉单测：验证"首次连接不补拉、重连才补拉"这一判据。
//
// 背景（生产 bug）：WS 断开期间服务端把事件投给了已 Unsubscribe 的订阅者，
// 既不置 dropped 标记也无补发；重连后 snapshot 又是轻量视图（不含 events）。
// 结果时间线永久缺一段，详情不刷新，只有手动刷新页面才能恢复。
// 本测试守住那条补拉路径的存在与触发时机。
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { WebSocketClient } from './client'

/** 最小 WebSocket 桩：手动触发 open/close，供客户端状态机驱动 */
class FakeWebSocket {
  static instances: FakeWebSocket[] = []
  static OPEN = 1
  static CONNECTING = 0
  readyState = 0
  onopen: (() => void) | null = null
  onmessage: ((e: { data: string }) => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null

  constructor(public url: string) {
    FakeWebSocket.instances.push(this)
  }

  close(): void {
    this.readyState = 3
    this.onclose?.()
  }
  send(): void {}
  /** 测试驱动：模拟连接建立 */
  fireOpen(): void {
    this.readyState = 1
    this.onopen?.()
  }
  /** 测试驱动：模拟连接断开 */
  fireClose(): void {
    this.readyState = 3
    this.onclose?.()
  }
}

describe('WebSocketClient 重连补拉', () => {
  beforeEach(() => {
    FakeWebSocket.instances = []
    vi.stubGlobal('WebSocket', FakeWebSocket as never)
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  function makeClient() {
    const onReconnected = vi.fn()
    const client = new WebSocketClient({
      url: () => 'ws://test/api/ws',
      onMessage: () => {},
      onStateChange: () => {},
      onReconnected,
    })
    return { client, onReconnected }
  }

  it('首次连接不触发补拉（服务端紧接着会发首帧 snapshot）', () => {
    const { client, onReconnected } = makeClient()
    client.connect()
    FakeWebSocket.instances[0].fireOpen()
    expect(onReconnected).not.toHaveBeenCalled()
  })

  it('断线重连后触发补拉', () => {
    const { client, onReconnected } = makeClient()
    client.connect()
    FakeWebSocket.instances[0].fireOpen()
    expect(onReconnected).not.toHaveBeenCalled()

    // 断开 → 退避计时器到期 → 重新连接 → open
    FakeWebSocket.instances[0].fireClose()
    vi.advanceTimersByTime(1000)
    expect(FakeWebSocket.instances).toHaveLength(2)
    FakeWebSocket.instances[1].fireOpen()

    expect(onReconnected).toHaveBeenCalledTimes(1)
  })

  it('多次重连每次都补拉（每次断线都是一个空窗期）', () => {
    const { client, onReconnected } = makeClient()
    client.connect()
    FakeWebSocket.instances[0].fireOpen()

    for (let i = 0; i < 3; i++) {
      FakeWebSocket.instances[i].fireClose()
      vi.advanceTimersByTime(30000)
      FakeWebSocket.instances[i + 1].fireOpen()
    }
    expect(onReconnected).toHaveBeenCalledTimes(3)
  })

  it('手动 disconnect 后不再重连、也不再补拉', () => {
    const { client, onReconnected } = makeClient()
    client.connect()
    FakeWebSocket.instances[0].fireOpen()

    client.disconnect()
    vi.advanceTimersByTime(60000)
    expect(FakeWebSocket.instances).toHaveLength(1)
    expect(onReconnected).not.toHaveBeenCalled()
  })
})
