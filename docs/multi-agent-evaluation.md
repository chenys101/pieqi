# pieqi 多 Agent 技术方案（修订版）评估意见

> 评估对象：《pieqi Agent 技术方案（修订版）》[multi-agent.md](multi-agent.md)
> 评估依据：方案文本 + 当前代码库实现（`internal/agent`、`internal/core/agent_perm.go`、`internal/config`）
> 评估日期：2026-08-20
> 评估结论：**抽象方向正确、分层干净、验收可测；但方案通篇未与现有 `internal/agent` 实现做任何映射——现有 `AgentAdapter`/`AgentManager`/`PermissionWire` 已实现方案 §3/§4/§8 的大半，P1–P5 大量是"重命名 + 重组 + 接线"，工作量被高估。真正的新工作与唯一技术风险集中在 P0 的 claude-sdk-bridge。**

---

## 0. 总体结论

### 0.1 评分总览

| 维度 | 评分 | 说明 |
|------|------|------|
| 战略方向 | A | 业务只认 agent 名、传输关在实现里、print 保底——方向正确，与现有抽象哲学一脉相承 |
| 与现有代码对齐 | C | 方案未与 `internal/agent` 做映射，阶段计划因缺失此映射而失真 |
| 核心抽象（AgentSession+Caps） | A- | 中性接口设计好；但接口不完整（缺 `RespondPermission`，事件投递方式未定） |
| Claude 桥（sdk-bridge） | B | 成立前提（claude-agent-acp 常退）已在上次会话验证；常驻 Node 服务本身是真实新增成本 |
| 验收与分期 | A- | 验收清单具体可测、P0 排第一正确；桥自身崩溃、SSE 重连、配置迁移、目录重构未覆盖 |
| 落地可执行性 | B | 未给出与现有 Go 代码的迁移映射，阶段里含隐性重命名成本 |

### 0.2 三句话结论

1. **抽象是对的**：`AgentSession + Caps + 中性事件` 与现有 `AgentAdapter`/`AgentManager`/`PermissionWire` 一脉相承，方案是在正确方向上演进，不是另起炉灶。
2. **最大断层是"方案没提现有代码"**：`internal/agent/` 的 adapter.go/acp.go/print.go/manager.go 与方案 §3/§4/§8 几乎一一对应，P1–P5 大半是重命名与接线，工作量被高估。
3. **唯一技术风险是 bridge 本身**：官方 SDK 常驻多轮（loop 形状）、权限挂起、崩溃 resume 三个点，**已由 P0 spike 实证通过**（见 §8）——"claude-agent-acp 常退"已在上次会话验证，**不是**待验证项。

---

## 1. 与现有代码库的对齐分析（最关键）

> 方案全文未出现对 `internal/agent` 现有实现的引用。以下逐条对照——这是评估里最重要的一节，直接改变阶段工作量估计。

### 1.1 抽象映射表（方案 §3/§4/§8 ↔ 现有代码）

| 方案概念 | 现有代码 | 差距 |
|----------|----------|------|
| `AgentSession` 接口 | `AgentAdapter`（adapter.go:129）：NewSession/SendPrompt/Cancel/Close/Done | 基本是改名 + 加 `ID()`/`Caps()`；`Prompt()`=改名 `SendPrompt` |
| 事件：TextDelta / ThinkingDelta | `ContentDelta`（adapter.go:83，含 IsThought） | 1:1，纯重命名 |
| 事件：ToolStart / ToolEnd | `ToolCallUpdateInfo`（adapter.go:92，含 IsNew/Status） | 1:1，纯重命名 |
| 事件：PermissionNeeded | `PermissionRequest`（adapter.go:60）+ `PermissionWire`（core/agent_perm.go） | 1:1；方案 §8 的"全 agent 共用一套编排"已实现 |
| §3.2 `Open` 工厂（按 agent 名路由） | `AgentManager.Open` + `adapterFactory`（manager.go:46,143） | 已有；缺"按 agent 名路由"这层 |
| §4.2 primary/fallback 传输 | `createAdapterWithFallback`（manager.go:207） | 已有 |
| Caps.MultiTurnPersistent + idle 回收 | ACP 保活 + `AgentManager` reaper（manager.go:342） | 已有 |
| 续问策略（Caps 驱动） | `SessionConfig.ResumeFrom` + `ErrNoConversation`（adapter.go:42） | 已有 |
| 每项目并发上限 | `projectSem`（manager.go:449） | 已有 |

