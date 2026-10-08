# pieqi Agent 技术方案（修订版）

> Claude 主力走**官方 Agent SDK 常驻桥**；对上统一 **AgentSession**；实现细节（bridge / print / ACP）不泄漏到业务层。

---

## 1. 目标与约束

### 1.1 目标

| 目标 | 说明 |
|------|------|
| Claude 多轮续问 | 同逻辑会话下，第二轮起避免整进程冷启动 |
| 真流式 | 增量文本/工具事件到 IM/PWA |
| 权限闭环 | 工具审批走现有飞书/企微/PWA |
| 多 Agent | qoder 等与 Claude 并存，业务只认 agent 名 |
| 可降级 | Claude 桥不可用时仍能跑通（print） |

### 1.2 已验证结论

| 事实 | 含义 |
|------|------|
| qodercli `--acp` 同 session 两连发、进程不退 | ACP 协议与 pieqi 保活模型成立 |
| claude-agent-acp 常在 turn 后进程退出 | **Claude 不宜再依赖该 adapter 做主力** |
| `claude -p --resume` 流式弱、冷启动慢 | 仅作兜底，不作默认 |

### 1.3 非目标

- 不为 Claude 再维护完整 ACP 兼容层  
- 不把所有 Agent 塞进同一个 Node 进程  
- AgentSession 接口不出现厂商/传输品牌名  

---

## 2. 分层架构

```text
┌──────────────────────────────────────────────────────┐
│  Channel：飞书 / 企微 / PWA / CLI                     │
└──────────────────────────┬───────────────────────────┘
                           │
┌──────────────────────────▼───────────────────────────┐
│  pieqi 业务：Task / 会话编排 / 审批 UI                  │
│  只依赖：AgentSession + Caps + 中性事件                 │
└──────────────────────────┬───────────────────────────┘
                           │ Open(agentName, cwd, opts)
┌──────────────────────────▼───────────────────────────┐
│  Agent 工厂（按 agent 名路由）                          │
│    "claude" → internal/agent/claude                  │
│    "qoder"  → internal/agent/qoder（ACP）             │
│    …                                                 │
└───────┬─────────────────────────────┬────────────────┘
        │                             │
        ▼                             ▼
┌───────────────────┐       ┌─────────────────────────┐
│ agent/claude      │       │ agent/qoder             │
│ 实现 AgentSession │       │ 实现 AgentSession       │
│ transport:        │       │ transport: acp only     │
│  · sdk-bridge     │       │ qodercli --acp          │
│  · print (fallback)│      └─────────────────────────┘
└─────────┬─────────┘
          │ HTTP/SSE（仅 Claude 默认）
          ▼
┌───────────────────┐
│ claude-sdk-bridge │  常驻 Node
│ 官方 Agent SDK    │
│ → claude CLI      │
└───────────────────┘
```

**原则**

- 业务选 **agent**（claude / qoder），不选 Driver 名  
- 传输（bridge / print / acp）留在各 agent 包内部  
- 一轮结束结束的是 **turn**，不是「编排进程」的生命（Claude bridge 必须遵守）

---

## 3. 核心抽象（中性）

### 3.1 AgentSession

```go
type AgentSession interface {
    ID() string
    Prompt(ctx context.Context, text string) error
    Cancel(ctx context.Context) error
    Close(ctx context.Context) error
    Caps() Caps
}

type Caps struct {
    MultiTurnPersistent bool // 同会话多轮且尽量保持底层执行器存活
    ResumeSupported     bool // 可凭会话 id 恢复上下文
    Streaming           bool
}
```

事件（callback 或 channel，名称保持中性）：

| 事件 | 含义 |
|------|------|
| `TextDelta` | 助手增量文本 |
| `ThinkingDelta` | 可选 |
| `ToolStart` / `ToolEnd` | 工具 |
| `PermissionNeeded` | 需用户审批 |
| `TurnEnd` | 本轮结束（可带 usage、底层 resume id） |
| `Error` | 错误 |
| `StateChanged` | idle / running / waiting_permission / closed |

