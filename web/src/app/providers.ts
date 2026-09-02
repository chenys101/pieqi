// Providers（方案 §52）：应用级一次性接线，在 App.vue setup 中调用。
// 组件禁止自行建立 WS 连接 / 拉取 boot 数据（方案 §34）。

import { useAppStore } from '@/stores/app'
import { useTaskStore } from '@/stores/task'
import { connectRealtime } from '@/composables/useWebSocket'

let initialized = false

/** 应用启动：boot 上下文 + 首屏任务 + 实时连接（只执行一次） */
export function setupProviders(): void {
  if (initialized) return
  initialized = true

  const appStore = useAppStore()
  const taskStore = useTaskStore()

  // 1) 鉴权状态 + 斜杠补全源（失败静默）
  void appStore.loadBootContext()

  // 2) 首屏任务列表（HTTP 一次性；后续靠 WS 增量）
  void taskStore.loadTasks()

  // 3) 全局唯一 WebSocket 连接（重连 / 去重由内部处理）
  connectRealtime()
}
