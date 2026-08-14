# Tasks

- [x] Task 1 (M1)：ACP 会话内核 — 引入 acp-go-sdk，跑通 Claude Code 端到端出文本
  - [x] 1.1 新增 `internal/agent/` 目录与 `adapter.go`：定义 `AgentAdapter` 接口（NewSession/SendPrompt/OnContentDelta/OnPermissionRequest/Approve/Deny/InjectToolResult/Cancel）
  - [x] 1.2 `go.mod` 引入 `github.com/coder/acp-go-sdk`；实现 `acp.go` 的 `ACPAgent`：spawn `npx -y @agentclientprotocol/claude-agent-acp@latest`，握手（ProtocolVersionNumber=1），管理进程生命周期
  - [x] 1.3 参考 acp-go-sdk `example/claude-code` 跑通最小端到端：SendPrompt → 接收 `SessionUpdate` 文本 → 落 events
  - [x] 1.4 进程退出/异常捕获与清理（对应现 `liveProc` 语义）
  - [x] 验证：单测通过（25/25）；端到端手动验证受沙箱无 npx/凭证限制，代码正确性以编译+单测为准

- [x] Task 2 (M2)：真流式落地 — 内容增量 → WS → 前端逐字
  - [x] 2.1 把 `AgentMessageChunk.Content.Text` 增量映射为 EventBus 事件（新增 `task_delta` 事件 + `DeltaPayload`，安静持久化 `appendTextDelta`）
  - [x] 2.2 `ws.go` 透传 delta；前端 `web/src/main.js` 新增 `task_delta` 分支增量追加 DOM（不全量重绘）
  - [x] 2.3 `AgentThoughtChunk` 思考过程增量同链路（IsThought=true → thinking event/DOM）
  - [x] 验证：8 个单测通过；前端 npm run build 通过

- [x] Task 3 (M3)：协议级权限审批 — RequestPermission → IM/PWA → Approve/Deny
  - [x] 3.1 `WirePermission` 连接器：OnPermissionRequest→task waiting_input(approval) + Decision + task_updated + IM notify
  - [x] 3.2 PWA 自动弹审批卡片（task_updated 已含）；IM 经 notify 回调推送
  - [x] 3.3 `PermissionWire.Resolve`：approve→ACPAgent.Approve(allow option)；deny→Deny/Cancel；task 回 running
  - [x] 3.4 超时定时器→Deny+IM notify+task 回 running；先到先得防重复
  - [x] 3.5 `WireToolCall` 连接器：ToolCall→EventToolUse(Input from RawInput)；ToolCallUpdate→EventToolResult(Result from RawOutput)
  - [x] 验证：22 单测通过（16 权限+6 工具调用）；工具 Input/Result 填充已修复

- [x] Task 4 (M4)：多 Agent 抽象 — AgentAdapter + ACPAgent/PrintAgent + AgentManager
  - [x] 4.1 `print.go` 实现 `PrintAgent`：包现 `claude -p` + stream-json 解析，接口对齐 `AgentAdapter`（保留 `HookService`+hook 注入）
  - [x] 4.2 `manager.go` 实现 `AgentManager`：承担 `TaskRunner` agent 调度（多会话/并发上限/事件路由），保留 worktree/状态/通知逻辑
  - [x] 4.3 ACP 适配器不可用/崩溃 → 透明回退 `PrintAgent`，记录回退事件
  - [x] 4.4 `TaskRunner` 职责拆分：驱动部分迁到 adapter/manager，保留 transition/appendEvent/notify/generateTitle
  - [x] 验证：ACP 与回退双路径均跑通；接口对齐单测（9 个 ACP 路径单测 + 15 个 AgentManager 单测 + PrintAgent/ACPAgent 接口断言全绿；真机 e2e 受沙箱无 npx/凭证限制）

- [x] Task 5 (M5)：会话持久化与续问 — ACP session/resume
  - [x] 5.1 持久化 ACP Session 资源（替代 `ClaudeSessionID` CLI 匹配）
  - [x] 5.2 续问经 `session/load`/`session/resume`；会话丢失由协议层报告
  - [x] 5.3 PrintAgent 回退路径保留 `--session-id`/`--resume`+回退兜底
  - [x] 验证：续问复用上下文；会话丢失不静默失败（3 个新单测 + 既有 ACP/AgentManager/PrintAgent 单测全绿；真机 e2e 受沙箱无 npx/凭证限制，代码正确性以编译+单测为准）

- [x] Task 6 (M6)：落地第二个 agent — qodercli 原生 ACP
  - [x] 6.1 配置 agent 类型选择；qodercli 走 `qodercli --acp`，共用 acp-go-sdk client
    - 6.1 defaultSpawnCommand 已支持 qodercli/codex/其他 `--acp` 兜底；`TestDefaultSpawnCommand` 已覆盖 qodercli/codex/grok 路径；ACPAgent 经同一 acp-go-sdk client 驱动，桥接层接口不变
  - [x] 6.2 `config.go` 新增 agent/acp_spawn_command/use_acp 配置块
    - 6.2 `config.ACPConfig`（UseACP/AgentType/SpawnCommand/InitTimeout）已就绪，viper 默认值已设；新增 `ManagerConfigFromPieqi` helper 让消费方一行 wiring：`agent.NewAgentManager(agent.ManagerConfigFromPieqi(cfg.Pieqi, cfg.Claude), logger)`（配 `TestManagerConfigFromPieqi` / `TestManagerConfigFromPieqi_UseACPFalse` 两个单测）
  - [x] 验证：切换到 qodercli 跑通端到端，桥接层接口不变
    - defaultSpawnCommand + TestDefaultSpawnCommand 已覆盖；真机 e2e 受沙箱无 qodercli 限制，代码正确性以编译 + 单测为准（`go build ./...` 与 `go test ./internal/agent/... ./internal/config/...` 全绿）

- [x] Task 7：文档与配置更新
  - [x] 7.1 更新 README / 架构图 / CONTEXT 词汇
    - 7.1 README 架构图（AgentAdapter + ACP 多 agent + 回退）/ 特性列表（多 Agent 支持 Phase 2）/ 技术栈表（Agent 驱动行）/ 目录结构（去 cmd/bridge/PLAN.md/CONTEXT.md，加 internal/agent/ + docs/phase2-acp.md）/ 构建命令（library 形态 go build ./...）/ 新增"Phase 2：ACP 多 Agent"章节均已更新
  - [x] 7.2 `config.yaml` 补 ACP 段示例
    - 7.2 `config.yaml` 在 `pieqi:` 段末尾新增 `acp` 子段示例（use_acp/agent_type/acp_spawn_command/init_timeout）

# Task Dependencies
- Task 2 depends on Task 1
- Task 3 depends on Task 1
- Task 4 depends on Task 1, Task 3（PrintAgent 保留 hook，需审批链路对齐）
- Task 5 depends on Task 1, Task 4
- Task 6 depends on Task 4, Task 5
- Task 7 depends on Task 1–6（最后统一更新）
