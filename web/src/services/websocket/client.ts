// WebSocket 客户端（方案 §11）：
// connect / disconnect / reconnect / 心跳容错 / 消息解析 / 自动重连。
// 组件禁止直接 new WebSocket —— 一律经由此客户端 → Dispatcher → Pinia。

import { ReconnectPolicy } from './reconnect'

export type ConnectionState = 'initial' | 'connecting' | 'connected' | 'reconnecting' | 'disconnected'

export interface WebSocketClientOptions {
  /** 每次连接时动态求 URL（token 可能刷新） */
  url: () => string
  onMessage: (data: unknown) => void
  onStateChange: (state: ConnectionState) => void
  /**
   * 重连成功后的补拉钩子（首次连接不触发）。
   *
   * 为什么必须有这个钩子：断开期间服务端事件投给了已经 Unsubscribe 的订阅者
   * （见 internal/api/ws.go 的 defer），**不是** EventBus 的"缓冲满丢弃"，
   * 因此既不置 dropped 标记、也没有任何补发 —— 那段时间的 task_delta 永久消失。
   * 重连后服务端补发的 snapshot 又是轻量视图（不含 events），
   * 于是时间线会永久缺一段，直到用户手动刷新页面。
   *
   * 这个钩子就是那条缺失的补拉路径：重连后由调用方重新拉取当前详情（幂等全量替换）。
   */
  onReconnected?: () => void
}

export class WebSocketClient {
  private ws: WebSocket | null = null
  private policy = new ReconnectPolicy()
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null
  /** 手动断开后不再自动重连 */
  private manualClose = false
  /** 是否曾经成功连上过（区分首次连接与重连，见 onopen） */
  private hasConnectedBefore = false

  constructor(private opts: WebSocketClientOptions) {}

  connect(): void {
    if (this.ws && (this.ws.readyState === WebSocket.OPEN || this.ws.readyState === WebSocket.CONNECTING)) {
      return
    }
    this.manualClose = false
    this.setState(this.policy.attempts > 0 ? 'reconnecting' : 'connecting')
    try {
      this.ws = new WebSocket(this.opts.url())
    } catch {
      this.scheduleReconnect()
      return
    }
    this.ws.onopen = () => {
      // 是否"重连"要在 reset 之前判定：reset 会把 attempts 清零，
      // 之后就无法区分首次连接与重连了。
      const isReconnect = this.hasConnectedBefore
      this.hasConnectedBefore = true
      this.policy.reset()
      this.setState('connected')
      // 首次连接不补拉：服务端紧接着就会发首帧 snapshot（且此时详情页自己会按需拉）。
      // 只有重连才存在"空窗期"——见 onReconnected 的说明。
      if (isReconnect) this.opts.onReconnected?.()
    }
    this.ws.onmessage = (e) => {
      try {
        this.opts.onMessage(JSON.parse(e.data as string))
      } catch {
        // 非 JSON 帧（协议异常）：丢弃，不断连接
      }
    }
    this.ws.onclose = () => {
      this.ws = null
      if (!this.manualClose) this.scheduleReconnect()
      else this.setState('disconnected')
    }
    this.ws.onerror = () => {
      // onclose 随后触发，统一由 onclose 处理重连
    }
  }

  disconnect(): void {
    this.manualClose = true
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }
    this.ws?.close()
    this.ws = null
    this.setState('disconnected')
  }

  /** 手动立即重连（重置退避） */
  reconnect(): void {
    this.manualClose = false
    this.policy.reset()
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }
    this.ws?.close()
    this.ws = null
    this.connect()
  }

  send(message: unknown): void {
    if (this.ws?.readyState !== WebSocket.OPEN) return
    this.ws.send(JSON.stringify(message))
  }

  get state(): ConnectionState {
    return this._state
  }
  private _state: ConnectionState = 'initial'

  private setState(s: ConnectionState): void {
    this._state = s
    this.opts.onStateChange(s)
  }

  private scheduleReconnect(): void {
    this.setState('reconnecting')
    const delay = this.policy.next()
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null
      this.connect()
    }, delay)
  }
}
