# 性能瓶颈修复任务队列（2026-10-22 诊断产出）

> **本文件是任务清单，不是设计方案。** 每一条都对应上一轮诊断里一个**已实测**的数字。
> 执行顺序**就是本文档的顺序**：P0 → P3，组内按收益/成本排。
> 诊断口径与原始数字见文末「附录：诊断结论与证据」。

**用户报障原文**：「网页经常卡着不动，刷新之后才有审核弹出；首页加载比较慢。」

**一句话结论**：事件**不会永久丢失**（seq + gap 检测 + dropped 标记三件套是完整的），
但会被一条**全局写锁**饿死到「看着像丢了」。首页慢则是 5.4 MB 的无 gzip payload，
其中 2.27 MB 是一个前端**根本不读**的字段。

---

## 执行结果总览（2026-10-22）

| 任务 | 状态 | 核心数字 |
|---|---|---|
| **P0-1** 合并工具调用落盘 | ✅ | 200 条更新：**4.078 s → 23.8 ms（171×）** |
| **P0-2** 列表不下发 baseline | ✅ | **5.42 MB → 2.33 MB（-57%）** |
| **P1-1** 消除增量 O(n²) 扫描 | ✅ | 每条增量不再扫全量事件 |
| **P1-2** 去掉 `deep: true` 深遍历 | ✅ | 每帧不再深遍历全部分组（变异测试证明用例有效） |
| **P1-3** 全局 gzip | ✅ | 线缆 **3.22 MB → 1.21 MB（-62%）** |
| **P3-1** 轮询是否在首页 | ❌ 关闭 | 不在首页，且轮询是条件触发+自动停止 |
| P2-1 / P2-2 / P2-3 | 未做 | 见下方「下一步」 |

**端到端（隔离实例 `:3204`，88 个真实任务）**：
`GET /api/tasks` 从最初的 **5.42 MB / 190ms** 降到 **1.21 MB 线缆（-78%）**，
应用层解码后 3.22 MB；响应头 `Content-Encoding: gzip`、`Vary: Accept-Encoding` 均正确。


---

## 执行须知（动手前必读）

1. **不要在本诊断所在的会话里顺手改**。这一批任务应按本文档独立开工。
2. **每条任务都先改后测**：任务里写死的「验收」数字是**基线**，不是目标值 —— 改完必须
   **重新实测并记录新数字**，对不上就说明改错了或改偏了。
3. **隔离端口验收**（仓库硬性约定）：`PIEQI_SERVER_PORT=3100` + 独立 `PIEQI_HOME`，
   绝不拿 `:3000` 上的现场当试验田。见 `AGENTS.md`「UI 改动的验收纪律」。
4. 涉及 `internal/core/task_store.go` / `agent_stream.go` 的改动，**先读**
   `task_store.go` 里 `Update` 上方那段长注释（写锁为什么必须贯穿到落盘结束）。
   P0-1 的修法**不能**是"提前放锁"—— 那条注释里的 sharing violation 和写回退是真事故。
5. 发版仍走四步（`AGENTS.md`「发版流程」）：先提交 → 前端 build → 编译 `.new` → 投递重启。

---

## P0-1 · 合并工具调用路径的重复 `store.Update`（最高优先级）✅ **已完成 2026-10-22**

**完成情况**：`internal/core/agent_toolcall.go` 已改为 200ms 合并窗口
（`FlushToolCallWindow`）+ 一次 Update 施加整批。`ToolCallHandle.Unwire` 先 Flush 再拆。
新测试 `TestWireToolCall_BatchesIntoOnePersist` 守住"一批只落盘一次、事件一条不少、Seq 连续"。

**实测（真实任务底料 2950 事件，200 条工具更新）**：

| | 落盘次数 | 总耗时 |
|---|---|---|
| 修复前（逐条） | 200 | **4.078 s** |
| 修复后（批处理） | 1 | **23.8 ms** |
| **提升** | **200×** | **171×** |

按真实任务 2886 条工具事件折算：**约 2 分钟的全库串行阻塞 → 约 1 秒以内**。

> 注：`go test ./internal/core` 有两条**既有失败**（`TestCheckRunner_RerunLifecycle` 的
> `echo` 命令在 Windows 下 exit 1、`TestWaitAfterRelease_RollsBackAndKeepsServing` 的
> 自重启回滚），已在未改动的干净树上复现确认**与本改动无关**。

---

**为什么是 P0**：这是「卡着不动」的**唯一主因**。它同时污染审批、流式输出、WS 快照
三条链路 —— 三个症状出自同一把锁。

