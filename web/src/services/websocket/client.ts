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
}

export class WebSocketClient {
  private ws: WebSocket | null = null
  private policy = new ReconnectPolicy()
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null
  /** 手动断开后不再自动重连 */
  private manualClose = false

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
      this.policy.reset()
      this.setState('connected')
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
