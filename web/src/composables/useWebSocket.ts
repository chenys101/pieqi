// useWebSocket（方案 §34）：实时连接的唯一入口。
// 单例客户端在 connectRealtime() 中创建（由 providers 调用一次），
// 组件通过本 composable 读取状态 / 触发重连，禁止自行 new WebSocket。

import type { Ref } from 'vue'
import { storeToRefs } from 'pinia'
import { WebSocketClient } from '@/services/websocket/client'
import type { ConnectionState } from '@/services/websocket/client'
import { normalizeWsMessage } from '@/services/websocket/normalizer'
import { dispatch } from '@/services/websocket/dispatcher'
import { SeqTracker } from '@/services/websocket/seqTracker'
import { useSessionStore } from '@/stores/session'
import { useTaskStore } from '@/stores/task'
import { tunnelToken } from '@/services/api/client'

let client: WebSocketClient | null = null

/**
 * 重连补拉钩子注册表。
 *
 * 为什么是"注册表"而不是直接在这里 import router 判断当前路由：
 * 需要补拉的是**当前正在看的那个会话详情**，而"当前在看哪个会话"只有详情页自己知道
 * （路由参数在页面组件里）。让详情页把自己的重载函数登记进来，WS 层只负责在重连时
 * 无差别通知 —— 两个模块各自只依赖自己真正需要的东西，也便于单测。
 *
 * 返回注销函数（组件 onBeforeUnmount 调用），避免卸载后仍被通知。
 */
type ResyncHandler = () => void
const resyncHandlers = new Set<ResyncHandler>()

export function onRealtimeResync(fn: ResyncHandler): () => void {
  resyncHandlers.add(fn)
  return () => resyncHandlers.delete(fn)
}

/** 重连后通知所有登记方补拉（供 client 的 onReconnected 与单测调用） */
export function runRealtimeResync(): void {
  for (const fn of resyncHandlers) {
    try {
      fn()
    } catch {
      // 单个补拉失败不能拖垮其余登记方：补拉是"尽力而为"的兜底，
      // 下一次重连或用户手动刷新仍会覆盖到。
    }
  }
}

/** 事件序号连续性追踪（gap detection）：发现漏收即触发补拉，见 seqTracker.ts */
const seqTracker = new SeqTracker()

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
      if (!msg) return
      // 先判连续性再应用。两条覆盖互补，缺一不可：
      //   - 快照：**建立/重设基线**（服务端 seq 是进程内计数器，重启即归零，
      //     所以快照只作基线、不判定断裂 —— 详见 seqTracker.ts 的说明）
      //   - 其余事件：与基线内上一条比较，跳跃即报 gap
      // 发现 gap 说明同一连接内丢了事件（服务端缓冲溢出），必须补拉；
      // 而**断线**造成的丢失由下面的 onReconnected 无条件覆盖，不依赖这个判定。
      const gap = msg.type === 'snapshot' ? seqTracker.observeSnapshot(msg.seq) : seqTracker.observe(msg.seq)
      dispatch(msg, { taskStore, sessionStore })
      if (gap) runRealtimeResync()
    },
    onStateChange: (state) => sessionStore.setConnection(state),
    // 重连成功 → 通知所有已登记页面补拉（见 onRealtimeResync 的说明）。
    onReconnected: runRealtimeResync,
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
