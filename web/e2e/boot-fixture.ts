// e2e 专用入口（`vite --mode e2e` 时 index.html 指向它）。
//
// 为什么需要独立的入口：夹具路由必须在 `mount()` **之前**同步装好，否则 `/__e2e/detail`
// 会掉进主路由的兜底 `/:pathMatch(.*)*` 被静默重定向到 /dashboard（踩过，见 ui-e2e.md）。
// 而把这段注册写进 `src/main.ts` 会污染**生产入口本身** —— 一旦哪天静态 import
// 被 Rollup 判成"有副作用没摇掉"，e2e 夹具就会进 dist。
//
// 所以：**生产入口保持干净**，e2e 入口是本文件 —— 它只多一件事：装夹具路由。
// 两者的差异被钉死在 `e2e/build-guard.spec.ts`（断言生产产物里没有 __e2e 字样）。
//
// index.html 的切换由 vite.config.ts 的 e2e 插件完成（仅 mode=e2e 生效）。

import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from '@/app/App.vue'
import { fixtureRouter } from './fixtures/page-fixture'
import { registerServiceWorker } from '@/composables/usePwa'
import { initTheme } from '@/composables/useTheme'
import { initReduceMotion } from '@/composables/useReduceMotion'
import '@/styles/index.css'

// 与生产入口同序：外观偏好 → store → router → mount。
// 唯一区别是 router 用夹具表的 `fixtureRouter`（只认 /__e2e/detail，没有兜底可掉）。
initTheme()
initReduceMotion()

const app = createApp(App)

app.use(createPinia())
app.use(fixtureRouter)

app.mount('#app')

// e2e **不注册** Service Worker：它会缓存旧的 dev 产物，把"改了没生效"变成常态。
// 保留 import 只为提醒两者入口的其余接线必须一致；调用位置刻意留空。
void registerServiceWorker