**禁止**在公开接口出现：`Print`、`SDKBridge`、`ACP`、`ClaudeCode` 等类型名。

### 3.2 工厂

```go
session, err := agent.Open(ctx, agent.OpenParams{
    Agent: "claude", // 或 "qoder"
    Cwd:   cwd,
    // Metadata: taskId 等
})
```

内部读配置决定 Claude 用 bridge 还是 print，调用方无感。

### 3.3 续问策略（只看 Caps）

```text
Prompt 追加：
  if session 仍有效 && Caps.MultiTurnPersistent → 直接 Prompt
  else if Caps.ResumeSupported → 携带 resume 信息重新 Open 再 Prompt
  else → 新会话或明确失败
```

不写 `if print` / `if acp`。

---

## 4. Claude 后端设计

### 4.1 包结构

```text
internal/agent/claude/
  session.go       # 实现 AgentSession
  factory.go       # 按 config 选 transport
  bridge/          # HTTP 客户端 → claude-sdk-bridge
  print/           # claude -p [--resume]
  types.go
```

### 4.2 Transport 选择

```yaml
agents:
  claude:
    transport: sdk-bridge          # 默认主力
    bridge:
      base_url: "http://127.0.0.1:18790"
      auto_start: true
      start_cmd: ["node", "services/claude-sdk-bridge/dist/index.js"]
      idle_timeout_sec: 1800
    fallback:
      transport: print             # bridge 连续失败时
    print:
      command: "claude"
      # 额外 args 按需
```

| transport | Caps（预期） | 用途 |
|-----------|--------------|------|
| **sdk-bridge** | MultiTurnPersistent=true, Streaming=true, Resume=true | **默认** |
| **print** | MultiTurnPersistent=false, Streaming=弱, Resume=true | 兜底/批处理/排障 |

### 4.3 为何不默认 ACP

`claude-agent-acp` 将「查询流结束」与「进程退出」绑定，同 PID 续问不可靠；与 qoder 原生 ACP 不是同一质量。Claude 主力改为官方 SDK 自管，**不再把 claude-agent-acp 作为默认路径**（可保留实验开关，不进主路径）。

---

## 5. claude-sdk-bridge（官方 Agent SDK）

### 5.1 职责

- 常驻进程，不随单轮 turn 退出  
- 使用官方 `@anthropic-ai/claude-agent-sdk` 的 **streaming input**（或等价同进程多轮）  
- 对 pieqi 暴露薄 HTTP + SSE（或 WS）  
- 权限回调转成挂起请求，等 pieqi 回写  

### 5.2 会话模型

```text
Session {
  id, cwd, sdkSessionId?,
  state: idle | running | waiting_permission | closed,
  inputQueue,  // 多轮用户消息；Close 时 end
  abortController
}
```

核心循环：

```text
query({ prompt: asyncGenerator(from inputQueue), options: { cwd, resume, canUseTool, ... }})
for await (msg of query):
  映射为中性事件 → SSE
  on result → state=idle, 发 TurnEnd；**不 process.exit**
  继续等 inputQueue 下一轮
```

子进程崩溃：bridge 内用 `sdkSessionId` **resume** 重建 query，尽量对 pieqi 透明；失败则会话 `closed` + Error。

### 5.3 HTTP API（bridge 私有，仅 claude 包使用）

| 接口 | 作用 |
|------|------|
| `POST /v1/sessions` | 创建（可选 `resumeSdkSessionId`） |
| `POST /v1/sessions/:id/prompt` | 追加一轮 |
| `GET  /v1/sessions/:id/events` | SSE 事件流 |
| `POST /v1/sessions/:id/permissions/:rid` | allow/deny |
| `POST /v1/sessions/:id/cancel` | 取消本轮 |
| `DELETE /v1/sessions/:id` | 结束会话 |
| `GET  /v1/health` | 探活 |

事件 JSON 与第 3 节中性事件一一对应，由 `claude/bridge` 客户端翻译成 Go 事件，**不把 `/v1` 暴露给业务**。

