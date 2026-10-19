// 真机验收（Playwright）配置。
//
// 设计判据（为什么每一项是现在这个值）见 docs/testing/ui-e2e.md。三条硬约束：
//
// ① **隔离端口**：baseURL 指向 :3100 的 vite + :3101 的夹具后端。
//    绝不指向 :3000 的现场 —— 现场有真任务与真 agent 进程，一次误点审批就是
//    「本机任意代码执行」，这在 e2e 里是不可逆事故，不是脏数据。
// ② **通过的用例零产物**：screenshot / video 只在失败时留（only-on-failure）。
//    这是"每次改动都要重新截图、图还进上下文"的成本开关。
// ③ **视觉用例串行、数值用例并行**：截图对并发敏感（抖动会让 diff 假红），
//    而 DOM/LCP 断言互不干扰，可以吃满 CPU。

import { defineConfig } from '@playwright/test'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

/**
 * 运行产物（trace / 失败的 actual+diff / HTML 报告）的落点：**系统临时目录**。
 *
 * 为什么不放仓库里（`e2e/.artifacts`）：它属于"一次性产物"，
 * 仓库里放一次性产物会积攒、会被 git status / checkpoint 快照扫到、
 * 还要额外写 .gitignore 去挡 —— AGENTS.md「缓存放哪里」那条纪律的判据正是这个。
 * 每次跑之前先清空（`rmdir` + 重建），所以它永远是"最近一次失败"的证据，不会越攒越多。
 * 系统临时目录本身由 OS 定期清理；这里再显式清一次是为了避免旧 trace 被误读成本次的失败。
 *
 * 需要保留时：设 `E2E_OUTPUT_DIR` 指向你想放的地方。
 */
const OUTPUT_DIR = process.env.E2E_OUTPUT_DIR || join(tmpdir(), 'pieqi-e2e')

const VITE_PORT = Number(process.env.E2E_VITE_PORT || 3100)
const MOCK_PORT = Number(process.env.E2E_MOCK_PORT || 3101)

/**
 * 浏览器通道：默认用**系统安装的 Chrome**（本机已有），而不是下载的 chromium。
 *
 * 为什么不默认用 chromium：
 *   · `playwright install chromium` 要拉约 130MB，无代理/网络受限时直接卡死（实测踩过）；
 *   · `channel: 'chrome'` 与 chromium 的 API 完全一致（同内核），断言与截图行为相同；
 *   · CI 想用自带的 chromium 只需设 `E2E_CHANNEL=chromium`（此时仍需 install）。
 *
 * 基线图的生成环境必须**稳定**：改这个变量会改变字体渲染，视觉基线要重拍。
 */
const CHANNEL = process.env.E2E_CHANNEL || 'chrome'
const channelOpt = CHANNEL === 'chromium' ? {} : { channel: CHANNEL }

