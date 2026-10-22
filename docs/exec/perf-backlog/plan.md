# 执行计划：性能瓶颈修复队列（perf-backlog-2026-10-22）

> 源任务清单：`docs/performance/perf-backlog-2026-10-22.md`（**判据与实测基线以它为准，本文件不复制数字**）
> 本文件只回答三件事：**切了几段、每段谁做、每段的验收判据是什么**。

## 0. 为什么这份任务必须分段执行

它有 7 条任务、跨 Go 后端与 Vue 前端、每条都要求「改完**重新实测数字**并对比基线」。
若在一个会话里顺序做完，压缩会发生在 P1/P2 之间，**最先丢的恰恰是 P0 的实测新数字与
"哪些改法已被否掉"** —— 而后者正是这个仓库 ADR 体系要保住的东西。

关键前提（读 `AGENTS.md` 得到）：**这个仓库的全部验证都是"真机数字 + 隔离实例"**，
所以每一段的证据必须以文件形式留在 `evidence/`，不能留在对话里。

## 1. 隔离载体：沿用已存在的 worktree，不新建

`git worktree list` 实测已有两个，且都干净停在 `2b08d19`（0 commits ahead）：

| 目录 | 既有分支 | 本计划分配 |
|---|---|---|
| `D:\code\go\pieqi` | `main` | **不做实现**，只做计划/状态/证据/合并 |
| `D:\code\go\pieqi-perf-a` | `perf/p0-update-merge` | 段 A = P0-1 |
| `D:\code\go\pieqi-perf-b` | `perf/p0-payload` | 段 B = P0-2 + P1-3（同为下发体积轴） |

P1-1/P1-2 是前端改动、与后端无冲突，可在 `main` 新建一个 worktree 或直接在段 B 之后做；
**判据：只要不与段 A 改同一批文件（`internal/core/agent_toolcall.go`）即可。**

## 2. 分段（按"可独立封板"切，不是按任务号切）

每一段 = **一个全新会话**，读 `state.md` 开工，结束时写回 `state.md` + `evidence/<段>/`。

### 段 A · P0-1 合并重复 Update（最高优先级，独占一条轨道）

- **依赖**：无。**必须先于段 B**（段 B 的收益要在写锁不再抢占的前提下才测得准）。
- **改动面**：`internal/core/agent_toolcall.go`（主）、可能新增 `Flush` 钩子并在
  `ToolCallHandle.Unwire` 调用；测试新增一条"一条 ToolCallUpdate 只落盘一次"。
- **必读**：`task_store.go` 的 `Update` 上方长注释（写锁为什么贯穿到落盘结束）。
- **不许**：提前释放写锁。
- **封板判据**：`go test ./internal/core` 全绿 + 新测试红过再绿 + `evidence/A/` 里有两个数字
  （工具事件数→Update 次数、并发 p95）且与 backlog 基线并列。
- **会话结束动作**：在 `perf/p0-update-merge` 上提交（只 `git add --` 自己的文件）。

### 段 B · P0-2 + P1-3 下发体积（依附段 A 的产物测）

- **依赖**：段 A 合并进 `main` 后，把 `main` 合入 `perf/p0-payload` 再开工。
  **理由**：payload 体积的改善会被「写锁串行」掩盖，先修锁才能量出真实的首屏收益。
- **改动面**：`internal/model/task.go`（`TaskSummary`）、`internal/api/tasks.go`（列表 handler 内已实测可做窄化）、
  压缩中间件挂载点。
- **封板判据**：两台隔离实例（改前/改后）的 `GET /api/tasks` 体积 + gzip 前后体积，四组数字并列。
- **风险点（见 §4）**：WS `task_updated` 是全量 `*model.Task`，**不经过 TaskSummary**；
  只窄化列表视图时，单任务 2.4 MB 的 WS 推送仍在。这一点 backlog 未写，段 B 开工时**先量再决定范围**。

### 段 C · P1-1 + P1-2 前端渲染（纯前端，可并行于 A/B 之外）

