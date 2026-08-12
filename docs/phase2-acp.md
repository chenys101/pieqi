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

---

## 5. 迁移路径（建议里程碑）

- **M1 · ACP 会话内核**：桥接层实现 `ACPAgent`（stdin/stdout JSON-RPC、握手、`NewSession`/`SendPrompt`/收增量）。先用 claude-code 验证 ACP 端到端出文本。
- **M2 · 流式落地**：内容增量 → WS → 前端逐字追加渲染（替换 Phase 1 整块 text 事件）。
- **M3 · 权限审批**：`PermissionRequest` → IM/PWA 审批 → Approve/Deny。替换 hook 链路。
- **M4 · 多 Agent 抽象**：定义 `AgentAdapter` 接口，`ACPAgent` + `PrintAgent` 双实现；`AgentManager` 统一调度。
- **M5 · 会话/续问**：Session 资源持久化，替代 `--session-id`/`--resume` 脆弱链路。
- **M6 · 落地第二个 agent**：接 qodercli 等，验证统一接口。

## 6. 需要核实的点（下一站调研）

> [待调研结果填充] Claude Code 暴露 ACP 的确切命令/子命令与版本；qodercli 等哪些 agent 已支持 ACP 及成熟度；ACP 规范版本号与流式/权限资源的确切字段；回退策略边界。

## 7. 风险与回退

- **ACP 生态成熟度**：若某 agent 未支持 ACP，用 `PrintAgent` 回退，接口不破。
- **状态与复杂度**：ACP 会话有更多状态，需严谨的进程/消息生命周期管理（对应 `liveProc`）。
- **协议演进**：ACP 规范仍在演进，能力协商 + 版本检测可兼容。
- **IM 审批往返延迟**：审批是异步的人机交互，需超时/过期策略（可参考 Phase 1 hook_timeout）。

---

## 附：待办（进入第二阶段后）

- [ ] 调研并确认 ACP 细节（见 §6）
- [ ] 建 `internal/agent/` 目录：`adapter.go` / `acp.go` / `print.go` / `manager.go`
- [ ] 可行性 spike：claude-code ACP 最小端到端
- [ ] 更新 README / 架构文档 / CONTEXT 词汇
