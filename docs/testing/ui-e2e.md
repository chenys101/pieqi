# UI 真机验收（Playwright）

> 这一层解的不是"AI 会不会写 CSS"，而是**AI 的验证通道是图片**带来的三个成本：
> 图片 token、每轮重复截图、以及"测试绿灯了"却不等于验收。
>
> 适用场景：任何改了 UI 的改动。**单元测试（vitest）不替代本层** ——
> jsdom 量不出尺寸、遮挡、滚动、首屏渲染代价（AGENTS.md「UI 改动的验收纪律」第 1 条）。

---

## 1. 一图流

```
改 UI
  │
  ├─ npm test                     ① 逻辑层（vitest，秒级，零图片）
  │
  ├─ npm run test:e2e:impact      ② 按改动路径**只跑相关的**（脚本打印并执行）
  │     └─ layout project         L0 数字断言：DOM/遮挡/溢出/LCP/CLS（**不产图**）
  │        └─ 失败 ⇒ 断言消息即诊断（"按钮被 xxx 遮挡"），不需要看图
  │
  ├─ npm run test:e2e:visual      ③ 视觉回归（toHaveScreenshot，机器 diff）
  │     └─ 失败 ⇒ expected/actual/diff 三张图留在 .artifacts，**不喂给模型**
  │        └─ 需要判断"设计变更还是 bug" ⇒ npm run test:e2e:vision（只回一句结论）
  │
  └─ 部署后再跑一次 layout（线上复量）
```

三层分工：

| 层 | 工具 | 管什么 | 模型看图吗 |
|---|---|---|---|
| 逻辑 | vitest（`src/**/*.spec.ts`） | 行为、状态机、格式化 | ❌ |
| 布局 | Playwright `--project=layout` | 尺寸/遮挡/溢出/首屏代价/DOM 量 | ❌（只回数字） |
| 视觉 | Playwright `--project=visual` | 像素级外观回归 | ❌（机器 diff） |
| **交互** | Playwright `--project=interaction` | **真的点下去**：请求发没发、参数对不对、状态变没变 | ❌ |

> **交互层与前两层的判据不同**：前两层量"渲染结果"，交互层量"**行为**"。
> "按钮看得见、点得到"证明不了 onClick 绑没绑、参数对不对、请求发没发 ——
> 所以需要 `e2e/interaction/`（5 例：审批 / L3 二次确认 / 展开更早轮次 / 发送 /
> **模型选择器关闭后键盘输入仍进输入框**）。
> 它靠 mock 后端的 `/__writes` 流水账观测（**记录写请求但不改数据**，
> 避免 mock 的副作用与我们的点击抢同一个因果）。
> ⚠️ 这一层的选择器与断言坑最多（层级写错会点到别的卡、跨元素正则会超时、
> mock 的 DELETE 路由会掉进 GET 分支），详见 `ui-e2e-next.md` 的四条记录。

---

## 2. 三个问题的对应解法

| 问题 | 解法 | 代码位置 |
|---|---|---|
| ① 截图 → 图片 token | **默认不截图**：DOM / 遮挡 / 溢出 / LCP 全用数值断言；必须看图时走 `vision.mjs` 旁路（只把一句结论带回上下文） | `e2e/lib/measure.ts`、`e2e/vision.mjs` |
| ② 多次修改重复截图 | 基线进 git + `toHaveScreenshot` 自动 diff（机器判"变了没有"）；影响面脚本只跑相关用例；通过的用例零截图产物 | `e2e/visual/`、`e2e/impact.mjs`、`playwright.config.ts` |
| ③ 效率 | dev server 常驻复用（`reuseExistingServer`）；数值层三视口并行；视觉层串行（稳定优先）；失败只留 trace | `playwright.config.ts` |
| ④ **结论只有一份** | 所有失败收敛成 `SUMMARY.md`：按签名聚合 + 与上一轮对比（已修好 / 仍红 / 新挂 / 保持绿）。**跑完只读它** | `e2e/lib/report.ts`、`e2e/reporter.ts`、`e2e/show-summary.mjs` |
| ⑤ **环境先分诊** | `--project=health` 先证明夹具后端 / dev server / 浏览器是活的；红在这一层 = **环境问题，不要改源码** | `e2e/health/env.spec.ts`、`e2e/safe-run.mjs` |