### 1.2 方案里真正"新"的部分

减去映射表后，新工作只剩：

1. **claude-sdk-bridge**（Node 常驻服务 + 7 个 HTTP 接口 + 事件翻译）——唯一的技术未知数；
2. `internal/agent` flat 包 → `claude/`、`qoder/` 子包的目录重构（连带 `adapter_test.go`/`acp_test.go`/`print_test.go`/`manager_test.go` 全量搬迁）；
3. 配置从 `acp.use_acp` 迁移到 `agents.claude.transport`。

**P1–P5 的多数条目在映射表里已标"已有"**，方案应以 §1.1 为基准重估各阶段工作量。

### 1.3 可直接复用（正面）

- `AgentManager`：primary/fallback + 每项目并发信号量 + 透明回退 + idle reaper，方案 §9 的 `agent_manager` 语义已在（manager.go）。
- `PermissionWire`：30min 超时 / 先到先得 / Unwire 清理，方案 §8 与之对齐良好，应**包装**而非重写。
- `PrintAgent`：方案 §7 的 print 兜底已实现，P3 大部分是接线。
- `TaskStore`：task 状态持久化 + 重启恢复，续问的持久化基础已具备。

---

## 2. 分模块技术评估

### 2.1 §3.1 接口不完整

- `AgentSession` 没有 `RespondPermission`，但 §8 明确用 `session.RespondPermission(allow|deny)`。**接口必须补**：`RespondPermission(ctx, reqID string, allow bool, optionID string) error`（映射现有 `Approve`/`Deny`）。
- "事件（callback 或 channel）"二选一悬而未决。建议沿用现有 callback 模式（与 `AgentAdapter` 一致，`AgentManager.runMu` 的串行化已够用），每个事件带 `sessionID` + 轮次标识，避免多轮 + 重连下的关联歧义。
- 现有 `AgentAdapter` 的 `InjectToolResult`/`Done`（进程退出信号）在新接口里消失了。qoder ACP 路径工具结果由 agent 自驱，`InjectToolResult` 可去；但 `Done`（进程死亡通知）是 reaper/回退检测的信号，若 bridge 内部兜住子进程崩溃（§5.2），Go 侧可不要——需在接口里显式写清取舍。

### 2.2 §3.3 续问策略 — 优点，保留

`MultiTurnPersistent → 直接 Prompt / ResumeSupported → 重开 / 否则新会话`，不写 `if print` / `if acp`——全方案最干净的一段，与现有 `ResumeFrom` 语义天然对齐。

### 2.3 §4/§5 桥（sdk-bridge）— 唯一技术风险（已实证）

前提"claude-agent-acp 常退"已在上次会话验证，**不构成待验证项**。桥自身三个点**已由 P0 spike 验证通过**（详见 §8），结论：

- **U1 同进程多轮流式**：成立。单条长生命周期 input async generator 跨多轮，每轮恰好一条 `result` 消息（turn 边界），`claude.exe` PID 两轮间稳定。
- **U2 权限挂起与释放**：成立。`canUseTool` 返回挂起 Promise 真能挂住 turn（无 result 到达），审批 allow 后工具执行、turn 完成，无泄漏到下一轮。
- **U3 崩溃后 resume**：成立。kill 子进程后 SDK 抛明确 Error（不挂死），新 query `resume:session_id` 成功重建上下文。

