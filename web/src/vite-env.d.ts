/// <reference types="vite/client" />

// 环境变量类型（方案 §54）：默认相对路径，仅开发环境使用独立后端地址
interface ImportMetaEnv {
  readonly VITE_API_BASE_URL?: string
  readonly VITE_WS_URL?: string
  readonly VITE_APP_VERSION?: string
}

// 构建期注入的应用版本号（vite define，来自 package.json）
declare const __APP_VERSION__: string

declare module '*.vue' {
  import type { DefineComponent } from 'vue'
  const component: DefineComponent<{}, {}, any>
  export default component
}