### 2.1 为什么需要 ④⑤（2026-10-19 复盘）

修一个「输入框被模型选择器遮住」的 UI 问题，实际消耗了 **473 次模型调用、近 1 亿 token**。
逐条归因（从会话事件流里量出来的，不是估计）：

| 归因 | 占全部消耗 |
|---|---|
| 同一组工具调用**连续重试**（`edit` 连打 13 次、`playwright` 连打 10 次、`vitest` 9 次…） | **45.8%** |
| 把**环境故障**当成产品缺陷（30 分钟超时 ×133 次、`spawn EPERM`、vite/mock 没起来） | 大量墙钟 + 多轮改错方向 |
| 反复读**同一批测试输出** | 每次都带着 20–30 万上下文重读 |

结论：重复的根源不是截图，是**反馈的单位是一条命令的输出**。于是：

```bash
npm run test:e2e:safe     # ① health 分诊（4 秒）→ 绿了才跑三层
npm run test:e2e:summary  # ② 只读这一段结论
```

`SUMMARY.md` 长这样（6 个用例失败，收敛成 2 条）：

```markdown
# e2e FAIL  12/18 通过 | 2 条失败签名 / 6 个用例 | 已修好 0 / 仍红 0 / 新挂 2

## 失败（按签名聚合）
- 动作行压在输入框内部（gap=-103px）
  - 出现 3 次：newtask @ mobile (375×667)、newtask @ tablet (834×1112)、newtask @ desktop (1440×900)
  - 出处 e2e/layout/fixture.spec.ts:578
```

**迭代循环的纪律**（`impact.mjs` 会把第 ① 步的命令直接打出来，不用自己拼 `-g`）：

1. 只跑**刚红的那几条**（`-g` 精确到用例名，几秒）；
2. 绿了以后才跑**一次**完整的影响面集合；
3. 读 `SUMMARY.md` 的"已修好 / 仍红 / 新挂"，不要读逐条输出；
4. **只有**数字看着对、但画面确实不对时，才看**一张**失败截图。

---

## 3. 隔离原则（**硬约束**）

| 边界 | 值 | 为什么 |
|---|---|---|
| 前端 | `:3100`（vite，`--strictPort`） | 绝不与 `:5174` 的日常 dev、`:3000` 的现场共用端口 |
| API | `:3101`（`e2e/mock-api.mjs`） | **夹具后端只读，写操作一律 405**；WS 升级显式拒绝 |
| 数据 | 内存夹具，**不碰 `~/.pieqi`** | 现场有真任务与真 agent 进程，一次误触审批 = 本机任意代码执行 |

`vite.config.ts` 里的改道只在 `E2E_MOCK_PORT` 存在时生效 —— 日常 `npm run dev` 行为不变。