### 5.4 进程管理

- 默认监听 `127.0.0.1`  
- pieqi `auto_start` 或 systemd 常驻  
- 可选共享 token  
- 空闲 session 按 `idle_timeout_sec` 回收  

---

## 6. Qoder（及其它 ACP Agent）

```text
internal/agent/qoder/
  session.go    # AgentSession
  acp/          # 现有 ACP 客户端逻辑可迁入
```

- 继续 `qodercli --acp`  
- Caps：`MultiTurnPersistent=true`（已实测）  
- 与 Claude **共享** AgentSession / 审批 / Task 编排，**不共享** Node bridge  

后续 Codex 等：新包 `internal/agent/codex`，同样实现接口即可。

### 6.1 dsh（DeepSeek Harness）实测的 ACP 面

`@deepseek-ai/dsh`（0.x）的 ACP 入口是一个 profile：`dsh --profile acp`（简写 `dsh acp`），
底层包 `@deepseek-ai/dsh-acp`，`agentInfo.name` 报 `deepseek-harness-acp`。2026-10-05 本机
Windows 实测：

| 项 | 实测结果 | 对 pieqi 的影响 |
|---|---|---|
| `initialize` | 热启动 1s 内返回；**首启动 >90s**（组装配方、挂插件） | `agents.dsh.acp.init_timeout` 默认放宽到 2m，否则第一次会话直接被 30s 判死 |
| `sessionCapabilities` | `close` / `list` / **`resume`**，**没有 `load`** | 续问走 `ACPAgent.NewSession` 的 `ResumeSession` 分支（现有判据自动选对，无需分叉） |
| 活跃会话上 `session/resume` | 报错 `session is already active` | 只有进程重启 / 会话已关后才能 resume；同进程内复用仍靠 pieqi 的轮间保活 |
| `session/request_permission` | 支持，一次性 allow/deny 选项 | 审批卡片链路正常，与 qoder 一致 |
| `session/new` 返回 `configOptions` | `model` / `reasoning_effort` 是**会话内选项**，改要发 `session/set_config_option` | 原生 dsh-acp 会下发；本仓库 fork 后**恒为空**（见 6.2），清单改走响应 `_meta`。pieqi 两条通道都读、并据此选写回方式（见 6.2.1）：fork 的 dsh 走 `_meta.model`，原生 agent 走 `set_config_option` |
| provider 路由 | 原生只来自 dsh `acp` profile 里 `acp` 这条 entry 的 pin（出厂 pin `deepseek-official`，需 `DEEPSEEK_API_KEY`）；pin **缺失或写错**时 `session/new` 直接失败 | 路由归 dsh 侧配置，pieqi 不干预。fork 后 pin 缺失会**回落到 `agent-default-model`**；写错（指向已下架的模型）仍会失败，`explainProbeErr` 会给可操作提示 |
| stdout | 只承载协议流量，无 banner 污染 | 满足 JSON-RPC over stdio 前提 |
| 客户端 fs / terminal / plan / elicitation | 文档明确「不支持的界面会被省略或拒绝」 | `acp.go` 目前虚报 `ClientCapabilities.Fs=true` 而实现返回 `ErrNotSupported`；dsh 不据此发调用，暂无影响，但**这是个待修的虚报** |

Windows spawn 形态：npm 全局装的 `dsh` 是 `.cmd` shim，Go 直接 spawn 会永久挂死（同 claude-code
的 npx 问题）。`internal/agent/acp.go` 的 `nodeCLIAgent` / `resolveNodeCLI` 会把它换成
`node <node_modules>/@deepseek-ai/dsh/lib/bin.js --profile acp`，配置里只写裸名。

### 6.2 本地 fork：配置可缺失 + 模型可外部指定

上游 dsh-acp 的模型路由只有「建会话那一刻的静态 pin」一个入口：`session/new` 不认模型参数，
`session/set_config_option` 又只接受**已经下发给客户端**的选项。于是「建会话指定」「每轮换模型」
都无处落脚，pin 缺失还会让 `selected` 悬空、会话彻底没模型。

