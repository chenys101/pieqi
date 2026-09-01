// useWebSocket（方案 §34）：实时连接的唯一入口。
// 单例客户端在 connectRealtime() 中创建（由 providers 调用一次），
// 组件通过本 composable 读取状态 / 触发重连，禁止自行 new WebSocket。

import type { Ref } from 'vue'
import { storeToRefs } from 'pinia'
import { WebSocketClient } from '@/services/websocket/client'
import type { ConnectionState } from '@/services/websocket/client'
import { normalizeWsMessage } from '@/services/websocket/normalizer'
import { dispatch } from '@/services/websocket/dispatcher'
import { useSessionStore } from '@/stores/session'
import { useTaskStore } from '@/stores/task'
import { tunnelToken } from '@/services/api/client'

let client: WebSocketClient | null = null

/** 构造 WS URL：默认同源 /api/ws（带 tunnel token），可用 VITE_WS_URL 覆盖 */
function wsUrl(): string {
  if (import.meta.env.VITE_WS_URL) return import.meta.env.VITE_WS_URL
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  const token = tunnelToken()
  const qs = token ? `?token=${encodeURIComponent(token)}` : ''
  return `${proto}://${location.host}/api/ws${qs}`
}

/** 建立全局实时连接（应用生命周期内只调用一次） */
export function connectRealtime(): void {
  if (client) return
  const sessionStore = useSessionStore()
  const taskStore = useTaskStore()

  client = new WebSocketClient({
    url: wsUrl,
    onMessage: (raw) => {
      const msg = normalizeWsMessage(raw)
      if (msg) dispatch(msg, { taskStore, sessionStore })
    },
    onStateChange: (state) => sessionStore.setConnection(state),
  })
  client.connect()
}

export function useWebSocket() {
  const sessionStore = useSessionStore()
  const { connection } = storeToRefs(sessionStore)
  return {
    connection: connection as Ref<ConnectionState>,
    reconnect: () => client?.reconnect(),
  }
}
