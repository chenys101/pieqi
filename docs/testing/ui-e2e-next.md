# UI 真机验收：下个版本待办（2026-10-18 记）

> 背景与用法见 `docs/testing/ui-e2e.md`（本文件只记**还没做的事**）。
> 当前已落地：`web/e2e/` 两层（layout 数值断言 6 例 + visual 视觉基线 12 例），
> 全量 18 例连跑 4 次全绿。下面按性价比排序。

---

## P0 · 修「发送按钮被底栏盖住」（本轮测试发现的唯一真实可用性缺陷）

**现象**：矮视口下输入区装不下，`button[aria-label="发送"]` 的中心与四角被
`BottomNav` 的 `RouterLink`（`a.relative.flex`）命中 —— Playwright 的 `click()`
会直接报 "element intercepts pointer events"，**用户点不动发送按钮**。

**实测数字**（`sessionTimeline` / `composerBar` 的 getBoundingClientRect）：

| 视口 | 主区可用高 | 输入区高 | 结果 |
|---|---|---|---|
| 375×667（老 iPhone） | ~141px | 170px | 发送按钮被盖 |
| 1440×900（笔记本） | ~141px | 170px | 发送按钮被盖 |
| 375×844（竖屏手机） | ~318px | 170px | 可用 |

输入区 170px 的构成：`PromptInput` 的 `rows=2` textarea（实测约 149px）
+ `pb-[4.375rem]`（70px 让位行）。

**状态：实现已改（2026-10-18，并行会话），⚠️ 基线待更新。**

采用方向 1：`rows` 改 `1`（运行中保留 `2`）+ `autoResize` + `maxHeight=66`，
并给 `composer-bar` 加 `shrink-0`（硬约束：不加的话矮视口下被压掉的恰是发送按钮那一段）。
改动文件：`Textarea.vue` / `InterveneInput.vue` / `PromptInput.vue`。

> ⚠️ **验收未完成**：`e2e/baseline/layout-baseline.json` 里 `composerBar.height` 仍是
> 旧值 `170`、`composerInner.height` 仍是 `149`。按注释里的实测（66px ⇒
> composer-bar 74px / composer-inner 53px），几何断言**现在必然红**。
> 补最后一步：确认数字后跑 `npm run test:e2e:update-baseline`（视觉层同跑
> `update-snapshots`），再跑一遍全量确认 18 例全绿。

**改完的验收**：现有 layout 用例里 `composerSend` 的可操作性断言会立刻验证；
另建议补一条"点击发送后断言请求已发出"（见 P1）。

---

## P0 · 给 Dashboard 建基线

Dashboard 是首页，"待审批组 + 本周概览"是最高频首屏，**目前零基线**。
`docs/ui-design/UI-DESIGN-SPEC.md` 明确列了三条可度量指标（介入时延 / 验证成本 / 误判率），
现在一条都还没有测试守着。

做法：在 `e2e/layout/fixture.spec.ts` 与 `e2e/visual/fixture.spec.ts` 的 `SCENES` 各加一条
（夹具页需要新增 `mode=dashboard` 分支，挂真实的 Dashboard 组件 + 夹具数据）。

---

## P1 · 补「真点击」用例（当前只做静态几何，不做行为）

现在的 18 例全是"渲染出来之后量位置/比像素"，**没有任何一条真的点下去**。
而按钮能不能点、点了会发生什么，只靠几何采样是推不出来的（本轮已经把
`load-older-turns` 这类**宽按钮四角落在圆角外**造成的假红从清单里剔除，
正说明像素采样只能当"哨兵"，不能当"行为验证"）。

建议覆盖三条最短的幸福路径：
1. 「更早的 N 轮」→ 点击 → 断言更早的 Turn 被挂载（DOM 数量上升、`turn-group-1` 出现）；
2. 「允许一次」→ 点击 → 断言出现内联二次确认「确认允许」（L3 路径）；
3. 输入框输入 → 点「发送」→ 断言 `POST /api/tasks/:id/intervene` 已发出
   （mock 后端目前对写操作一律 405，需要给测试加**可观测**的写通道：
   记录请求而不是真的改数据，见 `mock-api.mjs` 顶部注释）。

---

## P1 · 线上复量接线（AGENTS.md 第 2 条纪律的后半段）

当前 `baseURL` 虽然是参数化的，但夹具页 `/__e2e/detail` **只在 dev 入口存在**，
所以这套用例**不能**直接指向线上。部署后复量需要另写一组只读用例：

- 只打真实页面（`/dashboard`、`/sessions/<真实任务 id>`），不依赖夹具；
- **只读**：不点审批、不提交、不发任何写请求（线上没有 405 兜底）；
- 断言的指标用"相对基线放宽的阈值"（线上数据量不同，DOM 节点数不可直接比），
  但**横向溢出、元素遮挡、主行动按钮可见**这三条与数据量无关，可以一字不改地复用到线上。

⚠️ 线上复量必须用独立凭据与只读身份，并且**先在小流量上跑一次**再接入日常流程。

---

## P2 · 扩展场景与视口

- 场景：设置页（`/settings`）、新建任务页（`/tasks/new`）—— 两者都含表单，
  是"标签挤压 / 按钮换行 / 输入框被键盘顶走"的高发区。
- 视口：补 320×568（最小 iPhone）与 1024×768（iPad 横屏）；桌面补 1920×1080。
  ⚠️ 每加一个视口都要**重拍全部视觉基线**（`npm run test:e2e:update-snapshots`）。

---

## P2 · 让 LCP 真正可用

现状：多数场景 `lcpMs: null`（拿不到 Largest Contentful Paint 条目），断言自动跳过 ——
首屏代价目前主要靠 DOM 节点数与几何断言兜底。**不要把它解读成"首屏很快"。**

排查方向：`PerformanceObserver` 是否需要在 `page.addInitScript` 里提前注册
（现在是在页面加载后读 `performance.getEntriesByType`，若条目被早期清理就拿不到）。

---

## P3 · 已知限制（不是待办，是必须知道的边界）

1. **跨机/跨通道基线会失配**：Windows+Chrome 生成的基线放到 Linux+Chromium CI 会全红。
   换环境必须重拍；`E2E_CHANNEL` 与 `maxDiffPixelRatio` 的调整都要同步重拍。
2. **全局横幅会让整屏下移 12~36px**：`App.vue` 的横幅由 auth 状态驱动，
   夹具页已显式清空 `appStore.banner`，但真实页面在两种状态下不可比。
3. **`src/main.ts` 一旦改写法要重新确认夹具入口**：`e2eEntryPlugin` 靠
   `index.html` 里的 `/src/main.ts` 字样替换入口，改不到就抛错（刻意响亮）。
4. **改 `tailwind.config.ts` 或夹具后必须重启 dev server**：
   `reuseExistingServer` 会复用旧进程，而 Tailwind 的内容扫描不会重扫 ——
   表现为"我的样式没生效"，且不报错。
