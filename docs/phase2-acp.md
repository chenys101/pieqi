# Phase 2.0 规划 — 从 `claude -p` 迁移到 ACP，支持多 Agent 与协议级权限审批

> 状态：草稿（feature2.0 分支）
> 目标：突破第一阶段（Phase 1）的限制——真流式、协议级权限审批、多 Agent（claude code / qodercli 等）统一驱动。

---

## 1. 背景：第一阶段（Phase 1）的限制

Phase 1 通过 `os/exec` 驱动 `claude -p`（print 模式）子进程，用 `--output-format stream-json` 解析事件、`--permission-mode bypassPermissions` + PreToolUse hook 做审批。实测暴露出几个结构性限制：

| # | 限制 | 表现 |
|---|------|------|
| L1 | **非真流式** | claude 把回答作为一个完整 text 块输出，界面"思考可见 + 回答整段出现"，不是逐字 |
| L2 | **审批是"hack"** | 用 `bypassPermissions` + 注入 `.claude/settings.json` 的 PreToolUse hook 回调 `/internal/hook`；审批粒度/可视化受 CLI 限制，非协议级 |
| L3 | **绑死单一 CLI** | 每个任务 spawn 一个 `claude -p`，参数/解析格式是 claude 私有的，难复用 qodercli 等其它 agent |
| L4 | **会话/续问脆弱** | 靠 `--session-id`/`--resume` 匹配会话；代理差异下 id 对不上，曾出现 "No conversation found" |
| L5 | **代理吞事件** | 部分代理（如 127.0.0.1:15721）在 `-p` 下整段缓冲甚至吞掉中间事件 → 任务"无响应" |

这些根因都是：**`-p` 是"一次跑完打印"的模式，不是为"客户端驱动交互式 agent"设计的**。

---

## 2. 方案方向：Agent Client Protocol（ACP）

**ACP** 是一个开放的、全双工的客户端↔agent 协议（Zed + Anthropic 发起），目标正是"让任意客户端以统一方式驱动任意 coding agent"。它把 agent 交互建模为协议资源而非 CLI 私有参数。

### 核心模型

- **传输**：默认 JSON-RPC over stdio（客户端 spawn agent 进程）；可扩展其它传输。
- **资源（resources）**：`Session`、`Message`、`PermissionRequest`、`ToolUse` 等，是客户端可操作的一等对象。
- **全双工消息**：agent→client 推内容增量、tool_use、权限请求、通知；client→agent 发 prompt、批准/拒绝工具、注入 tool_result、编辑。
- **能力协商（capabilities handshake）**：连接建立时双方声明能力（流式、工具执行、权限控制等），客户端按能力降级。
- **真流式**：文本以增量（content delta）到达，天然支持逐字渲染。
- **权限审批**：agent 执行敏感工具前发 `PermissionRequest`，客户端（桥接层）批准/拒绝——协议级的一等公民，非 hook 补丁。

### 为什么对第二阶段是正确方向

- **多 Agent**：ACP 是"客户端驱动"协议，一个桥接层可 spawn 多个 ACP 会话，分别跑不同 agent（claude code / qodercli …），只要它们实现 ACP，接口统一。
- **协议级审批**：`PermissionRequest` 天然对接 IM 审批（在聊天里"批准/拒绝"某个工具调用），取代 L2 的 hook hack。
- **真流式**：内容增量逐字推送，解决 L1。
- **会话一等公民**：Session 是协议资源，L4 的续问/会话脆弱由协议承载。
- **解耦代理问题**：不再依赖 `-p` 模式下代理是否吐事件；流式由 ACP 语义保证。

---

## 3. 对比：`claude -p` vs ACP

| 维度 | `claude -p`（Phase 1） | ACP（Phase 2） |
|------|------------------------|----------------|
| 交互模型 | 一次跑完打印 | 全双工会话 |
| 流式 | 事件级（整块 text） | 内容增量（逐字） |
| 权限审批 | `bypassPermissions` + hook 回调 | 协议级 `PermissionRequest` |
| 工具控制 | 解析 stream-json tool_use | ToolUse 资源，客户端可审/批 |
| 多 Agent | 每个 `-p` 绑 claude | 统一协议驱动多种 agent |
| 会话续问 | `--session-id`/`--resume` 匹配 | Session 资源 |
| 能力协商 | 无 | capabilities handshake |
| 复杂度 | 低（但脆弱） | 中高（但健壮） |
| 生态现状 | 成熟 | 新但正在成为标准 |