本仓库维护了一份 fork（`forks/dsh-acp`，`node install.mjs` 装进 `~/.dsh-runtime`），走
ACP 请求的 `_meta` 扩展位把模型传进去 —— 不改协议字段，原生 agent 忽略 `_meta`，行为不变。
同时把 `configOptions` 对通用客户端隐藏，真实清单改挂响应
`_meta["pieqi/configOptions"]`，pieqi 侧 `internal/agent/model_catalog.go` 读同一个键。

三条能力：

| 能力 | 触发方式 |
|---|---|
| 配置缺失兜底 | `- id: acp` 的 provider/model 缺失 → 用 `- id: agent-default-model` |
| 建会话指定 | `session/new` / `session/resume` 带 `_meta.model` |
| 每轮换模型 | `session/prompt` 带 `_meta.model`（该轮生效，不落库） |

⚠️ 「缺失」**不是**「删掉 `- id: acp` 那一行」：`@deepseek-ai/dsh-acp-app` bundle 自带
`provider: deepseek-official`，删掉只会让 bundle 的值生效（需 `DEEPSEEK_API_KEY`，本机没有 ⇒
第一轮 prompt 报 `no API key for provider route`）。要表达不指定必须显式写空：

```yaml
- id: acp
  config:
    provider: !!js undefined
    model: !!js undefined
```

改动清单、`_meta` 契约、重打/还原与探针用法见 `forks/dsh-acp/FORK.md`；
pieqi 侧对应 `internal/agent/{acp,adapter,model_catalog}.go`。

#### 6.2.1 两条模型通道：`_meta` 是 dsh 专用，原生 agent 走标准字段

`_meta` 通道**只是 dsh 这一个 fork 的私有约定**，不是所有 ACP agent 的模型入口。
pieqi 侧必须**两条都读**，读哪条还决定**写**哪条（判据集中在
`internal/agent/model_catalog.go` 的 `catalogSource`）：

| agent | 清单来源 | 设模型的手段 |
|---|---|---|
| fork 的 dsh | `_meta["pieqi/configOptions"]` | 请求 `_meta.model`（它的 configOptions 恒空，`set_config_option` 会抛错） |
| 原生 agent（qodercli 等） | 标准 `configOptions` | `session/set_config_option`（它忽略 `_meta`） |

**优先级是标准字段优先**：标准字段存在即说明 agent 走协议正路、支持
`set_config_option`；fork 的 dsh 标准字段恒空，自然落到 `_meta` 分支。反过来先看 `_meta`
的话，一个"两者都发"的 agent 会被误判成 fork 通道，于是用 `_meta.model` 去设 —— 原生 agent
忽略它，表现是「界面显示已切换、实际没换」这种最难排查的静默失效。

这段历史值得记一笔：`bd0cd28`（移除 ACP 会话内选模型链路）为迁就 fork 后的 dsh，把清单读取
从标准字段**整体换成** `_meta`、并删掉 `set_config_option`。**qoder 是那次迁移的附带损伤** ——
它是原生 ACP 实现（实测 qodercli v1.1.64），`_meta` 整个缺失，于是清单恒空、前端显示
「该 Agent 不提供可选模型」，而它其实既下发清单（`qmodel_38max` / `qfmodel`）又接受
`set_config_option`。回归测试见 `internal/agent/model_catalog_test.go`（四个组合都钉住）。

探针：`go run ./.verify-acp/probe_qoder.go`（直连 qodercli，打印两个通道的实际内容并试设一次模型）。

---

## 7. Print transport（Claude 内部）

**定位**：保险，不是产品主叙事。

| 场景 | 行为 |
|------|------|
| bridge 起不来 / 健康检查失败 | fallback 到 print |
| 用户显式 `transport: print` | 直接 print |
| 排障 | 对比「纯 CLI」与 bridge |

实现：`claude -p` + 可选 `--resume <sdkSessionId>`；接受流式弱、续问较慢。

---

## 8. 权限与 Channel

全 agent 共用一套编排：

