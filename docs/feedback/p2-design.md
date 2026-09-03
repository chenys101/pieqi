# Pieqi Feedback P2 设计

> Status: Design（对应 `Pieqi「反馈体系」功能点规划.md` 的 P2 范围）
> Updated: 2026-09-03
> 关联：`CONTEXT.md`、`docs/adr/0001~0007`、`p0-design.md`、`p1-design.md`

---

## 1. 目标与范围

P2 把 Feedback 从「代码反馈」推进到**视觉反馈**：

```text
Preview ──▶ Screenshot / Browser Console / Network Error ──▶ Visual Evidence ──▶ Continue
```

覆盖规划项：Screenshot、Browser Console、Network Error、Screenshot → Evidence、File-level Rewind、Evidence Push（Notification Provider）。

## 2. 视觉采集底座（ADR-0006）

Screenshot / Console / Network 由一个 **node 服务 `services/visual-capture/`（Playwright）** 采集，Go 侧 `VisualCaptureManager` 经 HTTP 调用。

```text
Go (VisualCaptureManager) ── HTTP ──▶ services/visual-capture (Playwright + Chromium)
                                          │ 打开 http://127.0.0.1:<previewPort>/
                                          ├─ screenshot → PNG bytes
                                          ├─ console events → console.error/warn
                                          └─ network events → 4xx/5xx/failed
```

- **复用 claude-sdk-bridge 模式**：`Proc` spawn（幂等 + 健康探测 `/health` + watcher 唯一 Wait + SIGTERM→KILL）、auto_start、按需启动、空闲回收。
- **浏览器二进制**：首次使用需 `npx playwright install chromium`（文档标注部署前置）。
- **只连 127.0.0.1 preview 端口**，不接触用户页面之外的东西。
- 采集服务不持有隧道 token（同 Preview 子进程安全约束）。

## 3. Screenshot（规划 §23）

### 3.1 数据与 API

```go
type Screenshot struct {
    ID        string    `json:"id"`
    TaskID    string    `json:"task_id"`
    PreviewID string    `json:"preview_id"`
    URL       string    `json:"url"`   // /api/tasks/:id/preview/screenshots/<id>.png
    CreatedAt time.Time `json:"created_at"`
}
```

```text
POST /api/tasks/:id/preview/screenshots   → 对运行中的 preview 截图
GET  /api/tasks/:id/preview/screenshots   → 列表
GET  /api/tasks/:id/preview/screenshots/:id.png → PNG 文件（走 Preview 子路径，继承鉴权）
```

- 仅当 preview 处于 `running` 时可截图；否则 409。
- 全页或视口截图：P2 默认视口 + 可选全页。
- 存储：`~/.pieqi/previews/<taskID>/screenshots/`。

### 3.2 手机体验

Feedback 内「截图」按钮 → 即时生成 → 手机直接查看页面截图（覆盖「没装 dev 环境也想看效果」的场景）。

## 4. Browser Console（规划 §23）

采集 preview 页面运行时事件，只保留 error / warn：

```go
type ConsoleEntry struct {
    Level   string `json:"level"`   // error | warn
    Text    string `json:"text"`
    At      time.Time `json:"at"`
}
```

```text
GET /api/tasks/:id/preview/console?since=...
→ { "errors": N, "warnings": M, "entries": [...] }
```

- 采集窗口：capture 会话 attach 期间累积（内存，可丢弃）；持续模式可选（P2 默认 attach 期间 + 最近 N 条）。
- 展示：`Console ✗ 3 errors ⚠ 5 warnings` → 展开明细。

## 5. Network Error（规划 §23）

只采集失败的请求：

```go
type NetworkEntry struct {
    URL     string `json:"url"`
    Method  string `json:"method"`
    Status  int    `json:"status"` // 0 = failed
    At      time.Time `json:"at"`
}
```

```text
GET /api/tasks/:id/preview/network?since=...
```

- 过滤：仅 4xx / 5xx / failed（`net::ERR_*`）。
- 与 console 同窗口采集。

## 6. Screenshot → Evidence（规划 §24）

截图成为**视觉证据**，进入 Evidence → Continue 链路：

```text
Preview Screenshot
       ↓
Evidence（挂 Task/Turn/Outcome）
       ↓
Continue：后端组装含截图引用的 Agent Context
  「页面顶部按钮仍然错位」+ 截图 URL
```

- `POST /api/tasks/:id/continue` 扩展：Evidence 可携带 `screenshots: [id]`，后端把截图引用并入提示文本（P2 阶段 Agent 侧是否看图由 provider 能力决定，Pieqi 只负责传引用）。
- 为 P3（Region Annotation / 视觉输入）打基础。