---

## 4. 目标架构

```
IM（飞书/企微/微信）─┐
                    ├─→ 桥接层（Go）
PWA 监控            ─┘       │
                       ┌─────┴─────┐
                       │ AgentManager │  ← 统一调度、多会话、并发
                       └─────┬─────┘
             ┌───────────────┼───────────────┐
             ▼               ▼               ▼
      [ACP Client]    [ACP Client]     [ACP Client]   ← 每个 = 一个 agent 进程会话
             │               │               │
             ▼               ▼               ▼
       claude-code      qodercli        其他 ACP agent
```

### 新增抽象层

- **`AgentAdapter`（接口）**：对桥接层暴露统一能力——`NewSession()`、`SendPrompt()`、`OnContentDelta()`、`OnPermissionRequest()`、`Approve/Deny()`、`InjectToolResult()`、`Cancel()`。
- **`ACPAgent`（实现）**：走 ACP（JSON-RPC over stdio），负责 spawn agent、握手、资源管理、增量推送。
- **`PrintAgent`（回退实现）**：保留 `claude -p` 作为不支持 ACP 的 agent 的回退适配器（包一层，接口对齐）。
- **`AgentManager`**：跨 agent 的统一调度（对应现 `TaskRunner`），管理多个 ACP 会话 + 并发上限 + 会话持久化。

### 权限审批链路（ACP 版）

```
agent 要执行 Bash/Write
  → ACP PermissionRequest(工具, 参数, 摘要)
  → 桥接层 → IM/推送 PWA「需审批：Bash: rm -rf x」[批准][拒绝]
  → 用户回复 → 桥接层 ACP Approve/Deny
  → agent 才执行 / 拒绝后改走它路
```

取代 Phase 1 的 `bypassPermissions` + PreToolUse hook 注入。

### Claude Code 接入路径（方案 A：官方 TS 适配器）

```
Go 桥接层（acp-go-sdk client）
   ↓ exec: npx -y @agentclientprotocol/claude-agent-acp@latest
TS 适配器（Node.js 进程）
   ↓ 内部调用
Claude Agent SDK → Claude 能力
```

- `ACPAgent` spawn 的 command 是 `npx ... claude-agent-acp`，**不是** `claude`。
- 通信走 ACP JSON-RPC over stdio，由 acp-go-sdk 封装。
- `PrintAgent`（回退到 `claude -p`）仅在适配器不可用时启用。

### 其他 agent 接入路径（原生 ACP）

Qoder / Codex / Reasonix / Grok / Omp 直接 spawn 各自 `--acp` 进程，走同一套 acp-go-sdk client，无需适配器。

---

## 5. 迁移路径（建议里程碑）

- **M1 · ACP 会话内核**：引入 `github.com/coder/acp-go-sdk`，实现 `Client` 接口（`SessionUpdate`/`RequestPermission` 等）。参考 `example/claude-code` 跑通 Claude Code 端到端出文本（spawn TS 适配器）。
- **M2 · 流式落地**：内容增量 → WS → 前端逐字追加渲染（替换 Phase 1 整块 text 事件）。
- **M3 · 权限审批**：`PermissionRequest` → IM/PWA 审批 → Approve/Deny。替换 hook 链路。
- **M4 · 多 Agent 抽象**：定义 `AgentAdapter` 接口，`ACPAgent` + `PrintAgent` 双实现；`AgentManager` 统一调度。
- **M5 · 会话/续问**：Session 资源持久化，替代 `--session-id`/`--resume` 脆弱链路。
- **M6 · 落地第二个 agent**：接 qodercli 等，验证统一接口。

## 6. 调研结论（已核实）

> 以下结论来自对 `acp-go-sdk`（`github.com/coder/acp-go-sdk` v0.13.5）、官方 TS 适配器及各 agent 官方文档的核实。

### 6.1 Claude Code 接入 ACP 的方式