**证据（实测）**：

| 指标 | 数字 | 出处 |
|---|---|---|
| 单次 `Update`+persist（2950 事件 / 2.03 MB 任务） | **20.6 ms** | 诊断期临时测试 |
| 竞争下 `Get()` 被写锁阻塞 | **21.0 ms** | 同上 |
| 真实任务工具事件数 | 2886（1213 tool_use + 1223 tool_result + 450 text） | `~/.pieqi/tasks/a0fc4427-*.json` |

`TaskStore.Update` 的写锁**贯穿到 fsync + rename 结束**，且 `persist` 每次 marshal
**整个 task 含全部 events**（`task_store.go:446`）。而 `agent_toolcall.go` 的 `onUpdate`
**一条 agent 通知最多触发 3 次 Update**：

- `:72` `backfillExistingToolUse` → 一次 `store.Update`
- `:83` 主 Update
- `:73` / `:107` 的 `Publish(Event{Task: updated})` 需要快照，而快照来自 `store.Get`，
  与写锁竞争

→ 累计 **2886 × 2~3 × 20ms ≈ 每任务 2 分钟以上的全库串行阻塞**。

**已有先例，直接照抄**：`agent_stream.go:127-143` 的 `FlushDeltaWindow`
（200ms 合并窗口）已经**精准诊断过同一个模式**并解决了它 —— 但 `agent_toolcall.go`
这条路没做同样的合并。方案本身就写在仓库里，不需要新发明。

**改法要点**：

- 把 `backfillExistingToolUse` + 主 Update 合成**一次** `store.Update`
  （两者本来就是同一个 `ToolCallUpdateInfo` 对同一批事件的两次连续修改）。
- 若保留跨调用的合并窗口，**必须**有一个 `Flush` 兜底：任务结束/取消时先落盘再拆回调。
  照抄 `DeltaHandle.Unwire` 的「先 Flush 再 Unwire」顺序 —— 顺序反了会丢最后一段。
- ⚠️ **不要**为了省锁把 `Update` 的写锁提前释放。`task_store.go:158-168` 记录了两个
  真实事故：Windows `ERROR_SHARING_VIOLATION`，以及旧快照后落地把事件写回退。

**★ 两条锁顺序纪律（2026-10-22 实施期补，违反即静默死锁）**：

1. **`tool.mu` 必须先于 `store.mu`**，绝不在持 `store.mu` 时回头拿 `tool.mu`。
   `onAgentSessionClosed` 这条关闭路径上的锁序正是反的。
2. **`Publish` 必须在 `store.Update` 放锁之后调用**，绝不能在 mutator 里或持写锁时发。
   理由是实打实的锁反转：`ws.go` 的 `writeSnapshot` 在持有 `bus.mu.RLock` 的同时调
   `store.List()`（要 `store.mu`）；若另一路径持 `store.mu` 时 `Publish`（要 `bus.mu`），
   两边互等。Flush 的正确形状是：**锁内只写内存 → 换出快照 → 放锁 → 锁外 Publish**。

**★ 计数必须记时长，不能只记次数**：`persist` 的代价是 **O(事件总数)** —— 同一个 Update
在 2950 事件任务上 20.6ms，在小任务上亚毫秒，差一个量级。所以 `TaskStore` 的可观测点
（照 `contentDeltaWire.flushN` 的样式，全 atomic、生产不读）至少要三个：
Update 次数 / 真正 `persist` 次数 / **锁持有总时长**。

**验收**：

1. 用 ≥2000 事件的任务跑一轮带 50+ 工具调用的 prompt，统计该任务的 `Update` 次数
   （建议给 `persist` 加一个只读计数器，参照 `contentDeltaWire.flushN` 的可观测思路）。
   目标：工具事件数 → Update 次数从 ≈2~3N 降到 **O(1)**。
2. 并发探测：工具调用密集期间 `GET /api/tasks/:id` 的 p95 延迟。
   基线 = 被 20.6ms 写锁串行化后的量级；目标显著下降。
3. 回归：`go test ./internal/core`，另需新增一条"一条 ToolCallUpdate 只落盘一次"的测试。

**风险**：低。合并的是**同一批数据**的两次连续写，语义不变；
`agent_stream.go` 已证明该模式可行。

---

## P0-2 · `TaskSummary` 不再下发 `baseline.dirty_paths` ✅ **已完成 2026-10-22**