进程链路：pieqi(Go) → bridge(Node) → CLI(子进程)，两层中介，日志/调试面翻倍（`comm-arch` 评估对 ACP 路径的同类提醒同样适用）。

### 2.4 §5.3 HTTP API — 双通道竞态 + SSE 无序号

- `POST /prompt` 与 `GET /events` 分离：Cancel 后立刻再 Prompt 的穿插、事件与哪一轮 prompt 对应，需明确关联规则（建议事件带 turn 标识）。
- SSE 断线重连丢中间事件，无序号/回放。本地 127.0.0.1 场景概率低，但桥内应有轻量 `lastEventSeq` 或 ring buffer，否则与 `comm-arch` 方案"Resume 依赖序号化事件"的方向相悖。

### 2.5 §5.4 进程管理 — 缺桥自身崩溃

只有 idle 回收，缺 bridge 进程 crash 后的处理：`auto_start` 重启 + 会话 resume，还是"桥崩 = 全部会话 closed + Error"由上层定夺。验收清单也应补这一条。

### 2.6 §8 权限 — 对齐良好，补接口

与现有 `PermissionWire`（超时/先到先得/Unwire 清理）一致，包装即可；`RespondPermission` 入接口（见 2.1）。

### 2.7 §9 配置 — 迁移未排期

现有 `config.go` 是 `acp.use_acp`（config.go:94）/ `acp.idle_timeout`（config.go:98），方案 §9 是 `agents.claude.transport`。旧字段弃用/迁移（含兼容告警）不在任何阶段里，应在 P5 显式列出。

### 2.8 §10 目录 — 重构量未量化

flat 包 → `claude/`、`qoder/` 子包：`adapter_test.go`/`acp_test.go`/`print_test.go`/`manager_test.go` 全量搬迁；`manager.go` 落在新目录树的哪层（§10 只画了顶层 `session.go`）没写清。

### 2.9 §11 分期 — 工作量高估

按 §1.1 映射表：P1（AgentSession+claude 接入）大半是重命名+接线；P2（权限/Cancel/idle）已有实现；P3（print fallback）已有 `PrintAgent`；P4（qoder 迁同接口）本质是重组。**建议把 §1.1 作为各阶段验收的前置对照**，避免把已做当新做。

### 2.10 §12 验收 — 具体可测，补两项

6 条 Claude 验收可以直接当测试用例写，P0 排第一正确。缺：桥自身进程崩溃的恢复/降级验收、SSE 断线重连丢事件的验收。

### 2.11 §13 风险 — Windows 与开发机矛盾

本机是 Windows（且 `bd5e340` 已记录 ACP spawn 在 Windows 上的坑），方案却"先保 Linux/macOS"。建议至少 P0 在 Windows 跑通 bridge 最小链路（Node 脚本 + spawn CLI 在 Windows 可行），信号/进程管理差异用单测覆盖，否则本地无法开发排障。

---

## 3. 关键风险与缺口汇总

| # | 风险/缺口 | 等级 | 位置 |
|---|-----------|------|------|
| R1 | 方案未与现有 `internal/agent` 做映射，P1–P5 工作量高估 | 高 | 全文/§11 |
| R2 | SDK 常驻多轮（U1 loop 形状）未实证 | ~~阻断~~ **已实证** | §5.2/§8 |
| R3 | 权限挂起释放（U2）未实证 | ~~高~~ **已实证** | §8 |
| R4 | 子进程崩溃 resume（U3）未实证 | ~~高~~ **已实证** | §5.2/§8 |
| R5 | `AgentSession` 缺 `RespondPermission`，§8 落不了地 | 高 | §3.1/§8 |
| R6 | 事件投递方式未定；`InjectToolResult`/`Done` 取舍未写清 | 中 | §3.1 |
| R7 | 桥自身 crash 重启/降级策略缺失 | 中 | §5.4 |
| R8 | SSE 断线丢事件，无序号/回放 | 中 | §5.3 |
| R9 | 配置迁移（`acp.use_acp` → `agents.claude.transport`）未排期 | 中 | §9 |
| R10 | 目录重构量（含测试搬迁）未量化 | 中 | §10 |
| R11 | Windows 开发路径与"先保 Linux/macOS"矛盾 | 中 | §13 |

