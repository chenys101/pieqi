# 移除 console/network 采集，视觉反馈层整体下线

## 背景

ADR-0006 引入"独立 Node Playwright 采集服务"，产出三类视觉证据：Screenshot / Browser Console /
Network Error。ADR-0012（同日稍早）以"对 agent 无信息量 + 只能截根路径 + 触发苛刻 +
会连坐另两类"为由移除了 Screenshot，并**保留** console/network，理由是它们送的是
**具体文本**（`[error] <message>`、`<method> <url> (status=500)`），agent 可直接据此修 bug。

本 ADR 记录：**console/network 随后也被移除**。这不是推翻 ADR-0012 的技术判断
（那个判断仍然成立），而是基于**实际使用情况**的决策。

## 判据

1. **触发条件与截图同样苛刻**。采集的唯一入口是
   `POST /api/tasks/:id/preview/capture`，且要求：
   - preview 处于 `running`（否则 409）—— 即得先把被改项目的 dev server 跑起来；
   - **由人在 PWA 上手动点击**（全仓无任何自动触发；`.Capture(` 仅此一处调用）。
   换言之：它和截图共享同一套前置条件，而那套前置条件正是"实际从未被触发"的原因。

2. **宿主环境的维护成本与收益不成比例**。整层依赖一个独立 Node 服务，需要
   `npm install` + `playwright-core` + 下载 chromium 浏览器二进制；而本机
   `services/visual-capture/node_modules` **从未安装过**、`~/.pieqi/previews/` **从未产生过内容**
   —— 即这个能力在本机从未真正跑起来过一次。

3. **ADR-0012 之后，剩余部分的独立性并不成立**。截图移除后，`Capture` 的职责收窄为
   "只并事件窗口"，但它仍携带整套进程托管（spawn / `/health` 探活 / watcher / SIGTERM→KILL /
   `PIEQI_VISUAL_DIR` 目录解析）、事件窗口 ring、两个摘要端点、前端两个组件与四个 DTO。
   **为一条从未被触发的链路维持这些，是纯粹的持有成本。**

4. **agent 仍有替代手段**。它可以在 worktree 里自己起 dev server 并读输出；
   缺失的只是"pieqi 代它采集并格式化后送进 Evidence"这层便利，而非能力本身。

## 决策

**整体移除视觉采集层。** 具体：

| 删除项 | 内容 |
|---|---|
| `internal/core/visual.go` | 整个文件（`VisualCaptureManager` 全套） |
| `internal/core/visual_test.go` | 整个文件 |
| 端点 | `POST /preview/capture`、`GET /preview/console`、`GET /preview/network` |
| 接线 | `main.go` 的 `visualMgr` 建/`WatchBus`/`Stop`；`router.go` 的 `s.visual` 与 `SetVisualCapture` |
| `Evidence` | `Console` / `Network` 字段；`ConsoleSummary`/`NetworkSummary`/`ConsoleEntry`/`NetworkEntry` 类型 |
| `EvidencePrompt` | "页面 Console" 与 "页面网络失败" 两段输出 |
| 前端 | `CaptureResponseDto`/`ConsoleSummaryDto`/`ConsoleEntryDto`/`NetworkSummaryDto`/`NetworkEntryDto`；`capturePreview`/`getConsoleSummary`/`getNetworkSummary`；`EvidenceCard` 的 console/网络区；`PreviewSection` 的「截图」按钮 |

**四个 preview 子路径统一返回 410 Gone**（`capture`/`console`/`network`/`screenshots`）。
这一点是**有意的**，理由见下节。

**保留**：
- **preview 本身**（P1：把 agent 改完的代码跑起来在手机上看）—— 与采集无关，不受影响。
- 采集服务源码：**留档**在独立工程 `pieqi-ui-test/services/visual-capture`（用户决定）。
  它是可运行的 Playwright 采集实现，将来若要恢复或供自动化测试复用，可直接取用。

## 为什么是 410 而不是删掉判断让它 404

`previewRoute` 是 **catch-all**（`/api/tasks/:id/preview/*path`），未命中任何 case 的路径会走
`default` 分支的**反向代理** —— 把 `/preview/console` 当作**被预览项目的页面路径**转发给
dev server。调用方会拿到一个 HTML 或 SPA 兜底页，**无法区分"功能没了"与"服务坏了"**。

所以显式返回 410 Gone 并带 `hint`。语义也正确：410 表示"曾经存在、现已永久移除"。

⚠️ **实现细节（实测踩到）**：`screenshots/<id>.png` 带**子路径**，不匹配任何精确 `case`，
曾掉进 302 代理跳转。因此判断用 `path == ... || strings.HasPrefix(path, "screenshots/")`，
回归测试 `TestAPI_P2_VisualSubpathsGone` 覆盖 6 条路径。

## 被否掉的方案

- **只保留 console/network、删截图**（即 ADR-0012 的状态）：技术判断成立，但忽略了
  "这层从未被触发过"这个事实；维持一个独立 Node 服务 + chromium 的成本没有收益支撑。
- **降低触发成本（如任务失败时自动采一次）**：那是**给一个未被验证的需求加功能**。
  若将来确实需要"agent 看到页面运行时错误"，应当从**实际遇到的场景**出发重新设计，
  而不是激活一条从未跑通的链路。

## 代价（诚实记录）

- agent 不再自动获得"页面控制台报错 / 接口失败"的输入。UI 白屏或接口 500 时，
  它需要自己起 dev server 并想办法读输出。
- 本机从未真正使用过该能力，因此**预期无实际损失**；但这只是"本机"的证据，
  不排除其它部署在用 —— 若有，410 + hint 能让他们立刻知道发生了什么。
- 若将来要恢复：`git log` 可查本次提交；采集服务源码已在 `pieqi-ui-test` 留有完整实现
  （含 `want_png` 按需截图开关，见 ADR-0012）。

## 与既有 ADR 的关系

- **不改写 ADR-0006**（它记录当年的决策）。本 ADR 是它的**整体撤销**：
  ADR-0006 定义的三类证据全部退出，采集服务不再由 pieqi 托管。
- **ADR-0012 的技术判断仍有效**（截图确实劣于 console/network），但本 ADR 表明
  那个判断不足以支撑"保留 console/network"的结论 —— **技术优劣 ≠ 值得维护**。
