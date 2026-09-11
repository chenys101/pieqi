// 应用入口（方案 §52）：Pinia → Router → mount → SW 注册
import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './app/App.vue'
import router from './app/router'
import { registerServiceWorker } from '@/composables/usePwa'
import { initTheme } from '@/composables/useTheme'
import { initReduceMotion } from '@/composables/useReduceMotion'
import './styles/index.css'

// 外观偏好：首屏已由 index.html 的内联脚本设好 data-theme / data-reduce-motion（防闪），
// 这里只接管后续（系统主题变化重算 + meta theme-color 同步）。
initTheme()
initReduceMotion()

const app = createApp(App)

app.use(createPinia())
app.use(router)

app.mount('#app')

// PWA：Service Worker（离线兜底 + 新版本自动生效）
registerServiceWorker()
