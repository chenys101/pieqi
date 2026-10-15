# 移除截图功能，保留 console/network

## 背景

ADR-0006 决定用独立 Node Playwright 服务采集三类视觉证据：**Screenshot / Browser Console / Network Error**，
一次 `/v1/capture` 同时返回三者。落地后实际使用情况暴露出截图这一类的价值远低于另两类。

## 判据（逐条实测，非推测）

1. **对 agent 无信息量**。进 Evidence→Continue 时，截图只以**一行 URL 字符串**出现
   （`evidence.go` 原 159-162 行，注释自认"只传引用，Agent 是否看图由 provider 能力决定"）。
   agent 拿这个路径取不到图（那是需鉴权的 HTTP 端点），除占 prompt 外无作用。
   对照：console/network 送的是**具体文本**（`[error] <message>`、`<method> <url> (status=500)`），
   agent 可直接据此定位与修复。
2. **页面范围窄**。`visual.go` 固定访问 `http://127.0.0.1:<port>/`（**根路径**），
   `full_page` 只控制"整页 vs 视口"，**无法指定路由** —— `/settings`、某任务详情页都截不到。
3. **触发条件苛刻**。后端**没有任何自动触发**（全仓 `.Capture(` 仅一处调用，即 HTTP handler）；
   必须 preview 处于 running **且人在 PWA 上手动点击**。本机 `~/.pieqi/previews/` 从未产生过内容。
4. ★ **它的失败会连坐真正有用的部分**。`Capture` 里这是**唯一**一条失败即返回的断言：

       png, err := base64.StdEncoding.DecodeString(cr.PNGBase64)
       if err != nil || len(png) == 0 { return nil, errors.New("visual: empty screenshot") }
       v.appendEvents(taskID, cr.Console, cr.Network)   // ← 在截图断言之后

   即：截图解码失败/为空 ⇒ **已经采到的 console/network 一并丢弃**。
   这是三条里最实质的问题 —— 一个次要产出把主要产出拖垮。

## 决策

**移除截图，保留 console/network。** 具体：

- `core.VisualCaptureManager.Capture` 不再解码/落盘 PNG，只并入事件窗口；返回 `error`（不再返回 `*Screenshot`）。
- 删除 `Screenshot` 类型、`ListScreenshots`、`ScreenshotPath`、`shotDir`、`shotIDPattern`、`randomShotID`。
- 删除三个端点：`POST/GET /preview/screenshots`、`GET /preview/screenshots/<id>.png`。
- **旧路径返回 410 Gone**（而不是让它掉进反向代理拿到 SPA 兜底页）—— 调用方能明确区分
  "功能没了"与"服务坏了"，响应里带 hint 指向新端点。
- 端点改名：`POST /preview/screenshots` → **`POST /preview/capture`**，名字如实反映现在的行为。
- `Evidence.Screenshots` 字段删除；`EvidencePrompt` 不再输出"截图证据"行
  （并加**负向测试**钉住它不复活）。
- Node 服务侧 `png_base64` 改为**按需**（`want_png: true` 才拍）—— 保留能力以备将来
  （例如自动化测试需要图），但默认不产生 PNG 编码与 base64 传输开销。

## 被否掉的方案

- **只删 agent 上下文里的那行 URL**（最小改动）：不解决"截图失败连坐 console/network"，
  也不解决前端仍在展示一个只能截根路径的按钮。**留下一个没人用的功能，比删掉它成本更高。**
- **截图改为按需（默认不采、人点才拍）**：仍保留连坐问题与三端点，改动面反而更大。

## 代价

- 前端 `PreviewSection` 的截图缩略图、`EvidenceCard` 的截图区**移除**；类型 `ScreenshotDto`/
  `ScreenshotsResponseDto` 删除。
- 已存在的 `~/.pieqi/previews/<task>/screenshots/*.png` 不再被引用（本机从未产生过）。
- 若将来要恢复，`git log` 可查本次提交；Node 侧能力已按需保留，恢复成本主要在 Go 与前端。

## 与既有 ADR 的关系

**不改写 ADR-0006**（它记录的是当年的决策），本 ADR 是对它的**部分修订**：
采集服务与 Console/Network 继续按 ADR-0006 工作；Screenshot 一类退出。
另：采集服务源码已于同日迁到独立工程 `pieqi-ui-test`，运行期用 `PIEQI_VISUAL_DIR` 指向，
与 ADR-0006 的"独立 node 服务"取向一致。