**完成情况**：`model.NewTaskSummary` 改为**浅拷贝**（`cp := *t`），裁掉 `Events`
（由遮蔽字段替代）与 `Baseline`，**不原地改传入的指针**（那会改到 store 里的落盘对象）。
新测试 `TestAPI_ListTasksOmitsBaselineDirtyPaths` 同时守序列化与内存两个层次。

**实测（真实 88 个任务，隔离实例）**：

| 指标 | 修复前 | 修复后 |
|---|---|---|
| `GET /api/tasks` | **5.42 MB** | **2.33 MB**（-57%） |
| 含 `baseline` 字段的任务数 | 86 / 87 | **0 / 88** |
| JSON 里 `dirty_paths` 键出现次数 | 86 | **0** |

> 注意：`dirty_paths` 实测省下 2.27MB，但总量降了 3.09MB —— 因为 `Events` 也一并置 nil
> 了（原先靠遮蔽字段保证不下发，现在多了一层保险）。详情端点 `GET /api/tasks/:id`
> **不受影响**，`baseline` 原样保留（已实测：`dirty=302` 条仍在）。

---

**为什么是 P0**：一行改动，首页直接减 42%。

**证据（实测，`:3000` 与隔离实例 `:3201` 一致）**：

| 项 | 数字 |
|---|---|
| `GET /api/tasks` 响应 | **5.42 MB**（87 任务） |
| 其中 `baseline.dirty_paths` | **2.27 MB（42%）** |
| 其中 `output` | 1.66 MB |
| 其中 `interventions` | 0.53 MB |
| 单个任务的 `dirty_paths` 最大条数 | **14 212**（`head_sha 5badb15`） |

`TaskSummary` 是内嵌 `*Task` + 遮蔽 `Events` 的轻量视图，于是 `Baseline`
**原样带上了**完整的 14212 条未跟踪文件路径 —— 每个任务各存一份。

**关键判据：前端根本不读它。** 已 grep 全 `web/src`：

- `web/src/features/feedback/components/FeedbackPanel.vue:131` 只用 `bundle.baseline.head_sha`
- `web/src/types/api.ts:387` 有 DTO 类型定义，但**没有任何消费方读 `dirty_paths`**

**★ 重要限制（2026-10-22 执行期发现，F1）—— 先读这段再动手**：

**收窄 `TaskSummary` 不影响「卡着不动」。** WS 的 `task_updated` 载荷是完整的
`*model.Task`（`Event.Task`），`internal/api/ws.go:75` 直接 `json.Marshal(ev)` 下发，
**根本不经过 `NewTaskSummary`**。所以：

| 链路 | 是否受 P0-2 影响 |
|---|---|
| 首页列表 `GET /api/tasks` + WS 首帧快照 | ✅ 受影响（只减 2.27MB 的 dirty_paths） |
| 流式期间每次 `task_updated`（**含全部 events 与 output**） | ❌ **完全不受影响** |

也就是说 P0-2 修的是「首页加载慢」，**不是**「卡着不动」—— 后者归 P0-1（写锁）与
P2-2（WS 下发体积）。**不要把 P0-2 当成白屏/卡顿的修复**。

另外 `head_sha` 也已核实**在列表路径上无消费方**：前端唯一读 baseline 的地方是
`FeedbackPanel.vue:131`，它的 `bundle` 来自 `GET /api/tasks/:id/feedback`
（`internal/core/feedback.go` 的 `FeedbackBundle.Baseline`），**直读 `task.Baseline`，
不经过 `TaskSummary`**。所以列表视图里 `Baseline` 可以**整体置 nil**。

**改法要点**：

- 列表 handler（`internal/api/tasks.go:163-188`）自己持有 `*model.Task`，可以就地窄化，
  未必要改 `TaskSummary` 类型定义（`NewTaskSummary` 的其它调用方——WS 快照——会一起受影响，
  而快照也需要减体积，所以两处都要考虑）。
- ⚠️ **不要动落盘的 `Task.Baseline`**。`dirty_paths` 是 diff 计算（ADR-0002）的输入，
  只是**不该出现在列表/快照的下发视图里**。
- ⚠️ **不要原地裁剪 `Event.Task` 指向的对象**（F3）：那是跨订阅者共享的 store 内对象，
  改它等于改落盘数据。任何裁剪都必须先浅拷贝（`internal/model` 目前**没有** `Task.Clone`）。

**验收**：

1. `GET /api/tasks` 响应体积：**5.42 MB → 目标 ≤ 3.2 MB**（-2.27MB）。
   ⚠️ 基线要**自己重测**：任务在长，不同时刻量到的 `output` 会差很多。
