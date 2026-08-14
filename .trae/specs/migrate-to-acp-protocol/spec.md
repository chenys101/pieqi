# Phase 2：从 `claude -p` 迁移到 ACP 协议 Spec

> 来源：`docs/phase2-acp.md`（feature2.0 分支规划）
> 状态：草稿

## Why
Phase 1 通过 `os/exec` 驱动 `claude -p` 子进程（`--output-format stream-json` + `bypassPermissions` + PreToolUse hook）让 IM/PWA 驱动 Claude Code。该方案有结构性限制：非真流式（整块 text）、审批靠 hook hack、绑死单一 CLI、会话续问脆弱（`--session-id`/`--resume` 对不上 → "No conversation found"）、部分代理在 `-p` 下吞事件。迁移到开放全双工的 Agent Client Protocol（ACP）可一次性解决上述问题，并支持多 Agent 统一驱动。

## What Changes
- 新增 `AgentAdapter` 接口抽象 agent 交互（`NewSession`/`SendPrompt`/`OnContentDelta`/`OnPermissionRequest`/`Approve`/`Deny`/`InjectToolResult`/`Cancel`），提供 `ACPAgent`（JSON-RPC over stdio）与 `PrintAgent`（claude -p 回退）双实现。
- 引入 `github.com/coder/acp-go-sdk`（v0.13.5），Claude Code 经官方 TS 适配器 `npx -y @agentclientprotocol/claude-agent-acp@latest` 接入；其他 agent（Qoder/Codex 等）走原生 `--acp`。
- 新增 `AgentManager` 统一调度多会话/并发，承担现 `TaskRunner` 的 agent 驱动职责（worktree/事件/状态/IM 通知逻辑保留）。
- 真流式：ACP `SessionUpdate`/`AgentMessageChunk.Content.Text` 内容增量 → EventBus → WS → 前端逐字追加，替换 Phase 1 整块 `EventText`。
- 协议级审批：ACP `RequestPermission` → task `waiting_input(approval)` → IM/PWA 批准/拒绝 → ACP Approve/Deny（AllowOnce/AllowAlways/Deny），替换 ACP 路径上的 `bypassPermissions`+hook hack。
- 会话持久化：ACP `session/load`/`session/resume` 替换脆弱的 `--session-id`/`--resume` 匹配。
- 配置新增 agent 选择与 ACP spawn 参数。
- **BREAKING**（仅 ACP 路径）：`--session-id`/`--resume` CLI 匹配与 PreToolUse hook 注入在 ACP 路径停用；`PrintAgent` 回退路径保留两者，接口对齐。

## Impact
- 受影响代码：
  - `internal/core/task_runner.go`（`run`/`runClaude`/`parseStream`/`handleLine` 的 agent 驱动部分拆分到 `AgentAdapter`；调度/状态/通知保留）
  - `internal/core/hook.go`、`hook_settings.go`（ACP 路径停用，PrintAgent 回退保留）
  - `internal/core/stream_event.go`（ACP 路径不再用；PrintAgent 保留）
  - `internal/core/session_runner.go`、`approval.go`（旧 IM 路径，迁移期由 `legacy_im_path` 控制保留）
  - `internal/core/event_bus.go`、`internal/api/ws.go`（承接增量流式事件）
  - `internal/model/task.go`（`TaskEvent` 增量语义；`Decision` 复用 approval kind）
  - `internal/config/config.go`（新增 ACP/agent 配置块）
  - 新增 `internal/agent/` 目录：`adapter.go`/`acp.go`/`print.go`/`manager.go`
- 受影响能力：流式渲染、权限审批、会话续问、多 Agent 支持。
- 依赖新增：`github.com/coder/acp-go-sdk`；Node.js runtime（TS 适配器；Phase 1 的 claude CLI 已是 npm 包，Node.js 为既有依赖）。
- 计费：Claude Code 走 Agent SDK 额度（自 2026-06-15 起与交互式对话额度分离），需单独评估。

## ADDED Requirements

### Requirement: AgentAdapter 统一抽象
系统 SHALL 定义 `AgentAdapter` 接口，对桥接层暴露统一能力：`NewSession()`、`SendPrompt()`、`OnContentDelta()`、`OnPermissionRequest()`、`Approve/Deny()`、`InjectToolResult()`、`Cancel()`。`ACPAgent` 与 `PrintAgent` 双实现接口对齐。

#### Scenario: 接口对齐
- **WHEN** 桥接层通过 `AgentManager` 启动一个 agent 会话
- **THEN** 无论底层是 ACP 还是 claude -p，调用同一 `AgentAdapter` 接口方法
- **AND** `PrintAgent` 在 ACP 适配器不可用时被透明启用，调用方无感

### Requirement: ACP 会话内核（ACPAgent）
系统 SHALL 使用 `github.com/coder/acp-go-sdk` 的 `Client` 接口 spawn 并驱动 ACP agent 进程：Claude Code 经 `npx -y @agentclientprotocol/claude-agent-acp@latest`，其他 agent 经各自 `--acp` 进程；建立时完成 capabilities handshake（ProtocolVersionNumber=1），通过 JSON-RPC over stdio 管理会话生命周期。

#### Scenario: Claude Code 端到端出文本
- **WHEN** 创建一个 Claude Code ACP 任务并 SendPrompt
- **THEN** spawn TS 适配器进程，握手成功
- **AND** agent 回复文本经 `SessionUpdate` 回调到达桥接层
- **AND** 进程退出/异常被 `AgentManager` 捕获并清理

#### Scenario: 适配器不可用回退
- **WHEN** TS 适配器未安装或进程崩溃
- **THEN** 回退到 `PrintAgent`（claude -p + stream-json），接口不破
- **AND** 回退事件被记录以便定位