- **不原生支持 ACP**，需经官方 TS 适配器 `@agentclientprotocol/claude-agent-acp`（原 `@zed-industries/claude-code-acp`）桥接。
- **spawn 目标**：`npx -y @agentclientprotocol/claude-agent-acp@latest`（参见 acp-go-sdk `example/claude-code/main.go`）。
- **链路**：Go 桥接层（acp-go-sdk client）→ stdin/stdout ACP JSON-RPC → TS 适配器 → Claude Agent SDK → Claude 能力。
- **runtime 代价**：引入 Node.js。但 Phase 1 的 `claude` CLI 本身即 npm 包，Node.js 已是既有依赖，非新增；额外代价是进程链路多一层，调试链路变长。
- **计费**：走 Agent SDK 额度（自 2026-06-15 起与交互式对话额度分离），需单独评估。

### 6.2 其他主流 agent 的 ACP 支持

经各 agent 官方文档核实，以下 agent **原生支持 ACP**，无需适配器：

| Agent | 接入方式 |
|-------|---------|
| Qoder | `qodercli --acp` |
| Codex | 原生 ACP |
| Reasonix / Grok / Omp | 原生 ACP |

> 主流 coding agent 中，仅 Claude Code 与 Gemini CLI 非原生 ACP。Gemini CLI 为单向 JSONL，定位偏只读分析。

### 6.3 协议字段（ACP v1）

- **协议版本**：`ProtocolVersionNumber = 1`
- **流式增量**：`SessionUpdate` 回调 → `AgentMessageChunk.Content.Text`（逐字）/ `AgentThoughtChunk`（思考过程）
- **权限审批**：`RequestPermission` 回调 → `Options[]`（`AllowOnce` / `AllowAlways` / `Deny`）→ 返回 `Selected{OptionId}` 或 `Cancelled`
- **会话恢复**：`session/load` / `session/resume` / `session/list`（实验性）/ `session/fork`
- **工具调用**：`ToolCall`（开始）/ `ToolCallUpdate`（状态变更）

### 6.4 Go SDK 选型

采用 `github.com/coder/acp-go-sdk`：
- 封装全部 ACP 协议细节（NDJSON 帧解析、请求/响应关联、通知分发）。
- 提供 `Client` 接口（`SessionUpdate` / `RequestPermission` / `ReadTextFile` / `WriteTextFile` / Terminal 系列）。
- 自带 `example/claude-code`、`example/gemini` 桥接示例，可照抄。

### 6.5 决策

- **Claude Code 接入路径**：采用官方 TS 适配器（方案 A）。理由：协议稳定、能力完整、有官方示例可照抄；Node.js 为既有依赖。
- **回退策略边界**：若 TS 适配器进程崩溃 / 未安装，回退到 Phase 1 的 `claude -p` + stream-json 解析（`PrintAgent`）。

## 7. 风险与回退

- **ACP 生态成熟度**：主流 agent（Qoder/Codex/Reasonix/Grok/Omp）已原生支持 ACP；Claude Code 经官方 TS 适配器接入。若适配器不可用，用 `PrintAgent` 回退到 `claude -p`，接口不破。
- **Node.js runtime 依赖**：Claude Code 路径依赖 Node.js（TS 适配器）。Phase 1 的 `claude` CLI 本身即 npm 包，Node.js 为既有依赖；额外代价是进程链路多一层（Go → TS 适配器 → Agent SDK），调试链路变长。
- **状态与复杂度**：ACP 会话有更多状态，需严谨的进程/消息生命周期管理（对应 `liveProc`）。
- **协议演进**：ACP 规范仍在演进，能力协商 + 版本检测可兼容。
- **IM 审批往返延迟**：审批是异步的人机交互，需超时/过期策略（可参考 Phase 1 hook_timeout）。

---

## 附：待办（进入第二阶段后）

- [x] 调研并确认 ACP 细节（见 §6，已核实）
- [ ] 建 `internal/agent/` 目录：`adapter.go` / `acp.go` / `print.go` / `manager.go`
- [ ] 可行性 spike：claude-code ACP 最小端到端
- [ ] 更新 README / 架构文档 / CONTEXT 词汇