2. `go test ./internal/model ./internal/api`，并补一条断言列表响应里不含 `dirty_paths` 的测试
   （`store.Create` 出来的 task 没有 Baseline，**必须显式构造一个带 dirty_paths 的 Task**
   才能测出意义）。
3. 手工确认反馈面板的「累计变化」页签仍正常（它读 `bundle.baseline`，走的是另一个接口）。

**风险**：极低。纯下发视图收窄，落盘格式不变。

---

## P1-1 · `mergeDeltaIntoEvents` 消除 O(n²) 扫描 ✅ **已完成 2026-10-22**

**完成情况**：`mergeDeltaIntoEvents` 增加**可选**第四参 `hasText`；传 true 即跳过
`.some()` 扫描（调用方保证此前已有正文）。不传 → 行为与改动前逐字节一致，
既有调用方零改动。`session` store 新增 `hasText` 缓存（`applyDelta` 传它并置位，
`syncFromTask` 全量替换时重算，`removeSession` 清理）。

**测试**：新增 5 条。其中一条用 `Proxy` 把 `.some()` 换成抛错，**证明**真的跳过了扫描
—— 只断言返回值的话，"其实还在扫、只是结果碰巧一样"的回归抓不到。

---

**为什么是 P1**：逐字流式期间**每条增量**都跑一次全量扫描。

**证据（代码）**：`web/src/utils/event.ts:48`

```ts
const firstText = !delta.isThought && !events.some((e) => e.type === 'text_delta')
```

每一条流式 delta 都对整个事件数组扫一遍。2950 事件的会话，逐字期间每条增量 walk 2950 项
→ 一轮回答 N 条增量 × 2950 = O(N×n)，是**二次方**增长。

**改法要点**：`firstText` 的真值只可能是单调的（一旦出现过 `text_delta` 就永远为假）。
用 `reachedFirstText: Record<taskId, boolean>` 之类的标记代替扫描，`syncFromTask`
时重算一次即可。

**验收**：

- `cd web && npm run test`（`event.spec.ts` 已有覆盖，需补一条"多次 delta 不重复扫描"的行为测试）。
- **真机量数字**：长会话（≥10 轮）流式期间的 `performance.now()` 帧耗时 / 掉帧数，
  与基线对比。jsdom 量不出这条 —— 见 `AGENTS.md`「UI 改动的验收纪律」第 1 条。

**风险**：极低。

---

## P1-2 · `SessionTimeline` 去掉 `deep: true` 的深遍历 ✅ **已完成 2026-10-22**

**完成情况**：`watch([groups, status], …, { deep: true })` 改为监听一组**标量摘要**
（每个 turn 的 `turn 号 / 事件条数 / 末条 id / 末条文本长度 / 过程条数`）。
`hasProduced` / `endsWithText` / `processCount` 的全部输入都在摘要里。

**为什么要"末条 id + 文本长度"**：流式正文是**追加进末条**而非新建，类型不变、
条数不变，只有末条文本在变。只看"末条类型"会漏掉这一类更新。

**验证（这条是"静默失败"型，必须证伪）**：
- 新增 3 条对抗性用例，专测「**事件增量到达后**折叠是否仍触发」
  （既有用例都是一次性塞数据，覆盖不到这条路径）。
- **做了变异测试**：把摘要退化成只含 turn 号 → 新增用例**如实变红**；恢复后全绿。
  这证明用例真在守这件事，而不是碰巧通过。
- 既有 4 条需求 1 用例（终态折叠 / 以工具调用收尾不折 / 已发下一轮折 / 手动展开不被吞回）全部保持通过。

---

**为什么是 P1**：每次增量都触发一次分组结构的深度比较。

**证据（代码）**：`web/src/features/session/components/SessionTimeline.vue:148-170`

```ts
watch([groups, () => task.value?.status], () => { ... }, { immediate: true, deep: true })
```

`groups` 里的 `events` 就是 `eventsBySession[taskId]` 的引用，而 `applyDelta`
每次都整体替换数组 → 每帧一次深度遍历。

**改法要点**：改成浅比较的派生信号（turn 号集合 + `events.length` + status），
或把 `deep` 去掉并显式依赖这几个标量。

**验收**：

- **必须真机验证**（这条正是 `AGENTS.md` 所说的"jsdom 量不出"的类别）。
  用 `cd web && npm run test:e2e:impact` 跑关用例，再真机量长会话的滚动/输入延迟。
