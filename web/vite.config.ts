/// <reference types="vitest/config" />
import { defineConfig, type Plugin } from 'vite'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath, URL } from 'node:url'
import { readFileSync, writeFileSync } from 'node:fs'

// package.json version → 构建期常量（Settings「关于」展示）
const pkg = JSON.parse(readFileSync(new URL('./package.json', import.meta.url), 'utf-8'))

/**
 * sw.js 版本号注入（R4 / AC-R4-01~03）。
 *
 * public/ 下的文件 vite 原样拷贝、不走 transform，所以用 closeBundle 后处理：
 *   - 版本 = `<pkg.version>+<毫秒时间戳>`：**包内无 .git**（git archive 交付），
 *     git sha 在对方机器上取不到 —— 时间戳保证连续两次构建版本必不同（AC-R4-01）；
 *   - 占位符缺失或 sw.js 不存在 → 抛错，构建以非 0 退出（AC-R4-03）：
 *     带着上一版版本号的 SW 会把用户钉死在旧缓存上，这种失败必须**响亮**。
 *
 * ⚠️ 落点取自 config.build.outDir，**不要硬编码 './dist'**：
 *   一旦用 `vite build --outDir X` 换目录，硬编码会去读上一轮遗留的 dist/sw.js
 *   （占位符早已被替换过）→ 构建在产物完全正确的情况下仍然失败，
 *   且报错信息指向"占位符缺失"，与真实原因（读错目录）完全不符。
 */
function swVersionInject(): Plugin {
  let outDir = 'dist'
  return {
    name: 'sw-version-inject',
    apply: 'build',
    configResolved(config) {
      outDir = config.build.outDir
    },
    closeBundle() {
      const swPath = fileURLToPath(new URL(`./${outDir}/sw.js`, import.meta.url))
      let code: string
      try {
        code = readFileSync(swPath, 'utf-8')
      } catch {
        throw new Error(`[sw-version-inject] ${outDir}/sw.js 不存在 —— public/sw.js 丢失或构建产物不完整`)
      }
      const PLACEHOLDER = '__SW_VERSION__'
      if (!code.includes(PLACEHOLDER)) {
        throw new Error(
          `[sw-version-inject] ${outDir}/sw.js 中找不到 ${PLACEHOLDER} 占位符 —— 版本号会被钉死，拒绝出产物` +
            `（若该目录是上一轮遗留产物，请先清空再构建）`,
        )
      }
      const version = `${pkg.version}+${Date.now()}`
      writeFileSync(swPath, code.replaceAll(PLACEHOLDER, version), 'utf-8')
    },
  }
}

export default defineConfig({
  plugins: [vue(), swVersionInject()],
  define: {
    __APP_VERSION__: JSON.stringify(pkg.version),
  },
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    host: true, // 监听所有网卡（支持 cloudflare 隧道等公网访问）
    allowedHosts: true,
    port: 5174,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:3000',
        changeOrigin: true,
        ws: true, // WebSocket 代理（/api/ws）
      },
      '/internal': 'http://127.0.0.1:3000',
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.spec.ts'],
  },
})