```ts
const E2E_MOCK_PORT = process.env.E2E_MOCK_PORT
const apiTarget = `http://127.0.0.1:${E2E_MOCK_PORT || 3000}`
```

> 为什么不用 `page.route` 拦截代替：没被拦到的请求（新接口、WS 升级）会**打到现场**。
> 端口是比"记得拦全"更可靠的边界。

---

## 4. 目录与角色

```
web/
├─ playwright.config.ts          # 两个 project：layout（并行）/ visual（串行）
├─ tsconfig.e2e.json             # e2e 的类型检查（typecheck 会连带跑它）
├─ e2e/
│  ├─ boot-fixture.ts            # ★ e2e 专用入口（vite --mode e2e 时替换 index.html 的入口）
│  ├─ mock-api.mjs               # 夹具后端（:3101，只读）
│  ├─ fixtures/
│  │  ├─ api-payloads.mjs        # ★ 夹具数据的唯一来源（mock 与用例共用）
│  │  └─ page-fixture.ts         # 夹具页组件 + **独立路由表** fixtureRouter（只认 /__e2e/detail）
│  ├─ lib/
│  │  ├─ measure.ts              # L0 采集：一次 evaluate 拿全部数字
│  │  └─ baseline.ts             # 数值基线的读写（按 key 合并，不整文件覆盖）
│  ├─ layout/fixture.spec.ts     # 数值断言（三视口 × 两场景）
│  ├─ visual/fixture.spec.ts     # 视觉回归（三视口 × 两场景 × 双主题）
│  ├─ visual/__screenshots__/    # ★ 视觉基线（**提交进 git**）
│  ├─ baseline/layout-baseline.json  # ★ 数值基线（**提交进 git**）
│  ├─ impact.mjs                 # 改动路径 → 用例映射
│  ├─ vision.mjs                 # 视觉旁路（只回文字结论）
│  └─ update-baseline.mjs        # 跨平台设置环境变量后跑基线回写
```

### 为什么要有 `/__e2e/detail` 这个页面

`web/src` 里没有"12 轮会话 + 待审批卡"形状的可直达页面。直接打 `/sessions/<id>`，
页面在 store 里找不到任务就会渲染**空态** —— 看起来测通了，实际测的是一屏空壳。

该路由只做两件事：把夹具经**生产适配器 `adaptTask`** 灌进 store，然后挂载**真实的生产组件**
（`SessionTimeline` / `InterveneInput` / `ApprovalCard` / `FeedbackPanel`）。

> ⚠️ 不要在夹具页里"简化样式"。放宽 class 或省略容器，量出来的就不是生产尺寸，基线失去意义。

### 生产入口为什么是干净的（以及这里踩过的三条歧路）

`src/main.ts` **完全不含 e2e 代码**。夹具路由通过 `vite --mode e2e` 把入口换成
`e2e/boot-fixture.ts` 来安装（`vite.config.ts` 的 `e2eEntryPlugin` 改写 `index.html`
的 `<script src>`，改写失败直接抛错）。

这不是洁癖，是实测出来的。把注册写进 `src/main.ts` 试过三种写法，**全部失败**，
而且**失败时一行错都没有**（页面正常渲染、控制台干净，只有一个"等 testid 超时"）：

| 写法 | 失败形态 | 根因 |
|---|---|---|
| `void import(...).then(...)` | `/__e2e/detail` 掉进主路由兜底 `/:pathMatch(.*)*`，被静默重定向到 `/dashboard` | import 异步，`app.use(router)` 与 `mount()` 同步执行，注册晚于首轮路由解析 |
| 加**顶层 await** | 同上，且更难查 | Vite 给含顶层 await 的模块发三条独立 script，求值时机不可控 |
| 变量路径 + `@vite-ignore` | 同上 | 只换了问题形态，没有解决"注册必须在 mount 之前" |

结论：**注册必须在 `mount()` 之前同步完成**；既要保证这一点又不污染生产入口，
唯一干净的做法就是**换入口**。夹具页用**独立路由表**（只认 `/__e2e/detail`，没有兜底可掉），
掉不进去就渲染不出东西 —— 失败是响亮的。

### 生产构建的守卫

`npm run build` 后，`dist/` 里**不得**出现 `__e2e` / `fixtureRouter` / `page-fixture` 字样
（已验证：产物零痕迹）。生产入口干净 + 产物扫描，是这条约定的两条独立证据。

---

## 5. 常用命令

```bash
cd web

npm run test:e2e:install          # 可选：装 chromium（约 130MB）。默认用系统 Chrome，不需要它
npm run test:e2e:layout           # 数值层（快，最常跑）
npm run test:e2e:visual           # 视觉回归（需已有基线）
npm run test:e2e                  # 三层全跑
npm run test:e2e:impact          # 按本次改动选跑哪些（推荐日常用这个）

# ★ 2026-10-19 新增（复盘出来的两条，日常用这两条）
npm run test:e2e:safe             # 先 health 分诊（4s），绿了才跑三层 —— 环境坏时不要改源码
npm run test:e2e:health           # 只做环境分诊：夹具后端 / dev server / 浏览器
npm run test:e2e:summary          # 打印上一轮的结论（SUMMARY.md），不必记路径