---

## 4. 修订建议（按优先级）

### P0 — 进入实施前必须解决

1. **补映射表**：§1.1 入方案，按"已有/重命名/新写"重估各阶段工作量。
2. **P0 spike 验证桥**：U1（loop 形状）、U2（权限挂起释放）、U3（崩溃 resume）**已完成，全部 PASS**（详见 §8）。**注意：`claude-agent-acp` 的"常退"已在上次会话验证，不是本 spike 的重验项。**
3. **接口补全**：`AgentSession` 加 `RespondPermission`；定死事件投递为 callback；写明 `InjectToolResult`/`Done` 取舍。

### P1 — 协议冻结前补全

4. 事件带 `sessionID` + turn 标识；SSE 轻量序号（`lastEventSeq`/ring buffer）。
5. 补桥自身 crash 的重启/降级策略与对应验收。
6. `POST /prompt` 与 `GET /events` 的关联规则（Cancel 后立刻再 Prompt 的穿插）。

### P2 — 阶段计划调整

7. P5 加"配置迁移 + 旧字段弃用告警"。
8. 目录重构量（含测试搬迁）列进 P4；`manager.go` 落位写清。
9. §13 补 Windows 开发路径（P0 在 Windows 跑通最小链路）。

---

## 5. 推荐落地路径

在方案 §11 基础上调整（核心差异：先做映射表与 bridge spike，P1–P5 按映射表重估）：

```
Step 0  §1.1 映射表入方案 + P0 spike（U1/U2/U3）   ← 唯一技术闸门
Step 1  接口补全（RespondPermission/事件投递/Done 取舍）
Step 2  claude-sdk-bridge 常驻 + SSE + 权限挂起（P0 通过后）
Step 3  现有抽象改名重组：AgentAdapter→AgentSession 对齐 + bridge 事件翻译接线
Step 4  复用现有 PermissionWire / PrintAgent / reaper / ResumeFrom，逐项验收（§12）
Step 5  qoder 迁到同接口 + 目录重构 + 配置迁移（agents.claude/qoder）
```

> 关键差异：真正的新工作只有 bridge（Step 2）与重组接线（Step 3）；其余阶段按 §1.1 大多是"已有实现 + 验收"，不再按 §11 的原始估时推进。

---

## 6. 与现有抽象的复用边界（避免重写）

| 现有组件 | 方案对应 | 复用/改造 |
|----------|----------|-----------|
| `AgentAdapter`（adapter.go） | `AgentSession` | **改名 + 补 `ID()`/`Caps()`/`RespondPermission`**，不重写 |
| `ContentDelta`/`ToolCallUpdateInfo`/`PermissionRequest` | 中性事件 | **纯重命名** |
| `AgentManager`（manager.go） | §3.2 工厂 + §9 agent_manager | **直接复用**，加"按 agent 名路由" |
| `PermissionWire`（agent_perm.go） | §8 权限编排 | **包装**，保留超时/先到先得语义 |
| `PrintAgent`（print.go） | §7 print 兜底 | **直接复用**，接入 bridge 客户端 |
| `SessionConfig.ResumeFrom` | §3.3 续问 | **直接复用** |
| `acp.use_acp` 配置 | `agents.claude.transport` | **迁移**，含兼容告警 |
| — | claude-sdk-bridge | **新写**（唯一新增组件） |

---

## 7. 最终判断

