# Pieqi 通信架构升级方案 — 评估意见

> 评估对象：《Pieqi 通信架构升级方案 v1.0》（2026-08-14）
> 评估依据：方案文本 + 当前代码库实现（`internal/api`、`internal/core`、`internal/agent`、`internal/channel`）
> 评估日期：2026-08-14
> 评估结论：**方向正确，核心分层成立；但在落地语言、关键模块现状、安全细节与若干协议字段上存在不可忽视的缺口，需修订后再进入实施。**

---

## 0. 总体结论

### 0.1 评分总览

| 维度 | 评分 | 说明 |
|------|------|------|
| 战略方向 | A | Protocol/Transport 分离、Relay 不可信、E2E 保护端到端，四原则成立且面向未来 |
| 架构分层 | A- | Channel→Protocol→Transport→Runtime→ACP→Agent 清晰，ACP 隔离正确 |
| 与现有代码对齐 | C+ | 提案用 `.ts`/TypeScript，但 Pieqi 是 **Go**；若干"现状假设"与代码不符 |
| 技术选型 | B+ | X25519+HKDF+AES-GCM、CF DO Hibernation 是合理 MVP 选型 |
| 安全模型 | B | E2E 算法对，但 PWA 密钥存储、首次配对信任根、轮换触发均欠定义 |
| 可恢复性设计 | B- | 目标正确，但低估了 EventBus 现状（无序号、丢弃式）的改造量 |
| 阶段计划 | B | 顺序合理，但 Phase 0 协议冻结缺验证载体、REST→协议迁移未排期 |
| 落地可执行性 | C+ | 立即执行清单偏理想，未给出与 Go 代码的具体迁移映射 |

### 0.2 三句话结论

