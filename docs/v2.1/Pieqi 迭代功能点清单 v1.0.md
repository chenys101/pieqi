# Pieqi 迭代功能点清单 v1.0

> 文档定位：v2.1 迭代执行清单  
> 前置文档：《Pieqi 下一阶段综合功能迭代规划 v1.0》（战略方向采纳，排期基于代码现状重新校准）  
> 校准日期：2026-09-07

---

## 1. 与规划文档的关系

《综合功能迭代规划 v1.0》的核心战略判断——不继续堆「Agent / IM / 页面」，把 Pieqi 从 Remote Control 升级为可靠 Task Runtime——完全采纳。但其 Phase 1 列为「立即做」的 P0 项与代码现状存在严重脱节：**约三分之二已经落地**。按其 Sprint 1–6 字面执行会把已有工作重做一遍。

本清单只保留真缺口。迭代主线：

> **把闭环做可靠，把入口做顺手。**
> 上一阶段回答了「能不能」（Observe→Verify→Intervene 闭环已落地），这一阶段回答「可不可靠、找不找得到」。

### 1.1 已核对现状（无需重做）

| 规划文档功能项 | 代码现状 |
|---|---|
| F-03 Feedback 完整闭环 | 已完成：`internal/core/` 下 feedback / git_diff / prospective / checks / evidence / outcome / preview / visual / push 全套，对应 docs/feedback/p0–p2 三期设计均已落地 |
| F-04 Approval→Prospective Diff | 已完成：`core/prospective.go` `ProspectiveDiff`，从工具入参生成前瞻 diff |
| F-05 Checks | 已完成：`DeriveChecks`（复用 agent 已跑结果，ADR-0005）+ `CheckRunner.Rerun` 重跑 |
| F-06 Task Outcome | 已完成：`outcome.go` `DeriveOutcome` |
| F-07 Evidence / Evidence→Continue | 已完成：`BuildEvidence` + `EvidencePrompt`（后端组装证据，ADR-0004），PushRegistry 已接 lark/wechat/webhook |
| F-08 Checkpoint / Rewind | 已完成且超出规划：除 `RewindToTurn` 外还有文件粒度 `RewindFileToTurn` |
| F-02 Event Store 之「WS 去重」 | 大半已解决：前端 normalizer 用 `taskId:seq` 去重，重连靠 snapshot 全量同步兜底 |

### 1.2 真缺口（本清单范围）

Event Store 写放大、INTERRUPTED 中断语义、Watchdog、Task Inbox 聚合、Cost/Token 透出、首次飞书绑定管理员。另砍掉规划中自相矛盾的 Pause（见 §7）。

---

## 2. 总览

| 编号 | 功能 | 价值依据 | 优先级 | 规模 |
|---|---|---|---|---|
| F1 | Event Store 事件外置 | 唯一阻塞项，解锁后续一切 | P0 | L |
| F2 | INTERRUPTED 中断语义 + 一键续跑 | 重启≠失败，真实痛点 | P0 | S |
| F3 | Watchdog 静默检测 | 手机端长任务静默无感知 | P0 | M |
| F4 | Task Inbox 聚合首屏 | 任务多了之后「找需要我处理的」 | P1 | M |
| F5 | Cost / Token 透出 | 数据已在载荷里，纯透出 | P1 | S |
| F6 | 首次飞书消息自动绑定管理员 | 绑定无产品化入口，闭环断裂 | P1 | S |

---

## 3. P0：本迭代必做

### F1 Event Store：事件流外置（地基，先做）

**现状问题**（三个）：

1. `Task.Events` 内嵌在 Task JSON 里（`internal/model/task.go:110`），每 append 一条事件走 `TaskStore.Update` 全量重写 JSON（`internal/core/task_store.go:96`），长任务下磁盘写是 O(N²) 放大。
2. 全部历史事件常驻内存。
3. 每次任务变更 WS 推完整 Task 副本给订阅者。

规划文档给 Event Store 的理由（WS 去重）站不住——已由前端去重 + snapshot 兜底解决。**真实动机是写放大、内存，以及给 Replay / 审计 / 跨任务查询打地基。**

**方案**（两步，同一迭代内完成）：

- (a) 存储外置：`~/.pieqi/tasks/<id>/` 下 append-only 事件文件（一行一事件），Task JSON 只留元数据 + lastSeq，内存索引也只挂元数据，详情视图按需读文件。旧格式在 `TaskStore.load()` 里一次性迁移（检测内嵌 Events → 拆出写事件文件 + 重写 Task JSON）。
- (b) WS 事件级推送：`task_updated` 全量副本改为带 seq 的事件级消息，前端 normalizer 已有去重逻辑，天然兼容；重连 snapshot 机制不变。