方案在**抽象与战略上正确**，且与现有 `AgentAdapter`/`AgentManager`/`PermissionWire` 的哲学一脉相承——这是它能落地的基础。§3.3 的 Caps 续问策略与 §12 的验收清单尤其值得保留。

但**当前版本不能直接进入实施**，原因有二：

1. **方案没提现有代码**：`internal/agent` 已实现 §3/§4/§8 的大半，P1–P5 大量是重命名与接线。不补 §1.1 映射表，阶段估时会失真、会重复劳动。
2. **唯一技术风险是 bridge 本身**：U1/U2/U3 三个点（SDK 常驻多轮、权限挂起释放、崩溃 resume）未实证，P0 spike 通过前，桥之外的重组工作没有意义。

补齐 P0（映射表 + spike + 接口补全）后，方案的阶段计划可按 §5 修订路径推进。**建议先把 §1.1 映射表与 §12 的 U1/U2/U3 验收项写进方案，再做 P0 spike。**

---

## 8. P0 spike 实测结论（2026-08-20）

> 验证载体：`docs/spikes/claude-sdk-bridge/`（spike 存档，`spike/u1|u2|u3_*.mjs`），SDK `@anthropic-ai/claude-agent-sdk@0.3.237`，claude CLI 2.1.229（Windows native `claude.exe`），走本地代理 `127.0.0.1:15721`。
> 三个 spike 均 **PASS**。

### 8.1 U1 — 同进程多轮流式 + turn 边界 ✅

| 验证点 | 结果 |
|--------|------|
| 单条长生命周期 input async generator 跨多轮 | ✅ 成立，一轮一 `result` 消息 |
| turn 边界 | ✅ 每轮恰好一条 `result`（subtype=success），带 `session_id` |
| CLI 子进程保活 | ✅ `claude.exe` PID 两轮间稳定（变化的叶子只是临时 `cmd.exe`） |
| 上下文跨轮保留 | ✅ turn2 答出秘密词 banana |
| 增量文本 | ✅ 需 `includePartialMessages: true`，否则只有整块 `assistant` 消息 |

### 8.2 U2 — 权限挂起与释放 ✅

| 验证点 | 结果 |
|--------|------|
| `canUseTool` 挂起 turn | ✅ 返回挂起 Promise 时进程存活、3s 内无 result 到达 |
| 审批 allow 后工具执行 | ✅ resolve `{behavior:'allow'}` → 工具执行（SPIKE_OK）→ turn 完成 |
| 无泄漏到下一轮 | ✅ 下一轮纯文本不再触发 canUseTool，pending 归零 |
| 强制走 ask 路径 | ✅ 需 `settings.permissions.ask` 显式声明（否则本机 native 默认直接放行 shell） |

### 8.3 U3 — 崩溃后 resume ✅

| 验证点 | 结果 |
|--------|------|
| kill 子进程后原流行为 | ✅ 干净报错 `"Claude Code process exited with code 1"`，**不挂死** |
| resume 重建上下文 | ✅ 新 query `resume:session_id` 答出秘密词 banana |
| 兜底语义 | ✅ 即使进程保活失效，凭 session_id 冷启动 resume 也成立（print 同源） |

### 8.4 对 bridge 构建的关键约束（spike 副产品）