export default defineConfig({
  testDir: './e2e',
  outputDir: OUTPUT_DIR,

  // 用例自己的超时：18s 是"页面渲染 + 量数字"的保守上限；慢到超出说明有真问题，不该等
  timeout: 18_000,
  expect: { timeout: 5_000 },

  // 失败重跑 0 次：视觉/数值用例重跑会把"偶发"变成"绿"，而我们要的恰恰是让它红
  retries: 0,
  forbidOnly: !!process.env.CI,
  // 默认 reporter 是 list：数字直接打在终端上，"结论就是输出"，不需要开 HTML 报告。
  // CI 才额外产出 html（放临时目录，不进仓库）。
  reporter: process.env.CI
    ? [['github'], ['html', { open: 'never', outputFolder: join(OUTPUT_DIR, 'report') }]]
    : [['list']],

  use: {
    baseURL: `http://127.0.0.1:${VITE_PORT}`,
    ...channelOpt,
    // ★ 关键：通过的用例不留任何图片/视频。失败的才留 —— 而且只留 trace（信息量远大于截图）
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'off',
    // 主题固定浅色：'system' 会跟随机器设置，换一台机器基线就全红
    colorScheme: 'light',
    /**
     * 减少动画 = 稳定性三件套之一（另两件：固定夹具数据、串行 worker）。
     *
     * ⚠️ 本机装的是 Playwright 1.62，它的 `use` 类型里**没有**顶层 `reducedMotion`
     * （只有 `colorScheme` 等；`reducedMotion` 只在 `contextOptions` 里）。
     * 而 `reducedMotion` 恰恰是**允许在测试间变化**的少数选项之一 ——
     * 放进 `contextOptions` 与直接写等价，且能通过类型检查。
     * （同时也仍在 `stabilize()` 里注入 CSS 关动画，双保险：不依赖浏览器偏好是否生效。）
     */
    contextOptions: { reducedMotion: 'reduce' },
    viewport: { width: 1440, height: 900 },
    // 每个用例都是全新上下文：localStorage / sessionStorage 不会互相污染
    // （主题、正文宽度这类"设备级偏好"存 localStorage，复用上下文会让基线漂移）
    storageState: undefined,
  },

  projects: [
    {
      // ① 数值断言：**不产图**，只回传数字
      name: 'layout',
      testDir: './e2e/layout',
      /**
       * ⚠️ **必须串行（workers: 1）**。实测证据：6 worker 并发时，每三轮里约有两次会量到
       * `sessionTimeline` 宽 1156（应 1109）、或「更早的 N 轮」按钮若隐若现 ——
       * 那是冷启动下字体/布局尚未定型的时间窗被并发放大，**不是产品回归**。
       * 代价只是 wall time 多 1~2 秒（本就是 3 视口 × 2 场景的小集合），
       * 换来"读数可复现"—— 对基线类断言这是唯一正确的取舍。
       */
      fullyParallel: false,
      workers: 1,
      // 长会话 fixture 每次加载都要挂 12 轮，15s 是给慢机器的余量（真慢会红，不该被重试掩盖）
      timeout: 25_000,
      use: {},
    },
    {
      // ② 视觉回归：串行 + 固定数据。截图对并发敏感
      name: 'visual',
      testDir: './e2e/visual',
      fullyParallel: false,
      workers: 1,
      snapshotPathTemplate: '{testDir}/__screenshots__/{projectName}/{arg}{ext}',
      use: {},
    },
    {
      /**
       * ③ 交互（真点击）：**真的点下去**，断言状态变化与请求发出。
       *
       * 与前两层的判据不同 —— 那两个量的是"渲染结果"，这一层量的是"行为"：
       * 按钮看得见、点得到，证明不了 onClick 绑没绑、参数对不对。
       *
       * 串行的原因：用例共用夹具后端的写请求流水账（/__writes），
       * 并发会让它们互相读到对方的请求（beforeEach 的清空也会互踩）。
       */
      name: 'interaction',
      testDir: './e2e/interaction',
      fullyParallel: false,
      workers: 1,
      timeout: 20_000,
      use: {},
    },
    {
      /**
       * ④ 线上复量：打**真实服务**，验证"部署后这屏还能用"。
       *
       * 见 docs/testing/ui-e2e-online.md。三条纪律：
       *   · **不参与默认 `npm run test:e2e`**（它是发版动作，不是每次改代码都跑）；
       *   · 必须显式给 `ONLINE_BASE_URL`，**默认指向 127.0.0.1:3000**（本机），
       *     不默认任何公网地址 —— 防止一次疏忽把只读用例打到公网实例；
       *   · 只读放在**机制**上（用例内拦截所有非 GET），不靠"记得别点"。
       */
      name: 'online',
      testDir: './e2e/online',
      fullyParallel: false,
      workers: 1,
      // 线上可能有冷启动/隧道回源，给宽一点
      timeout: 30_000,
      use: {
        baseURL: process.env.ONLINE_BASE_URL || 'http://127.0.0.1:3000',
        // 线上复量**不做截图**：数据每天变，比像素必然全红，那种红没有信息量。
        trace: 'off',
        screenshot: 'off',
        video: 'off',
      },
    },
    // 说明：三视口在用例里用 page.setViewportSize 显式设置，**不**开三个 project ——
    // 一个用例覆盖三视口 = 一次启动、三次量测；而且"同一份夹具在三种宽度下的表现"
    // 正是要放在一起对比的东西，拆成三个 project 反而要额外聚合才能比。
  ],

  /**
   * 夹具层的两个服务（mock 后端 + dev server）：常驻复用。
   *
   * ⚠️ online project **不需要**它们（它打的是真实服务），但 Playwright 的
   * `webServer` 是**全局**的、没有"按 project 生效"的开关 —— 单独跑 online 时
   * 它们也会被拉起。代价是几秒启动（`reuseExistingServer` 会复用已存在的），
   * 不会影响断言。如果哪天觉得碍事，用 `ONLINE_ONLY=1` 跳过：
   * 判据是"跑 online 时不该需要夹具服务"，而不是"少等几秒"。
   */
  webServer: process.env.ONLINE_ONLY
    ? undefined
    : [
        {
          command: `node e2e/mock-api.mjs`,
          port: MOCK_PORT,
          reuseExistingServer: !process.env.CI,
          timeout: 30_000,
          env: { E2E_MOCK_PORT: String(MOCK_PORT) },
          stdout: 'pipe',
          stderr: 'pipe',
        },
        {
          // --mode e2e：入口换成 e2e/boot-fixture.ts（见 vite.config.ts 的 e2eEntryPlugin）。
          // 生产入口 src/main.ts 保持零 e2e 痕迹。
          command: `npx vite --mode e2e --port ${VITE_PORT} --strictPort`,
          port: VITE_PORT,
          reuseExistingServer: !process.env.CI,
          timeout: 90_000,
          // 夹具后端端口注入给 vite 代理（见 vite.config.ts 的 e2e 分支）
          env: { E2E_MOCK_PORT: String(MOCK_PORT) },
          stdout: 'pipe',
          stderr: 'pipe',
        },
      ],
})
