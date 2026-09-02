// Notification Store：全局 Toast（方案 §35）

import { defineStore } from 'pinia'

export interface Toast {
  id: number
  kind: 'error' | 'success' | 'info'
  message: string
}

let nextId = 1

export const useNotificationStore = defineStore('notification', {
  state: () => ({
    toasts: [] as Toast[],
  }),
  actions: {
    push(message: string, kind: Toast['kind'] = 'error') {
      const id = nextId++
      this.toasts.push({ id, kind, message })
      // 5s 自动消失
      setTimeout(() => this.dismiss(id), 5000)
    },
    error(message: string) {
      this.push(message, 'error')
    },
    success(message: string) {
      this.push(message, 'success')
    },
    dismiss(id: number) {
      this.toasts = this.toasts.filter((t) => t.id !== id)
    },
  },
})