### Requirement: 真流式内容增量
系统 SHALL 把 ACP `SessionUpdate` 的 `AgentMessageChunk.Content.Text` 增量逐字转发：AgentManager → EventBus → WS → 前端增量追加渲染。`AgentThoughtChunk`（思考过程）同样增量推送。替换 Phase 1 在 `appendEvent` 中 200ms 合并的整块 `EventText`。

#### Scenario: 逐字渲染
- **WHEN** agent 产出一段文本
- **THEN** PWA 详情视图逐增量追加，而非整段一次性出现
- **AND** thinking 块以独立增量流呈现

### Requirement: 协议级权限审批
系统 SHALL 把 ACP `RequestPermission` 回调作为一等审批请求：映射到 task `waiting_input(approval)`（复用 `Decision.Kind=approval`），经 IM 原渠道 + PWA 推送「需审批：工具/参数/摘要」，用户回复或点击后桥接层返回 ACP `Selected{OptionId}`（AllowOnce/AllowAlways/Deny）或 `Cancelled`。在 ACP 路径上取代 `bypassPermissions` + PreToolUse hook 注入。

#### Scenario: 批准执行
- **WHEN** agent 要执行 Bash/Write 等敏感工具，发 `RequestPermission`
- **THEN** task 置 `waiting_input`，IM/PWA 收到审批卡片
- **WHEN** 用户批准
- **THEN** 桥接层 ACP Approve，agent 执行该工具，task 回 `running`

#### Scenario: 拒绝改路
- **WHEN** 用户拒绝
- **THEN** 桥接层 ACP Deny，agent 不执行该工具并改走它路
- **AND** task 回 `running`

#### Scenario: 审批超时
- **WHEN** 审批超过配置上限（参考 Phase 1 `hook_timeout`）
- **THEN** 返回 `Cancelled`，task 标记超时并通知 IM

### Requirement: AgentManager 统一调度
系统 SHALL 提供 `AgentManager` 承担现 `TaskRunner` 的 agent 调度职责：管理多个并发 agent 会话、每项目并发上限（复用现 `maxConcurrentPerProject` 信号量）、会话持久化、事件路由到 Bridge/EventBus/WS。worktree 创建、状态转换、IM 通知逻辑保留。

#### Scenario: 并发上限
- **WHEN** 某项目已有 `maxConcurrentPerProject` 个活跃会话
- **THEN** 新任务阻塞等槽位（保持 pending），不超限 spawn

### Requirement: ACP 会话持久化与续问
系统 SHALL 持久化 ACP Session 资源，并通过 ACP `session/load`/`session/resume`（及实验性 `session/list`/`session/fork`）支持续问，替换脆弱的 `--session-id`/`--resume` 匹配（"No conversation found"）。

#### Scenario: 续问复用上下文
- **WHEN** 在已结束任务上续问
- **THEN** 经 ACP `session/resume` 复用历史上下文，无需 CLI id 匹配
- **AND** 会话丢失时由协议层报告而非静默失败

### Requirement: 多 Agent 选择
系统 SHALL 通过配置选择 ACP agent（Claude Code / Qoder / Codex 等），每个会话 spawn 对应 agent 进程，共用同一套 acp-go-sdk client，接口统一。

#### Scenario: 切换 agent
- **WHEN** 配置指定 agent 类型为 qodercli
- **THEN** 会话 spawn `qodercli --acp`，走原生 ACP，无需适配器
- **AND** 桥接层接口不变

### Requirement: 工具调用与结果呈现
系统 SHALL 把 ACP `ToolCall`（开始）/`ToolCallUpdate`（状态变更）映射为现 `EventToolUse`/`EventToolResult` 事件，前端详情视图按 `Seq` 渲染，行为与 Phase 1 一致。

## MODIFIED Requirements

### Requirement: TaskRunner 职责拆分
`TaskRunner` 的 agent 驱动部分（`run`/`runClaude`/`parseStream`/`handleLine`/`Intervene`/`Cancel`）拆分到 `AgentAdapter`（ACPAgent/PrintAgent）+ `AgentManager`。保留：worktree 创建（`WorktreeManager`）、状态转换（`transition`/`setRunning`/`setWaitingInput`/`completeTask`/`failTask`）、事件追加（`appendEvent`）、IM 通知（`notifyWaitingInput`/`notifyFinished`）、标题生成（`generateTitle`）。

### Requirement: 流式事件模型
ACP 路径的文本产出改为内容增量（新增 delta 事件或细化 `EventText` 增量语义），前端按增量追加；PrintAgent 回退路径保留整块 `EventText`+200ms 合并。

### Requirement: 配置扩展
`PieqiConfig` 新增 ACP/agent 配置：`agent` 类型、`acp_spawn_command`（TS 适配器或 `--acp`）、`use_acp` 开关、ACP 路径审批超时。保留 `permission_mode`/`hook_tools`/`hook_timeout` 供 PrintAgent 回退。

## REMOVED Requirements

### Requirement: ACP 路径的 bypassPermissions + PreToolUse hook
**Reason**: ACP `RequestPermission` 是协议级一等公民，取代 hook hack。
**Migration**: 仅 ACP 路径停用；`PrintAgent` 回退路径保留 `HookService` + `WriteHookSettings` 注入，接口对齐。旧 IM 路径（`SessionRunner`+`ApprovalGate`）由 `legacy_im_path` 控制保留。

### Requirement: ACP 路径的 --session-id/--resume CLI 匹配
**Reason**: ACP Session 是协议资源，续问由 `session/resume` 承载，更健壮。
**Migration**: 仅 ACP 路径停用；`PrintAgent` 回退路径保留 CLI 匹配（含 "No conversation found" 回退为新会话重跑的兜底）。