```text
PermissionNeeded
  → IM/PWA 审批卡片
  → session.RespondPermission(allow|deny)
  → 各 transport 内部落实
       Claude bridge → POST .../permissions
       Qoder ACP     → 协议内 permission 响应
       print         → 按 CLI 能力尽量映射或拒绝需交互的工具
```

---

## 9. 配置总表（示例）

```yaml
agents:
  claude:
    transport: sdk-bridge
    bridge:
      base_url: "http://127.0.0.1:18790"
      auto_start: true
      start_cmd: ["node", "services/claude-sdk-bridge/dist/index.js"]
      idle_timeout_sec: 1800
    fallback:
      transport: print
  qoder:
    transport: acp
    command: "qodercli"
    args: ["--acp"]

agent_manager:
  idle_timeout_sec: 1800
  # 按逻辑会话复用 AgentSession，与现网一致
```

---

## 10. 目录规划

```text
pieqi/
  internal/agent/
    session.go           # 接口、事件、Caps、Open 工厂
    claude/
      session.go
      factory.go
      bridge/
      print/
    qoder/
      session.go
      acp/
  services/              # 或独立仓
    claude-sdk-bridge/
      package.json
      src/
        index.ts
        server.ts
        session_runtime.ts
        sdk_runner.ts
        permission.ts
```

---

## 11. 实施分期

| 阶段 | 内容 | 验收 |
|------|------|------|
| **P0** | bridge：streaming input 两轮 + SSE；探针同 session 续问且进程不退 | 对齐 qoder 探针结论 |
| **P1** | `AgentSession` 接口 + `claude`（仅 bridge）接入 Task | Claude 任务默认可续问、可流式 |
| **P2** | 权限 + Cancel + idle 回收 | 与现网审批一致 |
| **P3** | print fallback + bridge auto_start/resume | 桥挂了任务仍可降级 |
| **P4** | qoder 迁到同接口；去掉业务对旧 Driver 名依赖 | 双 agent 统一编排 |
| **P5** | 文档/配置清理；claude-agent-acp 退出默认路径 | 主路径无 acp adapter |

---

## 12. 验收清单

**Claude（sdk-bridge）**

1. 创建会话 → prompt1 → 有 `TextDelta` → `TurnEnd`  
2. 桥接进程仍在 → 同 session prompt2 → 有上下文，延迟明显低于 print resume  
3. 工具触发 `PermissionNeeded` → 审批后继续 → `TurnEnd`  
4. Cancel 后再 Prompt 正常  
5. Close 后不可再 Prompt  
6. 模拟 CLI 子进程死亡 → 能 resume 或明确 Error 后可恢复  

**Qoder**

- 保持现有：同 session 两连发、进程不随 turn 退  

**抽象**

- 业务代码与公开 API **无** Print / SDKBridge / ACP 类型名  
- 配置选 `agents.claude` / `agents.qoder`，transport 仅出现在 agent 段  

---

## 13. 风险

| 风险 | 缓解 |
|------|------|
| SDK API 变更 | 钉版本；逻辑集中在 `sdk_runner.ts` |
| 多会话内存 | 并发上限 + idle 回收 |
| Windows | 先保 Linux/macOS；管道与信号单测 |
| 过度抽象 | 接口保持薄；新 agent 复制 qoder/claude 包模式即可 |

---

## 14. 决策摘要

| 项 | 决策 |
|----|------|
| Claude 主力 | **官方 Agent SDK + 常驻 claude-sdk-bridge** |
| Claude 兜底 | **print**（包内 transport，非一级 Agent） |
| Claude 默认 ACP adapter | **否** |
| Qoder | **原生 ACP**，实现同一 AgentSession |
| 业务抽象 | **AgentSession + Caps + 中性事件** |
| 命名 | 对外 agent 名；对内 transport |

**一句话：业务只打开名为 claude/qoder 的会话；Claude 在内部用官方 SDK 桥做长会话与流式，print 保底；qoder 继续 ACP——协议与传输都关在 agent 实现里，Session 层保持干净。**