## 7. File-level Rewind（规划 §25）

`POST /api/tasks/:id/rewind` 扩展 `scope: "file"`：

```json
{ "to_turn": 3, "scope": "file", "path": "src/Login.vue" }
```

- 用 P0 的 Checkpoint 组装对单文件求 before-Turn-3 状态 → 恢复该文件。
- 不影响其他文件；同样追加 `rewind` TaskEvent（`restored` 只含该文件）。

## 8. Evidence Push / Notification Provider（规划 §26）

**不绑定具体 IM**：基于已有 `channel.MessageSender`（`Bridge.senders`）+ Task 的 `OriginChannel/OriginChatID` 实现第一版，Webhook 做通用 Provider。

```go
type NotificationProvider interface {
    Push(ctx context.Context, target model.ReplyTarget, content EvidencePushContent) error
}
type EvidencePushContent struct {
    Kind    string        `json:"kind"`    // outcome | evidence | error
    Outcome *TaskOutcome  `json:"outcome,omitempty"`
    Evidence *Evidence    `json:"evidence,omitempty"`
    Text    string        `json:"text"`    // 精简可读文本（移动端卡片）
}
```

```text
POST /api/tasks/:id/push   → 把 Outcome/Evidence 推送到 OriginChannel（或指定 target）
```

- **推送时机**：Task 进终态时自动推 Outcome 摘要（飞书/企微卡片）；可选手动推 Evidence。
- Provider 注册表：`lark`（复用 MessageSender）、`webhook`（配置 URL）、后续扩展。

## 9. API 契约汇总（P2 新增）

| 端点 | 说明 |
|---|---|
| `POST /api/tasks/:id/preview/screenshots` | 截图 |
| `GET /api/tasks/:id/preview/screenshots` | 截图列表 |
| `GET /api/tasks/:id/preview/screenshots/:id.png` | 截图文件 |
| `GET /api/tasks/:id/preview/console` | Console error/warn |
| `GET /api/tasks/:id/preview/network` | 网络失败 |
| `POST /api/tasks/:id/rewind`（`scope:"file"`） | 文件级回退 |
| `POST /api/tasks/:id/push` | Evidence Push |

## 10. 验收标准

| # | 功能 | 验收 |
|---|---|---|
| 1 | Screenshot | running 预览可截图；手机可查看；preview 未运行 → 409 |
| 2 | Browser Console | 只采 error/warn；计数 + 明细 |
| 3 | Network Error | 只采 4xx/5xx/failed；含 URL/状态 |
| 4 | Screenshot → Evidence | 截图可挂 Evidence；Continue 携带截图引用 |
| 5 | File Rewind | 单文件恢复到指定 Turn；不影响其他文件；事件入 Timeline |
| 6 | Evidence Push | Outcome 终态自动推送；provider 可替换；不绑定单一 IM |

## 11. 已知限制 / P3 边界

- 采集只覆盖 preview 运行期；未跑 preview 无视觉数据。
- console/network 窗口数据在内存，服务重启即失（P2 接受，不做持久化归档）。
- 截图是像素证据，Agent 是否「看懂」取决于 provider 能力（P3 视觉输入才把区域语义化）。
- 浏览器二进制安装为部署前置。
- 完整 Browser Automation（控制浏览器）**不在范围内**（ADR-0007）。

## 12. 落地拆解

### 后端

| 模块 | 内容 |
|---|---|
| `services/visual-capture/` | node + Playwright：/health、/screenshot、attach 窗口（console/network 事件流） |
| `preview/visual.go` | `VisualCaptureManager`：spawn/健康/清理、截图存储 |
| `evidence_visual.go` | Console/Network 聚合、Screenshot→Evidence 挂载、Continue 扩展 |
| `push.go` | NotificationProvider 注册表 + Outcome 自动推送 |
| `api/feedback_p2.go` | 上述端点 |

### 前端

| 模块 | 内容 |
|---|---|
| `features/preview` | 截图按钮 / 截图列表 / 视觉证据卡 |
| `features/feedback` | Console/Network 摘要 + 展开；File Rewind 入口 |
| `features/evidence` | Push 按钮 / 推送状态 |

### 实施顺序

```text
P2-a: visual-capture 服务（screenshot 优先）
P2-b: Console / Network 采集与展示
P2-c: Screenshot → Evidence → Continue
P2-d: File-level Rewind
P2-e: Evidence Push（Lark 复用 MessageSender → Webhook）
```