1. **方向没问题**：把 PWA/IM 收敛到统一 Protocol + 可插拔 Transport，是 Pieqi 从"本地监控 PWA"演进到"远程 Agent 通信层"的正确路径，与现有 [AgentManager](file:///workspace/internal/agent/manager.go) 的 adapter 抽象一脉相承。
2. **最大断层是语言**：方案通篇 TypeScript（`transport.ts`、`interface Transport`），但 Pieqi 是 Go（gin/viper/zap/coder/websocket），且前端 [ws.go](file:///workspace/internal/api/ws.go) 已是 Go 侧 WS。需先统一到 Go 接口，否则 §18 的目录结构无法落地。
3. **最大隐藏改造是 EventBus**：[event_bus.go](file:///workspace/internal/core/event_bus.go) 现为"慢则丢弃、无序号、无 replay"，而 Phase 6 Session Resume 的 `lastSequence` 补发依赖"有序号 + replay buffer / Event Store"，这是对现有事件总线的重设计，方案未点明其工作量与风险。

---

## 1. 与现有代码库的对齐分析（最关键）

> 以下逐条对照方案"现状假设"与代码实现。**每条都直接影响阶段计划的可执行性。**

### 1.1 【阻断级】语言错配：TypeScript vs Go

| 方案假设 | 代码实际 |
|----------|----------|
| `transport/transport.ts`、`direct-transport.ts` | Pieqi 是 Go（`go.mod`），后端无 Node 运行时 |
| `interface Transport { ... }` TS 语法 | 应为 Go `type Transport interface { ... }` |
| `send(data: Uint8Array): Promise<void>` | 应为 `Send(ctx context.Context, data []byte) error` |

**影响**：§6/§18 的全部代码结构与接口定义无法直接落地。前端 [PWA](file:///workspace/web/src/main.js) 是 JS，但 **Transport/PieqiClient 应在后端**（PWA 是 client，Pieqi 是 server）还是前端？方案没有说清 PieqiClient 跑在哪一侧——这其实是分层语义问题，必须先澄清：
- 若 `PieqiClient` 指 PWA 侧客户端 SDK（JS/TS）：则 TS 合理，但 §15 的"PWA→Protocol Command→Transport→Pieqi"应明确 Pieqi 侧是 server。
- 若 `Transport` 指 Pieqi 后端的传输抽象：则必须是 Go。

**修订建议**：明确两侧边界——PWA 侧用 TS（`PieqiClient`），Pieqi 后端用 Go（`Transport` server 侧 + `Protocol` 编解码），并给出两侧接口的对应表。Relay（CF DO）用 JS/TS 是合理的。

### 1.2 【阻断级】EventBus 不支持 Resume，需重设计

[internal/core/event_bus.go](file:///workspace/internal/core/event_bus.go) 现状：
- `Publish` 对慢订阅者 `default` 丢弃（第 76-82 行），**不阻塞、不缓冲重发**。
- `Event` 结构只有 `Type/TaskID/Task/Delta`（第 28-33 行），**无 `sequence` 字段**。
- 订阅是 in-memory channel，进程重启即丢。

方案 §14 要求 `session.resume` + `lastSequence` + 补发 1051/1052/...。这要求：
- 事件带**单调递增 sequence**（按 session 维度）。
- 存在 **replay buffer / Event Store**（持久化或环形缓冲），可按 `lastSequence` 回放。

**这是 Phase 6 的真正成本**，方案把它列为"任务"但未标明这是对 [EventBus](file:///workspace/internal/core/event_bus.go) 的重设计。建议新增一个 `EventStore`（独立于 fan-out 的 `EventBus`）：`EventBus` 负责 fan-out，`EventStore` 负责序号分配 + 持久化 + 回放，WS 层 resume 时从 Store 取。需定义保留窗口（如环形 buffer + 完整快照）。

### 1.3 【重要】IM 渠道当前是"通知单向"，非双向命令通道

[internal/core/bridge.go](file:///workspace/internal/core/bridge.go) 的 `handlePieqiMessage`（第 160-162 行）收到 IM 消息只回固定提示：
> 💡 请在 PWA 新建任务…

IM 的**出方向**（Pieqi→IM）靠 `NotifyOrigin` 推送任务回执（第 111 行），但**入方向**（IM→Pieqi 命令）未实现。方案 §1.1 称"PWA、IM、未来 CLI 等客户端统一使用"——这意味着要把 IM 升级为可发命令的 Channel，工作量远超"接入新协议"，涉及各 IM 平台（飞书/企微/微信）的消息解析与权限模型。

**修订建议**：明确 IM 的"客户端"范围。建议 Phase 1-6 仅统一 **PWA + 未来 CLI** 为协议客户端；IM 维持"通知通道"定位（与 README 一致），E2E/Relay/Publish 主要服务 PWA。IM 升级为双向 Channel 列为 Phase 7+ 非目标，避免本阶段范围膨胀。

### 1.4 【重要】PWA 当前走 REST + WS 双通道，非统一协议

| 方向 | 现状 | 方案目标 |
|------|------|----------|
| PWA→Pieqi 命令 | HTTP REST（[tasks.go](file:///workspace/internal/api/tasks.go) 的 `POST /api/tasks`、`/intervene`、`/cancel`） | 统一经 `task.create`/`permission.response` 协议消息 |
| Pieqi→PWA 事件 | WS text 帧（[ws.go](file:///workspace/internal/api/ws.go) 直接 `json.Marshal(Event)`） | 统一经 `event.*` 协议消息 |

方案 §15 反向路径画成"PWA→Protocol Command→Transport→Pieqi"，但**未把 REST→协议的迁移列入任何 Phase**。若保留双通道，则"统一协议"打折；若迁移，则是巨大改造且需兼容期。

**修订建议**：在 Phase 1 显式决策——(a) 协议仅承载事件流（Pieqi→PWA），命令仍走 REST；或 (b) 命令也走协议（长连接 + 请求/响应）。推荐 (b) 但分步：Phase 1 先统一事件方向，命令迁移留到 Phase 2 之后，并保留 REST 兼容窗口。

### 1.5 【重要】Permission 当前以 `task_updated` 事件浮现，非独立消息

[internal/core/agent_perm.go](file:///workspace/internal/core/agent_perm.go) 的 `onPermissionRequest`（第 115 行）发布的是 `Event{Type:"task_updated", Task:...}`，权限请求**嵌在 task 的 `CurrentDecision` 字段**里（第 141-148 行），靠 PWA 前端从 task 状态里渲染审批卡片。方案 §16 要 `permission.request` 作为一级消息。

**对齐成本**：要么 (a) 新增独立的 `permission.request` 消息（与现有 `task_updated` 并发发布），要么 (b) 保持现状但把 `CurrentDecision` 抽成协议消息。现有 `PermissionWire` 的超时（30min 默认，第 125 行）、先到先得（第 176 行）、Unwire 时 Deny 挂起请求（第 273-280 行）等语义都需映射到协议层，方案未覆盖。**这是好事**——现有实现已较完善，协议层应包装而非重写。

### 1.6 现有抽象可直接复用（正面）

- [AgentManager](file:///workspace/internal/agent/manager.go)：`AgentAdapter` 接口 + primary/fallback 工厂 + 每项目并发信号量 + 透明回退，已是"可插拔"范式，方案 §6 的 Transport 抽象应**仿照此模式**而非另起炉灶。
- [PermissionWire](file:///workspace/internal/core/agent_perm.go)：协议级审批已落地，§16 与之高度一致。
- [TaskStore](file:///workspace/internal/core/task_store.go)：原子写 + 重启恢复（孤儿 running→failed，第 163-165 行），为 resume 提供了 task 状态持久化基础。
- ACP 隔离：`internal/agent/acp.go` 与 `core` 单向依赖（manager.go 注释第 8-10 行），方案 §1.1"不让 PWA 直接依赖 ACP"已满足。

---

## 2. 分模块技术评估

### 2.1 §4 Protocol Envelope — 合理但字段语义不足

`version/type/requestId/sessionId/taskId/sequence/timestamp/payload` 基本正确。缺口：

- **`sequence` 作用域未定义**：全局？每 session？每方向（上行/下行分别计数）？建议：**每 sessionId + 每方向** 单调递增，下行（Pieqi→PWA）用于 replay，上行无需严格序号（请求-响应靠 requestId 关联）。
- **缺响应/错误信封**：只有请求侧 `requestId`，未定义 `response`/`error` 的标准结构（`type` 取值？`error.code`？）。建议补 `{type:"*.response"|"*.error", requestId, error?:{code,message}}`。
- **缺 `ack` 机制**：远程模式下 PWA 是否需要对命令回 ack？无 ack 则命令丢失不可知。
- **version 协商**：只有 `version:1`，未定义版本不匹配时的拒绝/降级协议。

### 2.2 §5 消息类型 — 基本完整，补漏

- 缺 `session.resumed`（resume 成功 ack，携带 `fromSequence`）、`session.expired`、`permission.expired`/`permission.cancelled`（对应现有 30min 超时语义）。
- `task.create` 与 `task.start` 分离，但 [TaskStore.Create](file:///workspace/internal/core/task_store.go) 当前一并置 pending、由 runner 启动。需明确协议侧生命周期状态机（pending→running→waiting_input→running→completed/failed/cancelled），与 `model.Task` 的 `Status` 对齐。
- 缺 IM 异步通知类消息（现有 `NotifyOrigin` 的"任务完成/需决策"推送在协议里如何表达？建议复用 `event.status`/`permission.request`）。

### 2.3 §6 Transport 抽象 — 接口过窄

`connect/send/onMessage/onClose/close` 是最小集，但缺：
- `onError(handler)`（区别于 onClose 的异常通道）
- 背压/慢消费者信号（现有 EventBus 是"丢弃"，Transport 上层需明确策略）
- `flush()`/`drain` 语义（流式场景）
- 最大帧大小与分片策略（Relay 转发大消息如何处理）

### 2.4 §7 DirectTransport — 现成可改造

[ws.go](file:///workspace/internal/api/ws.go) 已实现 localhost WS + 30s ping（第 54-58 行）+ 先 snapshot 后转发。缺口：
- `InsecureSkipVerify: true`（第 16 行）本地可，公网 WSS 必须关掉并校验 Origin。
- 无重连退避（backoff/jitter/max retries）。
- TLS 证书供给（公网域名场景）未提。

### 2.5 §8-9 RelayTransport + Cloudflare DO — 选型合理，但缺关键细节

CF DO + WebSocket Hibernation 是 MVP 的正确选择。**阻塞性缺口**：

- **认证模型完全缺失**：§9.1 列了"Auth"组件但零细节。PWA 如何向 DO 证明身份？daemon 如何向 DO 证明？没有这层，任意 PWA 可连任意 deviceId。**必须在 Phase 3 之前定义**：建议 daemon 持有 DO 访问 token（短期签发），PWA 经配对获得同类 token；token 校验在 Worker 入口。
- **DO 寻址方案未定**：DO 由 name 实例化，`pairId`/`deviceId` 如何映射到 DO name？建议 `idFromName("device-"+deviceId)`，每设备一 DO。
- **`pairId` vs `deviceId` 关系不清**：§9.2 列 `pairId`，§10.2 配对产出 `deviceId`。是一对一？多 PWA 配一 daemon？
- **daemon 离线时 PWA 帧丢弃策略**：缓冲？拒绝？需明确（建议：daemon 不在线时 DO 直接 close PWA 并告知 offline）。
- **配额/成本**：DO 有 request/alarm 配额，长期 relay 需评估。Hibernation 缓解但不消除。
- **daemon 侧出站连接健壮性**：daemon 在 NAT 后，必须维护持久出站 WSS + 指数退避重连，方案未细化。

### 2.6 §10-13 E2E — 算法正确，工程缺口多

X25519+HKDF+AES-256-GCM 是教科书级正确选择，"不自研密码学"原则对。缺口：

- **首次配对信任根未明示**：§10.2 扫码/配对码是 TOFU（trust on first use）。若 QR 被中间人替换，则 publicKey 被替换，E2E 形同虚设。**必须显式声明**：配对码是唯一信任根，需带外核验（如配对码由 daemon 离屏显示 + 用户手动输入，而非扫码可被替换）。
- **PWA 密钥存储**：§10.1/§11.2 "本地安全存储"。PWA 是 Web App，`IndexedDB`/`localStorage` **不是**安全存储（XSS 可读）。Web Crypto 的 `CryptoKey(extractable:false)` + IndexedDB 是当前可达的最佳实践，但仍有边界。方案需承认这一限制或要求 PWA 走原生壳（TWA/Capacitor）以用平台 keystore。
- **nonce 生成策略**：§13 说"nonce 不得重复"但未给机制。AES-GCM nonce 重用是灾难性（泄露明文+认证密钥）。建议：`nonce = keyId(固定) || counter(8 bytes)`，counter 持久化且单调递增；或随机 96-bit + 冲突概率分析。必须写明。
- **counter 跨重连同步**：重连后如何恢复 counter？靠 sequence 协商？需协议化。
- **密钥轮换触发**：§13 提"预留 keyId"但无触发协议（消息数？时间？重连？）。建议：重连必重新握手（新 ephemeral key，保 PFS）；轮换可留后续。
- **session.resume 与密钥关系**：resume 时复用旧 sessionKey 还是重派生？复用破坏 PFS（若旧 key 已泄露）。建议 resume 走新握手 + 复用 sessionId。
- **DO 对控制帧的可见性**：§12 中 `client_hello`/`daemon_hello` 是明文（含 publicKey），DO 必须读 type 字段以路由。这**不违反**"Relay 不解 payload"——控制帧是 metadata，application frame 是密文。但方案表述含糊，应明确"DO 仅解析控制帧 type，application frame 为不透明 binary"。

### 2.7 §14 Session Resume — 目标对，工程量被低估

见 §1.2。补充：
- **"Agent 继续"前提**：需明确这是 **transport 断连**（PWA↔Pieqi 连接掉，Pieqi 进程与 ACP agent 进程存活），**不是** Pieqi 进程重启。后者现有 [TaskStore.load](file:///workspace/internal/core/task_store.go) 会把 running→failed（第 163-165 行）。两种 resume 语义不同，方案应区分。
- **replay buffer 保留策略**：环形 buffer（如最近 N 条）+ 完整快照（task 当前状态），超过窗口则回 snapshot + 增量。需定义。
- **permission 状态恢复**：现有 `PermissionWire.pending` 在内存，transport 断连时 Pieqi 进程未死、pending 还在，resume 后 PWA 应能重新看到 pending 决策。需 protocol 消息把当前 pending 同步过去（`permission.request` 重发 or 快照含 pending）。

### 2.8 §15 EventBus 与通信层 — 方向对，需补 REST 决策

见 §1.4。现有 `EventBus`→`ws.go`→PWA 的链路与方案"EventBus→Protocol Event→Transport→PWA"一致，只需在 `ws.go` 与 EventBus 之间插入 `Protocol` 编解码层。**命令方向**（REST）的归属必须先决策。

### 2.9 §16 Permission — 与现有实现对齐良好

现有 [PermissionWire](file:///workspace/internal/core/agent_perm.go) 已是协议级、不依赖 Claude UI、带超时/先到先得/Unwire 清理。方案 §16 与之一致。唯一需补：把 `task_updated` 内嵌的 `CurrentDecision` 抽成独立 `permission.request`/`permission.response` 消息（§1.5）。

### 2.10 §17-18 代码结构 — TS 化，且 ACP 归类需修正

- §18 全 TS 扩展名，见 §1.1。
- §18 `adapters/{acp,pwa,wechat,feishu,wecom}` **归类错误**：ACP 是 Pieqi→Agent（下游 agent 协议），PWA/IM 是 client→Pieqi（上游通道）。两者方向相反，不应同列 `adapters/`。现有代码已正确：`internal/agent/`（ACP）与 `internal/channel/`（IM）分置。建议保留这一分离，`adapters/` 仅放客户端侧（pwa/cli），ACP 留 `internal/agent/`。
- §18 `relay/cloudflare-do/` 在 Go 后端仓库里是 JS 子项目，需明确构建/部署边界（独立目录 + 独立 wrangler 部署）。

### 2.11 §19 阶段计划 — 顺序合理，补缺口

- **Phase 0 协议冻结缺验证载体**：纯设计易脱离实际。建议 Phase 0 末尾加"用 stub transport 跑通一条 task.create→event.message→task.complete 的纸面往返"。
- **Phase 2 DirectTransport 验收含"reconnect"**：但无 resume 的 reconnect = 全量 re-snapshot（重连后重发 snapshot）。应明确"Phase 2 的 reconnect 是 stateless re-sync，Phase 6 才是 stateful resume"。
- **REST→协议迁移未排期**（§1.4），需补。
- **EventBus 重设计未单列**（§1.2），建议作为 Phase 6 的前置子任务或独立 Phase 5.5。

### 2.12 §20-22 MVP / 安全 / 决策表 — 基本合理

- §20 场景 D"Agent 继续"依赖 ACP agent 进程独立存活——已满足（`AgentManager` spawn 独立进程）。
- §21 信任模型正确。
- §22 决策表清晰，但"Relay 协议: WebSocket"应注明 daemon 侧是**出站** WSS（NAT 后），且需重连策略。

---

## 3. 关键风险与缺口汇总

| # | 风险/缺口 | 等级 | 位置 |
|---|-----------|------|------|
| R1 | 语言错配（TS vs Go），目录结构无法直接落地 | 阻断 | §6/§18 |
| R2 | EventBus 无序号/丢弃式，Resume 需重设计为 EventStore | 阻断 | §14/§1.2 |
| R3 | Relay 认证模型完全缺失 | 阻断 | §9 |
| R4 | 首次配对信任根（TOFU）未声明，MITM 风险 | 高 | §10 |
| R5 | PWA 密钥"安全存储"在 Web 平台受限 | 高 | §10/§11 |
| R6 | nonce 生成与 counter 跨重连同步未定义（AES-GCM 重用灾难） | 高 | §13 |
| R7 | REST→协议迁移未排期，"统一协议"打折 | 高 | §15/§1.4 |
| R8 | IM 当前单向通知，升级为双向客户端范围膨胀 | 中 | §1.1/§1.3 |
| R9 | `sequence` 作用域、响应/错误信封、ack 机制未定义 | 中 | §4/§5 |
| R10 | ACP 与 PWA/IM 同列 `adapters/`，方向混淆 | 中 | §18 |
| R11 | daemon 出站连接重连退避未细化 | 中 | §8/§22 |
| R12 | 密钥轮换触发协议缺失 | 低 | §13 |
| R13 | DO 寻址/配额/离线丢帧策略未定 | 中 | §9 |

---

## 4. 修订建议（按优先级）

### P0 — 进入实施前必须解决

1. **统一语言边界**：PWA 侧 `PieqiClient`（TS）、Pieqi 后端 `Transport`/`Protocol`（Go）、Relay DO（JS/TS）。给出两侧接口对应表。
2. **新增 EventStore 设计**：序号分配（每 session+方向）+ 持久化 + replay + 保留窗口策略；`EventBus` 保持 fan-out，`EventStore` 独立负责回放。明确列为 Phase 6 前置。
3. **定义 Relay 认证模型**：daemon/PWA 各持短期 token，Worker 入口校验；token 签发与配对绑定。
4. **声明配对信任根**：配对码是唯一信任根，带外核验（离屏显示 + 手输，非可替换扫码）。

### P1 — 协议冻结前补全

5. 补 `sequence` 作用域（每 session+方向）、响应/错误信封、ack 机制、version 协商。
6. 补 `session.resumed`/`session.expired`/`permission.expired`/`permission.cancelled`。
7. 明确 task 生命周期状态机与 `model.Task.Status` 对齐。
8. nonce 生成策略：`keyId||counter`，counter 持久化单调递增；重连重新握手（新 ephemeral key，保 PFS）。

### P2 — 阶段计划调整

9. Phase 0 末加 stub transport 纸面往返验证。
10. Phase 2 reconnect 明确为 stateless re-snapshot，Phase 6 才是 stateful resume。
11. 新增"REST→协议命令迁移"阶段（建议 Phase 2.5，保留兼容窗口）。
12. Phase 7+ 才考虑 IM 升级双向；本阶段 IM 维持通知通道。
13. §18 修正：`adapters/` 仅放客户端侧（pwa/cli），ACP 留 `internal/agent/`，IM 留 `internal/channel/`。

### P3 — 细节补强

14. DO 寻址（`idFromName("device-"+deviceId)`）、离线丢帧策略、配额评估。
15. daemon 出站 WSS 指数退避重连。
16. PWA 密钥存储承认 Web 限制，或要求原生壳（TWA/Capacitor）走平台 keystore。
17. 明确 DO 仅解析控制帧 type，application frame 不透明 binary。

---

## 5. 推荐落地路径（修订后）

在方案 §24 基础上插入前置项与调整：

```
Step 0  语言边界确认 + EventStore 设计草案 + Relay 认证模型草案   ← 新增前置
Step 1  protocol/ (Go envelope + 生命周期状态机 + 响应/错误/ack) + stub 往返验证
Step 2  transport/ (Go interface + DirectTransport)，接入现有 ws.go
Step 3  PWA→Pieqi 迁移：先统一事件方向(Pieqi→PWA)；命令方向保留 REST (Phase 2.5 再迁)
Step 4  验证 Task / Event Streaming / Permission / ACP
Step 5  EventStore (序号 + replay buffer)   ← Phase 6 前置，提前
Step 6  RelayTransport + CF DO + 认证模型
Step 7  Pairing (带外信任根) + Identity
Step 8  E2E (X25519+HKDF+AES-GCM, nonce=keyId||counter, 重连重握手)
Step 9  Session Resume (session.resume + EventStore replay + permission pending 同步)
```

> 关键差异：把 EventStore 提前到 Relay 之前（Step 5），因为 Resume 的根基是序号化事件，且 Direct 阶段就能受益（stateful reconnect 而非全量 re-snapshot）。

---

## 6. 与现有抽象的复用边界（避免重写）

| 现有组件 | 方案对应 | 复用/改造 |
|----------|----------|-----------|
| [AgentManager](file:///workspace/internal/agent/manager.go) + `AgentAdapter` | §6 Transport 抽象的范式 | 仿照其 primary/fallback/并发范式，**不重写** |
| [PermissionWire](file:///workspace/internal/core/agent_perm.go) | §16 Permission | **包装**为 `permission.request/response` 消息，保留超时/先到先得语义 |
| [TaskStore](file:///workspace/internal/core/task_store.go) | §14 task state 恢复 | **直接复用**，task 状态持久化已具备 |
| [ws.go](file:///workspace/internal/api/ws.go) | §7 DirectTransport | **改造**：插入 Protocol 编解码层，补 Origin 校验/重连 |
| [EventBus](file:///workspace/internal/core/event_bus.go) | §15 fan-out | **保留** fan-out；**新增** EventStore 承担序号/replay |
| [bridge.go](file:///workspace/internal/core/bridge.go) NotifyOrigin | §16 IM 通知 | **保留**为通知通道，本阶段不升级为双向 |

---

## 7. 最终判断

方案在**战略与分层上优秀**，四原则（Protocol 统一 / Transport 可插拔 / Relay 不可信 / E2E 端到端）站得住脚，且与现有 [AgentManager](file:///workspace/internal/agent/manager.go)、[PermissionWire](file:///workspace/internal/core/agent_perm.go) 的抽象哲学一致——这是它能落地的基础。

但**不能直接进入实施**，原因有三：
1. 语言错配（TS vs Go）使 §6/§18 无法落地；
2. EventBus 现状（无序号、丢弃式）使 Phase 6 隐藏着重设计成本，须提前到 EventStore；
3. Relay 认证模型与配对信任根是安全前提，缺失则 E2E 形同虚设。

补齐 P0/P1 后，方案的"立即执行清单"（§24）可按修订路径（§5）推进。建议**先做 Step 0 前置三项**，再冻结协议。