1. **`includePartialMessages: true` 是桥的必需选项**——没有它 TextDelta 不会发出，只有整块正文。
2. **正文有两条通道**：`stream_event.content_block_delta`（增量）+ `assistant` 消息（完整块）。两者**内容重叠**，桥必须去重/分工：增量只走 delta，完整正文以 assistant 消息为准（不能简单拼接，会重复）。
3. **`result` 消息即 TurnEnd**：带 `subtype`（success / error_during_execution / ...）、`is_error`、`session_id`、`usage`、`total_cost_usd`（跨轮累计）。
4. **崩溃是 throw 而非 error result**：SDK 检测到子进程退出会抛出 `Claude Code process exited with code N`，桥需 catch 并映射为 `Error` 事件。
5. **Windows native 工具名是 `PowerShell` 不是 `Bash`**（模型实测声明"本环境只有 PowerShell"）；工具名按平台不同，桥的审批展示层需按实际 `toolName` 渲染。
6. **权限决策不一定到 canUseTool**：默认 `permissionMode: 'default'` 下本机 shell 工具被直接放行；要强制走审批必须显式 `settings.permissions.ask`。桥应在 session 打开时按产品语义注入该覆盖。
7. **`resume: session_id` 稳定可用**，可作为保活失效后的兜底（U3 已验证）。
8. **Query 控制面**：`interrupt()`/`close()`/`streamInput()` 仅在 streaming-input 模式可用；`close()` 会终止子进程（关会话用），Cancel 本轮用 `interrupt()`。

### 8.5 风险表状态更新

- R2 / R3 / R4（§3）：**已实证，风险关闭**，升级为 §8.4 的桥构建约束。
- 遗留风险不变：R1（映射表）、R5-R11（接口/SSE/配置/目录/Windows 等）——均不依赖 spike 结果。

---

## 9. 实施状态（2026-08-20 落地点）

按 §5 修订路径推进，当前进度与自测证据：

| 步骤 | 内容 | 状态 | 自测证据 |
|------|------|------|----------|
| Step 1 | `AgentSession` 接口 + `RespondPermission` 补全 + `sessionAdapter` 桥接 | ✅ 完成 | `go test ./...` 全绿；`internal/agent/session_test.go` |
| Step 2 | `claude-sdk-bridge` 常驻服务（HTTP/SSE + 多轮 + 权限 + cancel/close/resume） | ✅ 完成 | `services/claude-sdk-bridge/`（ESM JS，免构建） |
| Step 3 | bridge 集成自测 | ✅ 完成 | `node test/bridge_self_test.mjs` → **17/17** |
| Step 4 | Go 侧 bridge 客户端 + 工厂接入 + print 回退 | ✅ 完成 | `internal/agent/claude/`；httptest 单测全绿 |
| Step 4b | 真实端到端（Go AgentSession ↔ bridge ↔ claude） | ✅ 完成 | `go test -tags integration ./internal/agent/claude/ -run RealIntegration` → **PASS** |
| Step 5 | 全量自测 + 回写文档 | ✅ 完成 | `go test ./...`（-p 4）全绿 |

### 落地点产物

- `internal/agent/session.go` — 中性 `AgentSession` / `Caps` / `Event` / `Open` 工厂 / `sessionAdapter` 桥接
- `internal/agent/claude/bridge/client.go` — bridge HTTP/SSE 客户端
- `internal/agent/claude/session.go` — bridge 传输的 `AgentSession` 实现（clientRef 关联 + turn_end 表）
- `internal/agent/claude/factory.go` — 配置 + `agent.Open("claude")` provider + print 回退
- `services/claude-sdk-bridge/src/{index,session}.js` — 常驻桥（HTTP/SSE + 事件历史 ring + idle 回收）
- `services/claude-sdk-bridge/test/bridge_self_test.mjs` — 17 项集成自测
- `internal/agent/claude/integration_test.go` — 真实端到端（build tag `integration`）
- `internal/agent/provider.go` — ACP 系 agent 工厂：`agent.Open("qoder")` 复用 `ACPAgent` + `sessionAdapter` 桥接（P4 工厂层）

### 与方案的偏差（已决策）

1. **bridge 用 ESM JS（免构建）**：替代方案的 TS→dist，`start_cmd` 直接 `node src/index.js`；后续要 TS 可平移。
2. **`/prompt` 增 `clientRef` 字段，turn_end 回带**：解决 cancel 后立刻再 prompt 的 turn 关联竞态（方案 §5.3 未覆盖）。
3. **强制审批走 `settings.permissions.ask`**（spike §8.4 第 6 条）；host 全权经 canUseTool 决定放行/上卡。
4. **`ask: ["*"]`** 实测可用（自测 permission_needed 正常触发）；若未来 SDK 不认通配，改显式清单（session.js 有注释）。