**范围外**：不上 SQLite、不做跨任务索引（留给下迭代）。ADR-0001 的「TaskEvent 是事实源」关系不变。

**验收**：

- 千级事件任务磁盘写次数线性、任务列表加载不再随事件数变慢
- 重启后 timeline 完整无损
- 旧任务文件自动迁移无人工干预

### F2 INTERRUPTED：重启中断语义 + 一键续跑

**现状问题**：`task_store.go:159` 把重启时的 running 和 approval 等待任务一律标 failed——「服务重启」和「agent 真失败」混淆，用户无法区分该重试还是该放弃。choice 等待跨重启保留 waiting_input 的行为已正确，不动。

**方案**：

- 新增 `TaskStatus` 值 `interrupted`
- `load()` 改为：running → interrupted；approval 等待 → interrupted；choice 不变
- 前端对 interrupted 任务展示「服务重启导致中断」说明，给三个动作：续跑 / 标记失败 / 放弃
- **续跑要诚实**：claude 进程已死，这是带着上轮上下文起新进程（复用现有 `--resume` 续问机制自动组装），**不是进程级断点续传**，文案不要暗示后者
- 续跑前校验 worktree 是否还在（被清理时给明确提示而非静默失败）

**验收**：

- 重启后 interrupted 任务一键续跑、上下文延续、历史 timeline 保留
- failed 列表里不再混入重启受害者

### F3 Watchdog：静默检测与分类通知

**现状问题**：全仓库无任何实现。手机场景高频痛点：任务跑两小时静默，用户不知道是死了还是在干重活。推送通道现成（PushRegistry 已接 lark/wechat/webhook），只差检测层。

**方案**：

- TaskRunner 维护每任务 lastEventAt（事件 append 时更新，F1 落地后顺手）
- 后台 ticker 巡检：running 且静默超阈值（默认 15 分钟，`pieqi.watchdog.*` 可配，默认开）→ 经 PushOrigin 推送
- **分类，不要误报**（规划文档说对的点）：先探桥/进程活性——agent 已死推「疑似挂了，建议取消或重启」；进程活着推「仍在运行，最近在跑 npm test」
- 只通知、不自动取消
- 推送内容带上最近一条事件摘要，让用户在手机锁屏上就能决定要不要管

**验收**：

- 模拟静默任务收到推送
- 正在跑长测试的任务收到的是「仍在运行」而非「挂了」
- 关闭开关后无推送

---

## 4. P1：尽快做

### F4 Task Inbox：「需要我处理」聚合首屏

**现状问题**：TasksPage 按项目分组、ApprovalsPage 单独一页，但核心移动场景是「打开 Pieqi，三秒内知道哪些任务要我处理」，现在要跨页翻。

**方案**：

- 聚合桶：待审批、待选择、已中断（F2）、失败、疑似卡住（F3 结果）、已完成未确认
- 每行：项目 · 标题 · 等待时长 · 一键动作（批/拒、选选项、续跑、看 diff、看 outcome）
- MVP 做前端聚合（task store 内存里已有全量）；后端 `/api/inbox` 聚合端点等任务量上来再后置
- 落地页选择：直接改造 Dashboard，或作为新默认页

### F5 Cost / Token 透出

**现状**：TurnEnd 载荷已设计携带 usage（`internal/agent/session.go` TurnInfo），但从未累积透出。

**方案**：

- task_runner 处理 EventTurnEnd 时累积到 Task 级统计（in/out tokens、请求数）
- 有模型价格表则算成本；没有就先只透 tokens——**不要为了凑成本数字编价格**
- 展示三处：任务列表行内轻量、Session 页头部、Outcome 附带
- 若桥侧 session.js 的 usage 字段未透全，先补桥

### F6 首次飞书消息自动绑定管理员

**现状问题**（比表面更糟——绑定管理员没有产品化入口）：

- `POST /api/auth/bind` 仅内网可调（`BindOpGateMiddleware`），V2 前端明确不做 bind 入口（`web/src/services/api/auth.ts` 注释「仅提示」）
- 用户要先拿到自己的 open_id（只能翻日志），再从内网机器 curl JSON 接口；SettingsPage 只显示一行「未绑定飞书账号」，无下一步指引
- 飞书侧同样没引导：未绑定时发普通消息回「请在 PWA 新建任务」，发「隧道」回「仅绑定的飞书管理员可操作」——都没告诉用户怎么成为管理员
- 鸡生蛋问题：外网访问要靠管理员在飞书发「隧道」命令，而绑定又要求内网——部署在外网机器上时闭环断了，只能 SSH 回内网操作