node e2e/impact.mjs --print       # 只看会跑什么，不执行
node e2e/impact.mjs --base=main   # 相对某个 ref 的改动
npm run test:e2e:vision           # 视觉旁路（需 VISION_API_KEY，见 §7）
```

**更新基线**（只在人确认过数字之后）：

```bash
npm run test:e2e:update-snapshots   # 视觉：重拍基线图，然后提交 __screenshots__/
npm run test:e2e:update-baseline    # 数值：把本次实测写回 layout-baseline.json
```

> ⚠️ **不要**在断言失败时用上面两条命令"消红"。那等于把回退固化成期望值。

---

## 6. 断言清单（layout 层）

每个指标对应一条**真实会出事的故障**，不是"顺手测一下"：

| 指标 | 判据 | 对应故障 |
|---|---|---|
| `hOverflow` | `=== 0` | 移动端整页横向拖动（1100px 正文列 + 375 屏的经典事故） |
| `domNodes` | `< 预算` | 按需挂载失效（12 轮全挂 = 21,206 → 2,135 那条基线） |
| `lcpMs` | 有值时 `< 2500ms` 且相对基线 `< +35%` | 首屏代价悄回退 |
| `cls` | `< 0.05` | 字体/数据落地后元素跳位 |
| `interactive.blocked` | `=== []` | **要点的按钮被浮层拦住**（与 Playwright `click()` 同一套 5 点采样判据） |
| `boxes[*]` vs 基线 | 宽/高差 `<= 1px` | **布局平移/挤压**（只有上界阈值抓不到，见下） |
| 场景：detail | `composer-bar.intersects` | 续问入口被整块顶出屏幕 |
| 场景：detail | 「发送」按钮 `inViewport` | 用户无法完成续问 |
| 场景：approval | 「允许一次」按钮 `inViewport` | 要滚动才能决策（违反 SPEC「One Screen, One Decision」） |
| 控制台 | 无 `error`/`pageerror`（仅忽略 WS 握手与 favicon/SW 噪声） | 静默失败高发源（401 / 接口 404 / 未捕获异常） |

### 为什么"几何必须与基线一致"是**必需**的一条

上界断言（超预算、超预算的超时、有横向溢出）只能抓**量级**变化。
实测踩到的反例：给夹具外壳加 40px 左内边距，`sessionTimeline` 从 `503×942` 变成
`463×942`（整体右移 40px、正文列被压窄），**所有上界断言照样全绿**。
所以必须有一条"尺寸等于基线"的断言 —— 它才是布局回归的主力。

### 遮挡判定的正确形态

不要对**容器**查"中心点是否被盖住"：
- 容器的中心点几乎总被自己的子元素命中（那叫内容，不叫遮挡）；
- `pointer-events-none` 的布局行（如 `composer-actions`）采样点会穿透到下层 —— 那是设计；
- 宽按钮的四角会落在圆角外。

正确做法：只对**自身就是点击目标、常驻可见的小元素**做 5 点采样
（发送按钮、「回到最新」、可拖拽手柄），判据与 Playwright `click()` 一致。
需要验证"更早的 N 轮"这类按钮，就写一条**真点击**的用例，别靠像素采样猜。

**失败信息就是诊断**。例如：

```
sessionTimeline 宽度与基线不符：503 → 463（布局被挤压/拉伸，±1px 内视为相同）
以下元素会被别的元素拦截点击：· composerSend @(323,646) 命中的是 a.relative.flex
以下元素会被别的元素拦截点击：· composerSend @(323,646) 命中的是 a.relative.flex
DOM 节点数 21046 超出预算 6000：可能"更早的 Turn"被全量挂载了
整页横向溢出 32px（scrollWidth=407 > clientWidth=375）
```

这些行几乎等价于三张截图的结论。

---

## 7. 视觉旁路（L3）：什么时候用、怎么用

**只在需要判断"这个差异是设计变更还是 bug"时用**，平时不碰。

```bash
# 需要配：VISION_API_KEY / VISION_BASE_URL / VISION_MODEL（OpenAI 兼容）
npm run test:e2e:vision
# 或单张（失败产物默认落在系统临时目录 $TMP/pieqi-e2e/，可用 E2E_OUTPUT_DIR 改）
node e2e/vision.mjs "$env:TEMP\pieqi-e2e\<用例目录>\test-failed-1.png" "检查是否有元素重叠"
```

设计要点：
- **给的是故障清单，不是"描述这张图"** —— 描述会把图变成长篇文字，token 又回来了；
- 未配置密钥时**退出码 2 并报错**，不静默跳过（跳过会让你以为图已经看过了）；
- 输出只有几行文字，图留在本地。

---

## 8. 稳定性三件套（视觉回归 90% 的假红都来自这里）

1. **关动画**：`stabilize()` 注入 `animation/transition: none` + `contextOptions.reducedMotion: 'reduce'` + `caret: 'hide'`
   （⚠️ 本机 Playwright 1.62 的顶层 `use` **没有** `reducedMotion`，只能走 `contextOptions`）；
2. **固定数据**：`mock-api.mjs` 只读夹具；用例不请求真实后端；
3. **串行 + 固定主题**：`visual` project `workers: 1` / `fullyParallel: false`；`colorScheme: 'light'`，
   深色靠 `addInitScript` 写 `localStorage['pieqi.theme.pref']`，并在用例里**显式断言** `data-theme` 生效；
4. **滚动位置归一**：加载后先滚到底再量（见 §6）—— 会话页可能停在顶部或底部，
   不归一化时 DOM 数量与截图会在两个状态间跳；
5. **横幅状态归零**：夹具页显式把全局横幅设为 `null`。生产里它是 `auth/status` 回来后才设置的，
   夹具不打接口 ⇒ 不显式清掉就会**时有时无**（实测出现过两次基线不一致，差 12px）。

> 跨机注意：本地 Windows 与 Linux CI 的字体渲染不同，**基线必须在与 CI 相同的环境生成**，
> 否则会全红。若必须在两边跑，用 `maxDiffPixelRatio` 放宽到 0.01 并在 CI 里单独生成一份基线。
>
> 同理，**改 `E2E_CHANNEL`（chrome ↔ chromium）会让视觉基线全部失配** —— 换通道就要重拍基线。

---

## 9. 环境坑

| 现象 | 原因 | 处理 |
|---|---|---|
| `playwright install chromium` 卡住 / 140MB 包下不动 | 无外网代理或 CDN 慢（本机实测：5 分钟零进展） | 已在配置里默认 `channel: 'chrome'` 用系统 Chrome，无需下载；CI 想用自带 chromium 设 `E2E_CHANNEL=chromium`（但基线要重拍） |
| `npm run build` / `npm test` / `npx playwright test` 报 `EPERM` | 受限沙箱下 esbuild/vite/Chrome 需要命名管道 | 按仓库纪律**提权申请**，不要改命令形态（详见 AGENTS.md） |
| 视觉/几何用例全红但人看着一样 | 字体渲染/抗锯齿差异，或换了浏览器通道 | 确认基线与运行环境一致；必要时把 `maxDiffPixelRatio` 放到 0.01，或重拍基线 |
| **改了 `tailwind.config.ts` /夹具代码后，`reuseExistingServer` 复用了旧 dev server** | 之前起的 vite 还在 :3100，Tailwind 的内容扫描**不会**重扫 | 关掉旧进程再跑（`netstat -ano \| grep :3100`）。`--strictPort` 会让端口冲突显式报错，但"复用了旧服务"不会报错 —— 这是本层最容易误判的地方 |
| 夹具里写的 Tailwind class 「不生效」 | `tailwind.config.ts` 的 `content` 没包含 `./e2e/**` | 已在配置里加上；若有人移除，夹具里的类会**根本没有 CSS**，回归静默失效（实测踩过） |

### 浏览器通道：默认用系统 Chrome

`playwright.config.ts` 里 `E2E_CHANNEL` 默认 `chrome`（`chromium` 与 `chrome` API 完全一致）：

```ts
const CHANNEL = process.env.E2E_CHANNEL || 'chrome'
const channelOpt = CHANNEL === 'chromium' ? {} : { channel: CHANNEL }
use: { ...channelOpt }
```

本机已装 `C:\Program Files\Google\Chrome\Application\chrome.exe`，网络受限时直接可用。
**换通道会改变字体渲染 ⇒ 视觉基线必须重拍。**

---

## 9.5 已知问题（本轮实测暴露，尚未修）

这些是**产品/尺寸层面**的问题，不是测试框架问题。记在这里是为了下次看到相关失败时
不要误判成 flaky：

### 会议/输入区高度 vs 矮视口

`InterveneInput` 在 375×667 与 1440×900 下**装不满**视口：
`rows=2` 的 textarea（约 149px）+ `pb-[4.375rem]`（70px 让位）= **170px**，
而可用主区只有约 141px。表现是输入区在 flex 列里被压、**发送按钮被底栏盖住**
（`composerSend` 被 `a.relative.flex`（BottomNav 的链接）拦住点击）。

布局层已把判据从"输入区整块可见"收紧为"**发送按钮完整可见 + 不被拦截**" ——
后者才是"能不能用"，前者在当前几何下任何实现都满足不了。真要修，方向是让输入区
按剩余空间自适应（例如 textarea 用 `autoResize` + `maxHeight` 而不是固定 `rows`），
**但那会改变竖屏手机的手感，属于产品决策，不该由测试顺手改掉。**

### 全局横幅会整体下移 12~36px

`App.vue` 的横幅（"系统尚未绑定飞书管理员账号…"）是两行橙条。夹具页已显式清空
`appStore.banner`，但**生产页面**只要 auth 状态变化，整屏元素就会整体下移。
任何跨这两个状态的视觉基线都不可比。

### LCP 可能拿不到

见 §4 的说明：`lcpMs: null` 时断言跳过，由 DOM 与几何断言兜住首屏代价。

---

## 10. 与既有纪律的关系

- **提交必须在重启之前**（AGENTS.md「UI 改动的验收纪律」第 3 条）：e2e 不改变这条。
- **部署前用待部署的那一份再验一次，部署后线上复量**：本层当前只在 :3100（dev 入口）跑。
  线上复量**尚未接线**：需要"线上只读"的一组用例（不写操作、不打夹具、显式允许只读断言），
  不能把 dev 层的 spec 直接指到线上 —— 夹具页 `/__e2e/detail` 在生产入口里根本不存在。
- **隔离端口 + 独立数据目录**：已由 :3100 / :3101 / 内存夹具落实。
- 本层**不引入** Web IDE / Computer Use / 录屏（ADR-0007 非目标清单不受影响）；
  视觉采集服务已于 ADR-0013 整体移除，本层是**测试侧**能力，不复活产品侧的视觉反馈功能。

---

## 11. 一次真实改动的完整流程（示例）

```bash
# 改了 web/src/features/approval/components/ApprovalCard.vue
cd web
npm test                              # 逻辑层（秒级）
npm run test:e2e:impact               # → 判定"审批卡改动"，只跑 -g approval 的两层
#   [impact] 判据：审批卡
#   [impact] 执行：npx playwright test --project=layout -g approval --project=visual -g approval

# layout 全绿 + visual 有一处 diff：
#   → 看图判断是"设计变更"还是"回退"
#   → 设计变更：审过 diff 后 npm run test:e2e:update-snapshots 并提交基线
#   → 回退：改代码，重跑

git add -- web/src/features/approval/components/ApprovalCard.vue \
           web/e2e/visual/__screenshots__/visual/approval-*.png
git commit -m "feat(approval): ..."
# 再走 AGENTS.md 的发版四步（前端 build → go build → 投递 → 触发重启）
```

**关键点：整个循环里没有一张图进入模型上下文。**