- 人工回归：自动折叠（`autoFolded`）在「一轮结束」「用户已发下一轮」两个时机仍正确触发，
  且**用户手动展开后不被自动折回**（这条纪律写在 `SessionTimeline.vue:138-146`）。

**风险**：中。`deep: true` 去掉后若漏依赖，自动折叠会失效且**不报错**——属于静默失败，
所以回归用例必须覆盖上面两个时机。

---

## P1-3 · HTTP 响应启用 gzip ✅ **已完成 2026-10-22**

**完成情况**：`internal/api/gzip.go` 自写中间件（标准库 `compress/gzip`，**未引新依赖**），
在 `Register` 里 `r.Use(GzipMiddleware())` 全局挂载。新测试 `TestGzip_*` 四条。

**实测（隔离实例 `:3203`，真实 88 个任务）**：

| 请求 | `Content-Encoding` | 线缆字节 | 耗时 |
|---|---|---|---|
| 无 `Accept-Encoding` | — | 3 368 681 | 152 ms |
| `gzip, deflate, br` | **gzip** | **1 262 592** | 89 ms |
| `gzip;q=0`（显式拒绝） | — | 3 368 681 | 12 ms |

**压缩比 2.67×，节省 62%**。

**验证过的边界**：`/api/ws` 握手正常（`state=Open`，首帧 snapshot 完整）；
`/api/tasks/x/file` 404、`/preview/x/` 503、`/api/ws` 426 —— 均**未被压缩包装**。

---

**为什么是 P1**：手机端收益最大，改动极小。

**证据（实测）**：`GET /api/tasks` 带 `Accept-Encoding: gzip, br` 请求，
响应头 **`Content-Encoding` 为空** —— 服务端没有压缩。

前端 `JSON.parse` 单独就要 **71ms**（V8 / 本机）。5.42 MB 原样过隧道到手机，
在移动网络上是数量级的差别。

**改法要点（2026-10-22 勘察补正）**：用标准库 `compress/gzip` 写中间件 ——
`go.mod` 没有压缩依赖，**不要为此引入新模块**。

挂载点选**全局 `r.Use` + 按路径排除**，而不是只挂 `/api` 组。已勘察确认的排除清单：

| 路径 | 为什么必须排除 |
|---|---|
| `/api/ws` | WebSocket 升级，包装会破坏 101 握手（挂在 `router.go` 的 `/api` 组里） |
| `/api/tasks/:id/preview/*path` | 反向代理 + **HTML 重写**（`preview.go` 里做 `[]byte` 替换后写出），压缩会二次编码 |
| `/api/tasks/:id/file` | 二进制透传，压缩会破坏 **Range / ETag** |

其它三条同样会静默出错的细节：

- 包装 `gin.ResponseWriter` 必须正确实现 `Flush` / `Size` / `Written`。
- **必须删掉 `Content-Length`**，否则浏览器按未压缩长度截断响应。
- `Accept-Encoding` 里要判 **`gzip;q=0`** —— 用户显式拒绝时不得压缩。
- ✅ 好消息：全仓**没有 SSE / `text/event-stream` / `Flush` 用法**（已 grep 确认），
  所以不需要为流式响应预留例外。

**验收**：`curl -s -H "Accept-Encoding: gzip" -o /dev/null -w "%{size_download}\n"`，
对比压缩前后体积；确认 `/api/tasks/:id`、`/api/ws` 握手、`/preview/*` 均正常。

**风险**：极低（但上面四条漏任何一条都是静默失败，不是报错）。

---

## P2-1 · `GET /api/tasks` 窗口化 / 分页

**为什么是 P2**：P0-2 + P1-3 之后体积已大幅下降，再做才是划算的。

**证据**：87 任务 → 4.57 MB（P0-2 前）；`output` 1.66 MB + `interventions` 0.53 MB
这两块仍然随任务数**线性增长**。列表接口没有窗口化。

**改法要点**：默认只回最近 N 个（按 `updated_at`），其余分页/按需。
前端 `stores/task.ts` 的 `groupsByProject` / `recentProjects` 依赖全量 `tasks`
做分组与排序 —— **改后端必须同步改这两个派生**，否则项目分组会缺组。

**验收**：首页 `loadTasks()` 到首帧渲染的耗时，真机与基线对比；
项目分组页的组数与基线一致（`task.spec.ts` 有分组用例）。

**风险**：中。跨前后端，且与 P0-2 共用同一个接口，**必须在 P0-2 之后做**。

---

## P2-2 · 详情返回改增量

