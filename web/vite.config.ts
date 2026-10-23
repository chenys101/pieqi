/// <reference types="vitest/config" />
import { defineConfig, type Plugin } from 'vite'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath, URL } from 'node:url'
import { readFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

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

/**
 * e2e 入口切换插件：`vite --mode e2e` 时把 index.html 的入口换成 `e2e/boot-fixture.ts`。
 *
 * 为什么需要独立入口（而不是在 src/main.ts 里注册夹具路由）：
 *   · 夹具注册必须在 `mount()` 之前同步完成，否则 `/__e2e/detail` 掉进主路由的
 *     兜底 `/:pathMatch(.*)*` 被**静默**重定向到 /dashboard；
 *   · 而把那段注册写进生产入口，就多了一条"静态 import 万一没被摇掉，夹具进 dist"的风险面。
 *     本仓库实测过动态 import 的两条歧路（Vite 的 `__vitePreload` 包装不解析 .mjs 默认导出；
 *     顶层 await 让注册晚于 mount），都不应该继续堆在生产入口里。
 *
 * 生产入口 `src/main.ts` 因此保持**零 e2e 痕迹**；`npm run build` 不受本插件影响
 * （apply: 'serve' 且只在 mode=e2e 下命中）。
 * 回归测试：`e2e/build-guard.spec.ts` 断言 dist 里没有 `__e2e`。
 */
function e2eEntryPlugin(): Plugin {
  let isE2E = false
  return {
    name: 'pieqi-e2e-entry',
    apply: 'serve',
    configResolved(config) {
      isE2E = config.mode === 'e2e'
    },
    transformIndexHtml: {
      // 必须在 vite 的内置 html 插件之前跑，否则模块脚本标签已经生成完了
      order: 'pre',
      handler(html) {
        if (!isE2E) return html
        const replaced = html.replace('/src/main.ts', '/e2e/boot-fixture.ts')
        if (replaced === html) {
          // 入口没换成功 = 跑的是生产入口 = 夹具路由不存在。必须响亮，
          // 否则表现是"页面正常但 /__e2e/detail 被重定向到 dashboard"这种静默失败。
          throw new Error("[pieqi-e2e-entry] index.html 里找不到 '/src/main.ts'，e2e 入口未切换")
        }
        return replaced
      },
    },
  }
}

/**
 * e2e 的 API 改道：`E2E_MOCK_PORT` 存在时把 /api 指向夹具后端（e2e/mock-api.mjs）。
 *
 * 为什么必须改道而不是靠 page.route 拦截：没被拦到的请求（新接口、WS 升级）会直接
 * 打到 :3000 的**现场** —— 现场有真任务和真 agent 进程，一次误触审批就是不可逆事故。
 * 端口是比"记得拦全"更可靠的隔离边界。
 *
 * 不设该变量时行为与之前完全一致（日常 `npm run dev` 照旧打 :3000）。
 */
const E2E_MOCK_PORT = process.env.E2E_MOCK_PORT
const apiTarget = `http://127.0.0.1:${E2E_MOCK_PORT || 3000}`

/**
 * ⚠️ e2e 模式下**必须把 optimizeDeps 预热建在系统临时目录**。
 *
 * 踩过的坑：dev 优化器默认把 `node_modules/.vite` 当作缓存目录，而用
 * `page.request.get('/src/main.ts')` 取到的转译结果是**半成品**——
 * 只剩三条 import（vue/pinia/App/router 全丢），浏览器执行时 `app` 未定义、
 * 页面白屏。清掉 `node_modules/.vite` 重建后立刻正常。
 * 根因是那个缓存目录被多个 vite 实例/多次被杀进程弄脏了（残留的 dep 清单与内存图不一致）。
 *
 * 判据：**一次性产物不要写进工作区**（AGENTS.md「缓存放哪里」）。
 * e2e 本来就有独立的临时产物目录，优化缓存跟着它走，每次跑都是干净的。
 */
const E2E_OPTIMIZE_CACHE = E2E_MOCK_PORT
  ? join(tmpdir(), 'pieqi-e2e-vite', String(process.pid))
  : undefined

export default defineConfig({
  plugins: [vue(), swVersionInject(), e2eEntryPlugin()],
  define: {
    __APP_VERSION__: JSON.stringify(pkg.version),
  },
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  // e2e 模式（E2E_MOCK_PORT 存在）下把依赖优化缓存挪到临时目录，避免被脏缓存带坏（见上）
  ...(E2E_OPTIMIZE_CACHE ? { cacheDir: E2E_OPTIMIZE_CACHE } : {}),
  server: {
    host: true, // 监听所有网卡（支持 cloudflare 隧道等公网访问）
    allowedHosts: true,
    port: 5174,
    proxy: {
      '/api': {
        target: apiTarget,
        changeOrigin: true,
        ws: true, // WebSocket 代理（/api/ws）
      },
      '/internal': apiTarget,
      //
      // 测试专有的观测口/开关，只在 e2e 模式（E2E_MOCK_PORT 存在）下挂：
      //   · /__writes  交互用例读写请求流水账（GET /__writes；DELETE 清空）
      //   · /__faults  夹具的时序/故障开关（见 e2e/mock-api.mjs 的 parseFaultQuery）。
      //                用例用 `page.request.post('/__faults?_delay=/api/settings:800')`
      //                造出"设置晚到"这类真实竞态 —— 浏览器直接 fetch 会被 CORS 拦住，
      //                走 vite 代理就等于同源。
      // 生产与日常 dev 都没有这两条路径，它们不该出现在真实服务上。
      ...(E2E_MOCK_PORT
        ? {
            '/__writes': { target: apiTarget, changeOrigin: true },
            '/__faults': { target: apiTarget, changeOrigin: true },
            // 路由落点：夹具页用 `?to=` 让浏览器**真的导航**到某条夹具路由
            // （例如 `/tasks/new?project=…`），而不用在夹具里重造一份路由表。
            '/__fixture': { target: apiTarget, changeOrigin: true },
          }
        : {}),
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  test: {
    environment: 'jsdom',
    // src 下是组件/逻辑的单元测试；`e2e/lib/**` 是**纯 Node 的纯函数**（失败结论聚合、
    // 基线读写）。后者是一段"只有红的时候才执行"的代码，必须由单元测试兜住 ——
    // 放进 src 不合适（它不属于产品代码），所以这里显式多收一个目录。
    include: ['src/**/*.spec.ts', 'e2e/lib/**/*.spec.ts'],
  },
})