### 剩余工作（未做，明确列出）

- **P5**：~~配置迁移~~（已完成，见 §9.3）；TaskRunner 已切到 agent.Open（见 §9.2）
- 桥自身崩溃的重启策略（方案 §12 验收补项）——目前桥崩 = 各会话 error 事件 + Go 侧 fatal，可降级到 print
- 桥 auto-start 的孤儿残留：Windows 硬杀 pieqi 时桥进程无 PID 文件接管（与 cloudflared 同类问题，可用 PID 文件方案兜底）

---

## 9.2 TaskRunner 迁移到 agent.Open（2026-08-26 追加）

将任务执行路径切到多 Agent 默认驱动（用户决策：sdk-bridge 直接成为默认驱动，不受 use_acp 门控）：

| 项 | 内容 | 状态 | 自测证据 |
|----|------|------|----------|
| `sessionBackedAdapter` | AgentSession → AgentAdapter 桥接（复用 AgentManager/wire/reaper 零改动）：NewSession=agent.Open、SendPrompt=Prompt（阻塞到 turn_end）、Approve/Deny/RespondPermission=RespondPermission、Done=会话终止、RealSessionID=SDK resume id、事件翻译（delta/tool/perm，权限带合成 allow 选项） | ✅ | `sessionadapter_test.go`（delegation/translation/done/resume） |
| `NewAgentSessionManager` | 以 agent.Open(name) 为 primary 的 AgentManager（fallback=nil，agent.Open 内部桥→print 回退） | ✅ | `TestNewAgentSessionManager` |
| claude session 扩展 | `ResumeID()` 方法 + `fail()` 派发 EventError（adapter 据此关 Done） | ✅ | 单元 + 集成 |
| TaskRunner 轮末续问句柄 | `refreshResumeID`：桥 turn_end 后才有 SDK resume id，每轮成功回写 ACPSessionID（Open 时拿不到） | ✅ | 集成测试断言 adapter.ResumeID()==持久化值 |
| main 路由 | transport=sdk-bridge（默认）→ session "claude"；transport=print+qoder → "qoder"；否则回退旧 use_acp AgentManager；Phase 1 默认路径不动 | ✅ | `go build` + 启动冒烟 |
| 全链路集成 | TaskRunner.runACP → 会话 manager → adapter → agent.Open("claude") → 真实桥 → 真实 claude：多轮流式 + 冷续问重建上下文 + 权限审批闭环 | ✅ | `TestRunACPSessionManagerIntegration`（**4/4 稳定通过**） |

### 接线要点

1. **桥→print 回退由 agent.Open 内部完成**（session manager 不再做 fallback 层），transport=print 时直走 print。
2. **权限**：adapter 给 PermissionRequest 补合成 `allow_once` 选项（桥的 RespondPermission 只认 allow/deny，无 reject 选项概念）；approve → Approve（allow=true），deny → 无 reject 选项落到 Deny（allow=false）。
3. **冷续问**：`refreshResumeID` 在每轮成功后把 SDK resume id 回写 ACPSessionID（Open 时 RealSessionID 拿到的还是桥会话 id，不可续问）——这是本阶段最隐蔽的接线点，集成测试专门断言了它。
4. **门控**（用户决策）：`agents.claude.transport=sdk-bridge`（默认）即走 session 路径，`use_acp` 仅兜底；Phase 1 `claude -p` 路径在 transport=print + use_acp=false 时保持。

### 调试中发现并修复