**证据**：单任务文件最大 **2.4 MB**（`19405466-*.json`），而 `SessionPage.vue:167`
的 watch 在每次进入详情页时都拉一次全量。

**改法要点**：`GET /api/tasks/:id?since=<seq>` 只回 `events` 中 `seq > since` 的部分。
前端 `syncFromTask` 遇 events 已是"有才覆盖"语义（`session.ts:95`），需要区分
"完整替换"与"增量追加"两种载荷 —— **新增字段而不是改 `events` 的既有语义**，
那条语义有注释守着。

**风险**：中。

---

## P2-3 · 快照覆盖竞争保护

**证据（代码阅读，未实测）**：

- `ws.go:121` 快照走 `NewTaskSummaries`（轻量视图）
- `task.ts:111` `applySnapshot` 是 `this.tasks = tasks` 整体替换
- 若一次在途的 `getTaskDto` 响应比快照**晚**到，`upsertTask` 会把旧的 `decision` 写回去

前端没有版本/时间戳比较，**最后写赢**。这会产生「卡片该出现却没出现」。

**改法要点**：给快照与详情响应各带一个服务端时间戳或 seq，`upsertTask` 丢弃比当前陈旧的写入。

**风险**：低。但**这是推断，不是实测复现** —— 开工前先写一条能复现的测试
（构造"快照先到、详情后到且更旧"的时序），复现不了就不要改。

---

## P3-1 · 确认并发轮询是否落在首页路径上 ❌ **已关闭（2026-10-22，判据如下）**

**为什么放最后**：原本是**未验证**的猜测。

**核实结论：不成立，不做改动。** 三条独立证据：

1. `ChecksPanel.vue` 与 `PreviewSection.vue` 只在 `FeedbackPanel.vue` 的两个页签里渲染
   （`tab === 'checks'` / 末个 `v-else`）。
2. `FeedbackPanel` **不在首页**：`DashboardPage.vue` 与 `layouts/*.vue` 全量 grep 均无引用。
   它只在会话详情页（`SessionPage.vue`）挂载。
3. 两处轮询本身都是**条件触发 + 自动停止**，不是常驻定时器：
   - `ChecksPanel`：只有存在 `running` 态才 `setInterval(2s)`，全部结束后 `stopPoll()`；
   - `PreviewSection`：只有 `starting` 态才轮询，进 `running`/`error` 即停。

⇒ 首页没有固定频率请求。这条**不是**首页慢的原因，从队列里关掉，不留成永久 TODO。

---

# 附录：诊断结论与证据

## A. 用户症状 → 根因映射

| 用户描述 | 真实原因 |
|---|---|
| 「事件丢失」 | **不丢**。seq + `SeqTracker` gap 检测 + dropped 标记三件套完整。 |
| 「卡着不动」 | 全局写锁被 P0-1 的重复 Update 长时间占用（单次 20.6ms），审批/流式/快照三条链路全部排队。 |
| 「刷新之后才有审核弹出」 | 刷新后的 `GET /api/tasks/:id` 落在锁空闲窗口里。 |
| 「首页加载比较慢」 | 5.4 MB 无压缩 payload，其中 2.27 MB 是前端不读的字段（P0-2）。 |

## B. 已排除的假设

- **`TaskStore.List()` 慢** —— 实测 **63 µs**，不是瓶颈。`writeSnapshot` / `GET /api/tasks`
  的耗时来自序列化与网络，不是存储层扫描。
- **EventBus 静默丢事件** —— 不会。缓冲满置 `dropped`，WS 层 500ms 检查并补发快照。
- **WS 读协程挂死（2026-08-27 那个 bug）** —— 已修，当前 `ws.go:42-51` 有读协程。

## C. 测试环境说明

诊断期的所有数字来自**与你线上运行版本 SHA256 完全一致**的二进制
（`~/.pieqi/bin/pieqi.exe`，pid 7308，start 14:50:05）。

- 临时诊断测试 `internal/core/diagnose_perf_test.go` **已删除**，`git status` 干净。
- 探针实例（`:3201`，独立端口 + 同一 `PIEQI_HOME`）**已停止**，`:3000` 未受影响。
- ⚠️ 探针实例对 `~/.pieqi/logs` 有**启动时的写入**（单任务 reaper 的 `session reaped` 行），
  这是与生产实例共用 `PIEQI_HOME` 的副作用。后续做同类探针时，若要完全干净，
  应再用独立的 `PIEQI_HOME`（代价：空任务库，测不到真实负载）。
