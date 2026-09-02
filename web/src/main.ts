// 应用入口（方案 §52）：Pinia → Router → mount → SW 注册
import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './app/App.vue'
import router from './app/router'
import { registerServiceWorker } from '@/composables/usePwa'
import './styles/index.css'

const app = createApp(App)

app.use(createPinia())
app.use(router)

app.mount('#app')

// PWA：Service Worker（离线兜底 + 新版本自动生效）
registerServiceWorker()