**方案**：服务端未绑定时进入 setup 态。飞书渠道（长连接或 webhook 均可，复用现有 `Bridge.handleMessage` 身份提取）收到**显式命令**「绑定」/「bind」即自动绑定为管理员，回复确认并附绑定时间；其他消息在 setup 态回复「发送『绑定』成为管理员」指路。此后消息走原逻辑。

**设计决定**：

1. **显式命令而非「首条消息即绑定」**：后者有抢注风险——飞书应用虽是 Device Flow 私建，但同组织内其他成员理论上可发现机器人并发消息。显式「绑定」命令把意图声明作为门槛。更保守的部署可配 `auth.setup_code`：设置后命令须带码（码打在服务端启动日志里，等价于「能读到控制台的人才是操作者」），默认不启用。
2. **原子 first-wins**：`Bridge.handleMessage` 每消息一个 goroutine，并发两条「绑定」会竞争。现有 `BindingStore.Bind` 是覆盖语义，需加 `BindIfVacant`（锁内检查未绑定才写入，返回是否抢到），第一个赢。
3. **解绑保持仅内网**：绑错人的补救路径不变（内网 `DELETE /api/auth/bind`，或停服删 `~/.pieqi/feishu_binding.json`）——根信任操作不放松。
4. **实现落点小**：`Bridge` 现在只拿 `AdminBinding` 接口（`bridge.go:27`，仅 `Match`），经 `EnableTunnelOps` 注入；扩展接口加 `BindIfVacant` 或单独注入 BindingStore，不动渠道层。绑定成功写一条审计日志（复用 `auth.AuditLogger` 模式）。
5. **前端顺手改**：SettingsPage 未绑定时显示「在飞书对话里发送『绑定』」，两条入口的指引对齐。用户从此不需要知道 open_id 这个概念。

---

## 5. P2：滚动池（本迭代不动，F1 落地后变便宜）

- **Task Replay 只读版**：Turn → diff → check → outcome 串看「代码是怎么变成现在这样的」。事件 + 每 Turn checkpoint 数据都在，F1 之后纯读路径开发。
- **截图标注 → Evidence**：visual-capture 已出截图，缺前端框选标注 + `EvidencePrompt` 扩展（ADR-0004 的后端组装路径现成）。
- **跨任务搜索**：「哪些任务动过这个文件」——Event Store 的查询红利。
- **Agent Capability Matrix**（规划 F-13）：等下一个新 agent 接入时一并做，现在做是纸上谈兵。
- **小件**：并发槽满时 pending 任务标「排队中」。

---

## 6. 排期与验收

依赖关系决定顺序：**F1 是唯一阻塞项（先做），F2 独立可并行，F3 依赖 F1 的事件时间戳（顺手），F5 独立小件可穿插，F4 + F6 前端为主放最后。** 建议三个小段：

```text
地基段：F1 + F2
可靠段：F3 + F5
入口段：F4 + F6
```

### 整迭代验收（真实场景，替代规划文档 DoD-01~05——其描述的能力已上线，无需重验）

1. 千事件长任务全程流畅，服务重启后 timeline 无损（F1）
2. 重启后 interrupted 任务手机一键续跑成功（F2）
3. 静默 15 分钟收到分类正确的推送（F3）
4. 打开 PWA 首屏即待处理清单（F4）
5. 任务结束 Outcome 含 token 统计（F5）
6. 全新部署：手机飞书发「绑定」即成为管理员，全程未接触 openid 概念（F6）

---

## 7. 明确不做（本迭代）

- **Pause**：claude 无原生 pause、ACP 无此协议能力、桥无处挂。规划文档的 capability matrix 里自己都标「?」，但 Sprint 4 却排进去了，自相矛盾。Cancel + 续问已覆盖实际场景，等上游有能力再说。
- **Web IDE / 云桌面 / 完整浏览器自动化 / 录屏 / 更多 IM**：ADR-0007 维持。
- **Workflow / Multi-Agent**：单任务可靠性（F1–F3）补齐前不启动，顺序判断与规划文档一致。
- **SQLite / 跨任务索引**：Event Store 第一期只做外置拆分，查询能力等需求真实出现再上。

---

## 8. 一句话收束

这六个点没有一个是新「页面」或新「Agent」，全部是在已有闭环上补 Recover 短腿、修数据地基、收敛注意力入口——正是规划文档战略原则（不堆功能、把 Remote Control 升级成可靠 Task Runtime）落到当前代码现状上的样子。