- **`startSessionManagerBridge` 用 defer 杀桥**：helper 一返回桥就死了，导致所有轮静默走 print 回退（正确行为、错误测试基建）→ 改 t.Cleanup。
- **测试早读**：续问时任务首轮已是 completed，`waitTaskStatus(completed)` 立即返回 → 改 `waitTaskContains`（等输出落地）+ `waitTaskDoneAutoApprove`（桥 ask:["*"] 对模型偶发工具调用触发审批时自动放行）。
- **`TestFactoryTransportPrintSkipsBridge` 未复位 Transport**：包级 defaultConfig.Transport 被遗留为 "print"，污染同包后续测试 → defer 一并复位。

---

## 9.1 生产接线（2026-08-25 追加）

将 §9 的"库"级实现接入生产（`acp.use_acp` 旧路径不动，新增 `agents.*` 配置节并行存在）：

| 项 | 内容 | 状态 | 自测证据 |
|----|------|------|----------|
| 配置节 | `agents.claude{transport,bridge{base_url,token,auto_start,dir},print}` + `agents.qoder{transport,acp}`，含默认值 | ✅ | `config_test.go`（defaults + override） |
| 桥鉴权 | bridge 读 `BRIDGE_TOKEN`，/v1 业务路由校验 Bearer（health 保持开放）；Go client 带 token | ✅ | bridge 自测 **21/21**（新增 3 条 401/放行）；`client_test.go` |
| auto-start | `claude.Proc`：探活 → 不可达自动 spawn `node src/index.js` → 等健康 → Stop 关停；桥目录自动探测（config/env/exe/cwd 邻接） | ✅ | `proc_test.go` + 集成 `TestProcAutoStartIntegration`（真实 spawn/health/stop 全链路 PASS） |
| main 接线 | `claude.Configure(ConfigFromAgents)` + `ConfigureACPProviders` + `EnsureRunning` + 信号关停 `Stop` | ✅ | `go build ./...` + **真实启动冒烟**：日志 `claude sdk-bridge auto-started` / `ensured`，`curl /v1/health` = `{"ok":true}` |
| 全量回归 | | ✅ | `go test ./... -count=1 -p 4` 全绿 |

### 接线后新增落地点

- `internal/config/config.go` — `AgentsConfig`/`AgentClaudeConfig`/`ClaudeBridgeConfig`/`AgentPrintConfig`/`AgentQoderConfig` + `QoderACPConfig.ACPConfig()` 转换
- `internal/agent/claude/proc.go` + `proc_test.go` — 桥进程管理器（§5.4 auto_start）
- `internal/agent/claude/factory.go` — `Config` 增 `Transport`/`Token`；`ConfigFromAgents`；`openSession` 按 transport 路由
- `internal/agent/provider.go` — `ACPProviderConfigFromAgents`
- `internal/agent/claude/bridge/client.go` — `NewClientWithToken` + 请求附 Bearer
- `services/claude-sdk-bridge/src/index.js` — `BRIDGE_TOKEN` 鉴权（health 开放）
- `cmd/pieqi/main.go` — agents 配置接线 + auto-start + 关停
- `config.yaml` — 新增 `agents:` 示例节（与 `pieqi.acp.*` 并存，迁移留 P5）

### 设计取舍

1. **transport=print 时 openSession 直走 print**，不碰桥（`factory.go`）。
2. **health 不鉴权**：供探活/外部诊断，不泄漏会话数据（业务路由全部要 token）。
3. **auto-start 失败不阻塞启动**：仅 warn，会话打开时按 openSession 逻辑回退 print。
4. **硬杀 pieqi 会留孤儿桥**（Windows 无信号传播）：与 cloudflared 同类的 PID 文件方案列为剩余工作。

---

## 10. 一句话结论

**抽象层设计是对的，而且现有代码已经有一大半；桥的三大技术前提（U1/U2/U3）已全部实证通过，bridge 本体与 Go 侧接入已落地并自测通过（单元 + 集成 + 真实端到端三层）。剩余是 P4/P5 的迁移与进程管理，属增量推进，不再是技术风险。**
