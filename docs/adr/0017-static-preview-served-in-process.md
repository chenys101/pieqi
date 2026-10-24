# ADR-0017：静态产物预览走进程内 serve，不 spawn dev server

- 状态：**已采纳**（2026-10-24）
- 影响面：`internal/core/preview.go`（discovery / 生命周期）、`internal/api/preview.go`（代理分流）、
  agent 产物约定 `.pieqi/preview/`

## 背景

UI 迭代里让用户**选方案**，需要一个能点开的对比页（多套头部布局/交互并排、点卡片切换）。
这类产物是纯静态 HTML：没有 `package.json`，没有 dev server 可跑。

而 preview 原本只认 dev server：

1. `DiscoverPreview` 没有 `package.json` 直接判 `unavailable` ⇒ 页面根本打不开；
2. 即便用 `.pieqi/preview.json` 塞一条静态服务器命令，也仍要 spawn 一个进程（node/python 静态服务器
   常驻 15-60MB），且 **`WatchBus` 在任务 completed/failed/cancelled 时会把 preview 收掉** ——
   用户过一会儿回来想再看方案页，已经 503 了。

方案页的生命周期本质上不同于 dev server：它是**看完就算数的静态产物**，不需要常驻，也不该随任务结束消失。

## 结论

**静态产物由 pieqi 进程内直接 serve，不 spawn 任何进程。**

- 约定目录：worktree 下 `.pieqi/preview/`，含至少一个 `.html` 即判定 `framework: "static"`
  （判定排在 `package.json` 之前——一个明确的产物目录比"猜该跑哪个 dev server"更可信）。
- 覆盖：`.pieqi/preview.json` 可写 `{"framework":"static","dir":"<相对路径>"}` 换目录。
- 代理时分流：`PreviewManager.StaticDir(taskID)` 非空 → `serveStaticPreview` 读盘返回，
  不再走 `httputil.ReverseProxy`。
- 生命周期：`Start` 直接置 `running`（无启动期、无端口）；`Stop` 只落状态；
  **任务终态不回收 static**（`stopIfProcess` 只收有子进程的实例）；`task_deleted` 才 `Forget`。
- HTML 一律注入 `<base href="/preview/<id>/">` 并把根绝对 `src|href="/..."` 前缀化（与 reverse proxy
  共用同一份改写逻辑，避免两处 base 规则漂移），响应 `Cache-Control: no-store`。

## 补记：目录约定不能成为正确性前提（2026-10-24 同日）

初版只认一个字面路径 `.pieqi/preview/`，实际用起来有两个坑（都是"位置"惹的）：

1. **用户每轮都要在 prompt 里交代路径** —— 否则 agent 按自己的习惯写；
2. **agent 的惯性落点很分散**：`previews/`、`proposals/`、`docs/proposals/`、`web/public/proposals/`，
   甚至有把多方案写成 `<dir>/variant-a/index.html` 的。写偏了 → `unavailable` →
   用户只看到"预览打不开"，不知道是自己没说清还是功能坏了。

处理（三层，都不改架构）：

| 层 | 做法 |
|---|---|
| 发现宽容 | `PreviewStaticDirCandidates` 白名单（权威目录在前），命中任一即 `static`；目录内**递归两层**找 html，覆盖 `variant-a/index.html` |
| 结构兜底 | 根路径没有 `index.html` 时：唯一入口 → 302 过去；多入口 → 生成极简索引页（纯链接）；全空 → 404 带 hint |
| 指路而非静默 | `PreviewStatus` 增 `Dir`/`Convention`/`Hint`/`Suspected`；`status` 与 `start` 失败都带回权威目录 + 疑似方案页路径 |

**刻意不做**：把 worktree 根或任意散落 html 自动认领为预览根。静态预览是"整目录 serve"，
认领根目录等于把源码全部暴露在 `/preview/<id>/` 下；所以散落的页面只**报告**（`Suspected`），
由用户决定是让 agent 挪进约定目录，还是写 `preview.json` 指路。

**也不再要求用户记住路径**：prompt 里只写意图（"出一份手机上能点的对比页"）即可，
位置由发现层承接；实在落偏了，hint 会说明该往哪挪。

## 代价（明确接受）

| 限制 | 说明 |
|---|---|
| 只有 GET/HEAD | 静态产物没有 API；写方法直接 405 |
| 无 SPA fallback | 深链只找真实文件 + `index.html`，找不到即 404（多页对比页按文件名跳转即可） |
| 无 HMR | 靠 `no-store` 刷新即见新内容，方案页改写频率不高，够用 |
| 不做符号链接限制 | worktree 由 agent 控制，与 `local-image` 端点同一信任级别；目录穿越由 `path.Clean` + `pathWithin` 双重挡 |

## 被否掉的方案

1. **让 agent 起静态服务器**（`python -m http.server` / `npx serve`）：要处理 PORT 注入、
   进程回收、Windows 孙进程收尸；且仍逃不掉"任务终态被收掉"这一条 —— 用户回看方案页失败。
2. **截图贴 timeline**（`read_image`）：静态视觉够用，但**交互方案没法点**，而交互恰恰是
   这轮迭代要选的东西。
3. **全局共享一个静态服务器**：不是共享进程能解决的事——路径要按 taskID 隔离、鉴权要复用 pieqi 的，
   进程内 mux 天然同时满足两者，还省掉一个常驻进程。