- **依赖**：无（不同文件树）。但**验收必须真机**，且要在段 A/B 仍未合并的 `main` 上做，
  避免混淆归因。
- **封板判据**：`npm run test` 绿 + `npm run test:e2e:impact` 的相关用例绿 + 真机帧耗时/掉帧数字。
- **静默失败风险**：P1-2 去掉 `deep: true` 后，自动折叠若失效**不报错** —— 用例必须覆盖
  「一轮结束」「用户已发下一轮」两个时机 + 「手动展开后不被折回」。

### 段 D · P2-1 / P2-2 / P2-3 结构性改动

- **依赖**：**全部在 A+B 之后**（P2-1 与 P0-2 共用同一接口；P2 的收益要在体积已降后评估是否还做）。
- **开工前判据**：若 A+B 之后首页体积已达标，P2-1 **可能应当被关掉而不是实施**。
  所以段 D 的第一步是「用新数字重新判断这三条还成不成立」，不是直接动手。

### 段 E · P3-1 确认类任务

- **不写代码**。只产出 `evidence/E/homepage-network.md`：30 秒 Network 观察结论。
- 没有固定频率请求 → 在 backlog 里标记为「关闭」，不要留成永远的 TODO。

## 3. 每段的通用封板纪律

1. **先测基线，再改**。基线数字写进 `evidence/<段>/before.md`，不允许"凭 backlog 里的数字代替实测"
   （backlog 自己的「执行须知 2」要求重新实测）。
2. **改完重新测**，写入 `after.md`，两文件并列，**差值用算式写出**。
3. **提交只 `git add --` 显式路径**（`AGENTS.md` 硬约定，多会话并行）。
4. **封板 = 一次提交**（提交信息正文写判据与被否掉的方案），**不是**"代码写完"。
5. 段结束必须更新本目录的 `state.md`（唯一交接载体）。

## 4. 执行期新发现（截至 2026-10-22，待段 B 处理）

> 这些是读代码得来，**尚未实测**。写在这里是为了不让下一个会话重新踩一遍。

- **F1｜WS `task_updated` 载荷是完整 `*model.Task`**（`internal/core/event_bus.go` 的 `Event.Task`），
  `internal/api/ws.go:75` 直接 `json.Marshal(ev)` 下发 —— **不经过 `NewTaskSummary`**。
  因此 P0-2 收窄 `TaskSummary` **不会**减小这条链路。按 backlog 自己的诊断（单任务 2.4 MB、
  `output` 1.66 MB），**「卡着不动」的训练数据恰好走的就是这条路径**。
  → 段 B 必须先量 WS 消息体积，再决定是否把 P0-2 的范围扩到这里。
  ⚠️ 但收窄这里有真实语义风险：前端 `syncFromTask` 依赖 `task.events` 覆盖式同步，
  `output` 是文本流渲染源。**不能照抄列表的窄化做法**，需单独设计（例如给 WS 带 `?lite=1` 或只带增量）。
- **F2｜`listTasks` 在 handler 内可直接窄化**（`internal/api/tasks.go:163-188` 自己持有 `*model.Task`，
  且落盘 `Task.Baseline` 与 API DTO 是两回事），因此在 API 层遮蔽 `Baseline` 即可，
  **不必改 `TaskSummary` 的类型定义**。代价：`NewTaskSummary` 的其它调用方（WS 快照）不受影响。
- **F3｜`Event.Task` 是 `*model.Task` 指针，跨订阅者共享**：若为了让 WS 轻量化而原地裁剪该指针，
  会改到 store 里那份对象。**任何裁剪都必须先深拷贝**（`internal/model` 目前**没有** `Task.Clone`，
  实测 grep 无结果），这是一个独立的、容易被忽略的内存/正确性成本，要计入段 B 的估算。

## 5. 不做的事

- 不改 `TaskStore.Update` 的锁语义（backlog 明确禁止，且有事故记录）。
- 不为"跑通"而改沙箱/缓存落点（`AGENTS.md` 硬约定）。
- 不把视觉反馈相关代码当参考（ADR-0012/0013 已整体移除）。
