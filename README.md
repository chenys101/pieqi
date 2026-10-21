# Pieqi

把 **Claude Code / qoder / dsh 等 coding agent** 接入 IM（飞书 / 企微 / 微信）与移动端 PWA 的桥接服务：在手机上发消息即可创建任务、续问、审批工具调用，实时看到逐字流式输出，并在改完后**看见改了什么、验证对不对、必要时回退、带证据继续**。

```
飞书(长连接/webhook) ─┐
企业微信 / 微信      ─┼─→ Pieqi(Go 单二进制) ─→ AgentManager ─→ 多种 agent 传输：
移动端 PWA (:3000)  ─┘        │                  ├─ claude：sdk-bridge（官方 Agent SDK 常驻桥，默认）
                               │                  ├─ claude：print 回退（claude -p stream-json）
                               │                  └─ qoder / dsh 等：ACP（JSON-RPC over stdio）
                               ├── 任务调度：worktree 隔离 / 每项目并发上限 / 审批 / 续问 / 自重启
                               ├── 反馈体系：Changes / Diff / Checks / Preview / Rewind / Outcome
                               └── 安全：Cloudflare Access 或外链 token + 隧道 + 限流 / 审计
```

> 人类阅读看本文件；**给 AI coding agent 的协作指南见 [`AGENTS.md`](AGENTS.md)**（发版流程、沙箱纪律、审批分级、血泪坑都在那里）；领域术语以 [`CONTEXT.md`](CONTEXT.md) 为唯一真相。

***

## 特性

* **多 Agent 统一驱动**：`AgentManager` + `AgentSession` 统一接口，按 `agents.*` 配置选择传输：

  * `claude` → `sdk-bridge`（默认）：常驻 Node 桥服务（`services/claude-sdk-bridge`，:18790）封装官方 Claude Agent SDK，探活失败自动拉起；桥不可用时透明回退 `print`（`claude -p --output-format stream-json`）。

  * `qoder` 等 → 原生 ACP（`qodercli --acp`，`coder/acp-go-sdk`）。

  * `dsh` → 同为 ACP，但入口是一个 profile（`dsh --profile acp`，DeepSeek Harness）。默认**不注册**：需要本机装 `@deepseek-ai/dsh`，且其 `acp` profile 里 pin 了可用模型路由（出厂路由需 `DEEPSEEK_API_KEY`，也可改 pin 到本地网关等免 key 的 provider）；模型由 dsh 侧决定，不在本项目配置里。

  * 可选 agent 列表以 `GET /api/agents` 为唯一事实源，前端选择器由它驱动。

* **真流式输出**：内容增量（含思考过程）→ EventBus → WebSocket → PWA 逐字追加渲染；上下文用量（Usage）作为瞬时量单独推送。

* **协议级审批 + 免审名单**：agent 请求敏感工具时任务进入 `waiting_input`，IM / PWA 弹审批卡，Approve / Deny（AllowOnce / AllowAlways）直达 agent。风险按 ToolKind 分四档（L0 读 / L1 编辑 / L2 执行 / L3 删除），设置页两个开关（`auto_approve_l0`/`auto_approve_l1`，默认都开）决定哪些档免审；**只读 shell 命令、测试/构建命令按白名单降级**，而**删除、工作区外写、`git push`、改基线永远人工**（ADR-0008 / ADR-0015）。

* **反馈体系（Feedback，Observe → Verify → Control → Intervene）**：Turn 变更列表与 Diff、审批前的前瞻 Diff、Checks（复用 agent 已跑过的 test/lint/build）、Task Outcome、Evidence → Continue、Checkpoint / Rewind、Preview（把改完的代码跑起来在手机上看）。术语见 [`CONTEXT.md`](CONTEXT.md)，设计见 [`docs/feedback/`](docs/feedback/README.md)。

* **任务并行与隔离**：任务可在任意本地路径运行，或建 git worktree 隔离并行，每项目并发上限控制；`base_branch` 可配。

* **跨渠道会话共享**：身份映射把各渠道用户 ID 归一为统一身份，同一用户换渠道可续接上下文。

* **飞书接入极简化**：长连接模式（`event_mode: longconn`）WSS 订阅事件，**无需公网回调**；Device Flow 扫码**一键创建飞书自建应用**（`/api/larkreg`），凭据落盘并热生效；支持多机器人绑定记录（`/api/bots`）。

* **外网安全访问**（详见 [`docs/security-hardening.md`](docs/security-hardening.md)）：有固定域名走 **Cloudflare Access**（源站自行验签 JWT）/ 无域名走**外链 `?token=`**（内存 TTL、不落盘）二选一，两道都关 = 外网全拒；Cloudflared 隧道（quick / named）带健康巡检与域名回收自愈；IP 失败限流拉黑 + 审计日志。

* **远程自迭代**：改完代码可投递 `build/pieqi.new.exe` 并 `POST /api/admin/restart` 换版自重启，**重启后接回在途会话（含发起重启的那个会话）**；另有 `POST /api/admin/restart-only` 只重启不换二进制。

* **移动端 PWA**：Vue3 前端 `//go:embed` 进二进制；仪表盘 / 会话时间线 / 任务浏览器 / Agents / 设置五页，双主题、三视口（手机/平板/桌面），`/sessions/<id>` 深链接（兼容旧 `/session/<id>`）。

* **单二进制部署**：前端嵌入，前端源码与桥服务随发布包同装。

***

## 架构

### 请求流

`渠道(channel) / HTTP(api) / WebSocket → internal/core → internal/agent → 真实 agent 进程`

入口 `cmd/pieqi/main.go` 是**双模式二进制**：无参数启动主服务；`pieqi pre-tool-use` 作为 **Claude Code PreToolUse hook 子进程**运行，从 stdin 读 hook 输入 → 回连主进程 `/internal/hook` 等人类决策 → 输出 permissionDecision（print 路径的审批闭环）。hook 通过往 worktree 写 `.claude/settings.json` 注入（`internal/core/hook_settings.go`）；`pieqi.permission_mode` 默认 `bypassPermissions` —— 放行由 hook 真正拦截，不是 SDK 层。

### Agent 驱动三层

| 层    | 代码                                                                        | 职责                           |
| ---- | ------------------------------------------------------------------------- | ---------------------------- |
| 调度   | `internal/core/task_runner.go`                                            | 状态机、worktree、事件发布、IM 通知、标题生成 |
| 会话管理 | `internal/agent/manager.go` + `session.go`                                | 按 taskID 管理会话、项目级并发槽、传输降级回退    |
| 传输   | `internal/agent/claude/`（sdk-bridge / print）、`internal/agent/acp.go`（ACP） | 具体 agent 协议实现                |

* **sdk-bridge**：`services/claude-sdk-bridge`（Node ≥20，HTTP+SSE：`/v1/sessions` / `/prompt` / `/events`），封装 Claude Agent SDK 的多轮会话、权限回调、取消；Go 侧客户端见 `internal/agent/claude/bridge/client.go`，`auto_start: true` 时探活失败自动 spawn。

* **print 回退**：`claude -p --session-id/--resume` + `stream-json` 逐事件解析（`internal/agent/print.go` / `print_stream.go`）。

* **ACP**：JSON-RPC over stdio，`session/request_permission` / `session/prompt` / `session/resume` / `session/set_config_option` 为协议级能力（`internal/agent/acp.go`）。模型清单有**两条通道**（标准 `configOptions` 优先，`_meta["pieqi/configOptions"]` 为兜底），选哪条由 `internal/agent/model_catalog.go` 的 `catalogSource` 判定。

* 加一个 ACP agent 只碰三处：`config.AgentsConfig` 加一节、`agent.defaultACPAgentType` 加一行、`ACPProviderConfigFromAgents` 加一个分支（细节见 `AGENTS.md`）。

> 旧配置 `pieqi.acp.*` 已弃用（P5 迁移），显式配置会触发告警；请使用 `agents.*` 节。详见 [`docs/multi-agent.md`](docs/multi-agent.md)。

### 任务生命周期与两条审批路径

```
pending → running → waiting_input ⇄ running → completed | failed | cancelled
```

`waiting_input` 分两种，**恢复路径不同**（`model.DecisionKind`）：

* **路径 A（approval）**：PreToolUse hook / ACP RequestPermission 触发，**agent 进程还活着**挂在 channel 上；恢复走 resolve → 同一进程继续。

* **路径 B（choice）**：Claude 文本提问（`[CHOICE]` 格式），Claude 已 `end_turn` 退出（**进程已死**）；恢复走 `--resume -p <选项>` → 新进程。

这直接决定重启行为：`TaskStore.load()` 启动时把 `running` 与等待 approval 的任务标记 `failed`（子进程已死），等待 choice 的任务**保留** `waiting_input`，可隔天继续选。

### 事件与持久化

* **EventBus**（`internal/core/event_bus.go`）是内存 fan-out，订阅者慢则丢弃积压。事件类型：`task_created | task_updated | task_deleted | task_delta | task_usage`；`task_delta` / `task_usage` 只带增量，其余带完整 Task 副本，由 `internal/api/ws.go` 转发给 PWA。

* **没有 SQL 数据库**：一任务一文件 `~/.pieqi/tasks/<id>.json`，每次变更 `os.Rename` 原子写（`internal/core/task_store.go`）。`TaskEvent` 是内嵌在 Task JSON 里的切片，**它就是反馈系统的事实源**（ADR-0001）。

### 反馈体系（Feedback）

`internal/core/` 下 `feedback.go / git_diff.go / checkpoint.go / prospective.go / checks.go / evidence.go / outcome.go / preview.go / push.go` 构成 Observe→Verify→Control→Intervene 闭环。事实源关系（改这块先看 ADR）：

* `TaskEvent` 是事实源，`FileChange` 从事件派生（[ADR-0001](docs/adr/0001-taskevent-source-of-truth-filechange-derived.md)）。
* Baseline 是只读 git HEAD 参照，绝不写用户分支；Checkpoint 只覆盖 Agent 改过的文件（[ADR-0002](docs/adr/0002-readonly-git-baseline-file-snapshot-checkpoint.md)）。
* Turn 以用户消息为界（[ADR-0003](docs/adr/0003-turn-bounded-by-user-message.md)），是变更分组 / Checkpoint / Rewind 的基准单位。
* Evidence→Continue 由**后端**把证据组装成上下文（[ADR-0004](docs/adr/0004-evidence-continue-backend-assembled.md)）。
* Check 优先复用 Agent 已跑过的 test/lint/build，而非重跑（[ADR-0005](docs/adr/0005-checks-reuse-agent-tool-results.md)）。
* ⚠️ **视觉反馈（截图 / console / network）已于 2026-10-15 整体移除**，不再是 pieqi 的能力（[ADR-0012](docs/adr/0012-remove-screenshot-keep-console-network.md) / [ADR-0013](docs/adr/0013-remove-visual-capture-entirely.md)）。四个 preview 子路径（`capture` / `console` / `network` / `screenshots`）现在**显式返回 410 Gone**，**preview 本身不受影响**。
* 明确不做 Web IDE / Computer Use / 录屏（[ADR-0007](docs/adr/0007-feedback-non-goals.md)）：查看文件属于 Feedback，编辑文件属于 IDE。

### 安全模型（`internal/auth`）

* **主鉴权按部署形态二选一**：有固定域名（named）→ **Cloudflare Access**（源站侧 JWT 验签：强制 RS256、核 `aud`/`iss`、校 `exp/nbf`）；无域名（quick / 纯内网）→ 外链 `?token=`（内存 TTL token，不落盘）。named 下 `token_fallback` 可关；两道全关 = 外网 401 全拒。

* **客户端 IP 只能从可信转发头推导**：优先 `CF-Connecting-IP`，否则取 `X-Forwarded-For` **末段**（取首跳曾被伪造从而完全绕过鉴权）。

* **路由按「bootstrap path vs mutation path」分层 gate**；`/api/admin/*`（自重启）与 `/api/auth/bind`、`/api/bots` 写接口**仅内网**。

* **管理员绑定 + 限流 + 审计**：首个飞书用户经 `/api/auth/bind`（仅内网）绑定为唯一 admin；认证失败 5 次/分钟 → 拉黑 10 分钟；外部请求记审计日志。

* **隧道自愈**：健康巡检连续失败达阈值自动重启换域名，并把新链接推给飞书管理员；PID 文件跨重启清理孤儿 `cloudflared`。

***

## 目录结构

```
pieqi/
├── cmd/pieqi/                 # 入口：主服务 + pre-tool-use hook 子命令 + PWA 静态托管(web.go)
│   ├── main.go                # 装配与接线（逐层 wire：auth / feedback / settings / selfrestart）
│   ├── web.go                 # 前端静态托管 + SPA fallback（资源由 web/embed.go 的 //go:embed 提供）
│   ├── larkchannel.go         # 飞书渠道控制器：一台机器人 = 一个实例，差量重建 + 配置热应用（不重启）
│   └── reload_restart.go      # 自重启端点的接线
├── internal/
│   ├── agent/                 # AgentSession/Manager 抽象 + 各传输
│   │   ├── acp.go             # ACP 传输（qoder / dsh / codex）
│   │   ├── print*.go          # print 回退传输
│   │   ├── readonly_kind.go   # 只读命令降级（ADR-0008）
│   │   ├── shell_runner.go    # 测试/构建命令族分档（ADR-0015）
│   │   ├── noncommand_tool.go # lsp/job_output/todo_write 按参数证明只读
│   │   ├── model_catalog.go   # 模型清单两条通道的判定
│   │   └── claude/            # claude 专属：bridge/(sdk-bridge 客户端)、proc、session
│   ├── api/                   # HTTP API：tasks / feedback / preview / agents / settings / ws / auth / tunnel / larkreg / bots / admin / diagnostics
│   ├── auth/                  # Access JWT 验签、token、绑定、Cloudflared 管理、限流、审计、中间件
│   ├── channel/               # 渠道适配：lark（webhook + 长连接）/ wechat / receiver / sender
│   ├── config/                # viper 配置加载 + 弃用迁移
│   ├── core/                  # TaskRunner / TaskStore / EventBus / Hook / Worktree / Feedback / Preview / SelfRestart / 扫描器
│   ├── larkreg/               # 飞书 Device Flow 一键注册自建应用
│   ├── logging/               # zap + 按日滚动日志文件
│   └── model/                 # Task / TaskEvent / Decision / Bot 等数据结构
├── services/claude-sdk-bridge/ # Node 常驻桥：Claude Agent SDK HTTP+SSE 封装（见其 README）
├── web/                       # PWA 前端（Vite + Vue3 + Tailwind + Pinia，见 web/README.md）
│   └── e2e/                   # Playwright 真机验收：layout / visual / interaction / online
├── docs/                      # 设计与规划文档（索引见 docs/README.md）
├── build/                     # 运维脚本（build.sh / restart.bat / start*.bat）+ 唯一编译产物落点
├── config.example.yaml        # 配置模板：全字段 + 注释，复制成 config.yaml 后修改
└── config.yaml                # 本地实际配置（含个人域名/端口/凭据，.gitignore 忽略，不入库）
```

### 目录职责（三个位置，别混）

| 路径 | 放什么 | 谁能写 |
|---|---|---|
| `<repo>/build/` | 运维脚本 + `pieqi.new.exe`（**唯一**的编译/交付产物） | agent（会话可写工作区） |
| `%USERPROFILE%\.pieqi\bin\` | `pieqi.exe`（**运行产物**）+ `.old` 回滚备份 + `.failed` 失败证据 | `build/restart.bat` / 服务自重启 |
| `~/.pieqi/`（`PIEQI_HOME` 可覆盖） | 运行时数据：`tasks/`、`worktrees/`、`checkpoints/`、`logs/`、`previews/`、`bots/`、`feishu_binding.json`、`lark_credentials.json`、`cloudflared_token` 等 | 服务 |

> ⚠️ **运行产物必须在仓库外**。仓库目录带 Windows Low 完整性标签，区内 exe 起的进程继承它，
> 于是能读 `~/.pieqi` 却**写不了**（症状：创建任务 500、日志零增长）。安装一律用 `build/restart.bat`
> （它用 `copy` 把产物拷到区外，`copy` 会丢掉 Low 标签）。

**自重启的交付落点没有配置项，也没有环境变量**——由约定推导：`<config.yaml 所在目录>/build`。
（`config.yaml` 是启动本来就必需的输入，且与 `go.mod` 同级 = 仓库根，所以无需另配。）

> 注：会话（session）记录**不在** `~/.pieqi/` 下。pieqi 只把会话 id 存进 `tasks/<id>.json`，
> 真正的会话实体由外部 agent 自己保管（例如 dsh 在 `~/.dsh/storages/` 下）。

***

## 快速开始

### 后端

```bash
# 准备配置（首次运行必做；模板已含可跑默认值）
cp config.example.yaml config.yaml

# 构建二进制 —— 落点必须是 build/pieqi.new.exe（**只写 .new**，见「目录职责」）
go build -o build/pieqi.new.exe ./cmd/pieqi

# 安装运行副本并启动（把产物拷到仓库外，再以正常身份运行）
pwsh -File build/restart.bat        # Windows

# 或直接运行（开发用；注意这会以当前身份启动，不经仓库外安装）
go run ./cmd/pieqi

# 测试
go test ./internal/...                      # 单元测试（122 个 _test.go）
go test -tags integration ./internal/...    # 集成测试（需 claude CLI / 桥服务在跑）
```

> ⚠️ **别把产物编到 `bin/` 或直接覆盖 `build/pieqi.exe` / 运行位 `pieqi.exe`**：
> 前者是历史上的 ACL 缺口目录，后者会让自重启的"回滚备份"保存成刚编出来的那份（回滚即失效）。

### 前端

```bash
cd web
npm install
npm run build        # vue-tsc -b && vite build → web/dist，由 go:embed 打进二进制
npm run dev          # 开发服务器（:5174，/api、/internal 代理到 :3000）
npm run test         # vitest（23 个 *.spec.ts）
npm run test:e2e     # Playwright 真机验收（layout / visual / interaction，见 docs/testing/ui-e2e.md）
npm run test:e2e:impact  # 按改动路径只跑相关的
```

> 前端改动后需 `npm run build` 再重编译 Go，才会嵌入新前端。

### 桥服务自测

```bash
cd services/claude-sdk-bridge && npm install && npm test   # 工具名映射 / 图片块 / 流式 / 全链路
```

### 飞书接入（两种方式）

1. **一键注册（推荐）**：本机打开 PWA → 设置 → 添加渠道 → 扫码，自动创建飞书自建应用并写入凭据。
2. **手动配置**：在[飞书开放平台](https://open.feishu.cn)创建自建应用，填 `config.yaml` 的 `channels.lark`（长连接只需 `app_id` + `app_secret`；webhook 模式还需 `verify_token` + `encrypt_key`）。

### 关键配置

完整字段、可选值与环境变量覆盖方式见 **[`config.example.yaml`](config.example.yaml)**（每个旋钮都带注释）。日常最常改的几项：

```yaml
server:
  port: 3000

channels:
  lark:
    enabled: true
    event_mode: longconn   # longconn(推荐,无需公网) | webhook(需公网回调)

agents:
  claude:
    transport: sdk-bridge  # sdk-bridge(默认) | print
    bridge:
      base_url: "http://127.0.0.1:18790"
      auto_start: true     # 探活失败自动 spawn 桥服务
  qoder:
    transport: acp         # qodercli --acp
  dsh:
    transport: ""          # 留空 = 不注册；改成 acp 即接 dsh --profile acp（模型路由在 dsh 侧 profile 里 pin）

pieqi:
  max_concurrent_per_project: 4
  base_branch: main        # worktree 基准分支（按你仓库的主分支改：main / master）
  hook_timeout: 30m        # 审批等待上限（print 回退路径）
  hook_tools: [Bash, Write, Edit, NotebookEdit]

auth:
  cloudflared:
    mode: quick            # quick=临时随机域名(默认,零配置可用) | named=固定域名(可配 Cloudflare Access)
    default_ttl: 15m       # 外链 ?token= 有效期：15m | 1h | 4h
  cloudflare_access:
    enabled: false         # named 形态建议开启；源站会自行验签 Cf-Access-Jwt-Assertion
  ratelimit:
    max_failures_per_min: 5
```

> 环境变量：`PIEQI_CONFIG`（配置路径）、`PIEQI_HOME`（运行时数据根）、`PIEQI_` 前缀覆盖任意配置项（如 `PIEQI_SERVER_PORT`）。

***

## HTTP API

### 任务与会话（需内网 / 有效 token）

| 方法            | 路径                                     | 说明                                         |
| ------------- | -------------------------------------- | ------------------------------------------ |
| GET / POST    | `/api/tasks`                           | 任务列表（按项目分组）/ 新建（`project_path` + `prompt`） |
| GET / DELETE  | `/api/tasks/:id`                       | 任务详情 / 删除                                  |
| GET           | `/api/tasks/:id/capabilities`          | 会话此刻的能力位（如能否收图）                            |
| POST          | `/api/tasks/:id/intervene`             | 审批决策 / 追加续问（append\_prompt，可带图片）           |
| POST          | `/api/tasks/:id/model`                 | 会话内切换模型（落库 + 追加 `model_switch` 事件）         |
| POST          | `/api/tasks/:id/cancel`                | 取消任务                                       |
| GET           | `/api/ws`                              | WebSocket 实时事件推送                           |
| GET           | `/api/skills` · `/api/commands`        | Skill / 自定义命令补全数据源                         |
| GET           | `/api/agents` · `/api/agents/:name/models` | 可选 agent 目录 / 某 agent 的可选模型清单             |

### 反馈体系（Feedback）

| 方法   | 路径                                                     | 说明                     |
| ---- | ------------------------------------------------------ | ---------------------- |
| GET  | `/api/tasks/:id/feedback`                              | 总览：Turns / Changes / 摘要 |
| GET  | `/api/tasks/:id/feedback/diff?path=&turn=`             | 回顾性 Diff               |
| GET  | `/api/tasks/:id/approvals/:decisionId/diff`            | 审批前的前瞻性 Diff           |
| GET  | `/api/tasks/:id/checks`                                | System Checks 列表        |
| POST | `/api/tasks/:id/checks/:checkId/rerun`                 | 重跑某条 Check            |
| GET  | `/api/tasks/:id/outcome`                               | Task Outcome           |
| GET  | `/api/tasks/:id/evidence?scope=`                       | Evidence 聚合            |
| POST | `/api/tasks/:id/continue`                              | 带证据继续（后端组装上下文）         |
| POST | `/api/tasks/:id/rewind`                                | Checkpoint / File Rewind |
| POST | `/api/tasks/:id/push`                                  | Evidence Push 到 IM / Webhook |
| GET  | `/api/tasks/:id/file?path=`                            | 文件预览（markdown / pdf）    |
| ANY  | `/api/tasks/:id/preview/*path` | Preview 控制（`start` / `status` / `attach`） |
| ANY  | `/preview/:id/*path`           | Preview 鉴权反向代理（**刻意独立于 `/api`**，避免请求进入被预览项目自己的 `/api` 代理规则） |

### 认证、隧道与绑定

| 方法            | 路径                                       | 访问策略          | 说明                           |
| ------------- | ---------------------------------------- | ------------- | ---------------------------- |
| POST / DELETE | `/api/auth/bind`                         | 仅内网           | 绑定 / 解绑管理员飞书身份               |
| GET           | `/api/auth/status`                       | 公开            | 绑定状态（前端启动轮询）                 |
| POST          | `/api/tunnel/start`                      | 有效 token + UA 门 | 启动 Cloudflared 隧道 |
| POST          | `/api/tunnel/stop` · `/reset` · `/renew` | 有效 token + UA 门 | 停止 / 重置 / 续期                 |
| GET           | `/api/tunnel/status` · `/qrcode`         | 公开（token 脱敏）  | 隧道状态 / 二维码                   |
| GET           | `/api/bots`                              | 同 `/api` 主组   | 机器人绑定记录（不含 secret）            |
| POST/PATCH/DELETE | `/api/bots[/:id]`                    | 仅内网           | 增改删机器人（变更后热重建渠道实例）           |
| POST          | `/api/larkreg/start` · `/config`         | 仅内网           | Device Flow 一键注册飞书应用 / 手写配置  |
| GET           | `/api/larkreg/poll` · `/status` · `/config` | 仅内网        | 轮询注册进度 / 状态 / 读配置             |

### 偏好、运维与其他

| 方法         | 路径                         | 访问策略       | 说明                                  |
| ---------- | -------------------------- | ---------- | ----------------------------------- |
| GET/PATCH  | `/api/settings`            | 内网 / 有效 token | 免审开关（L0/L1）、免打扰、事件保留上限         |
| GET        | `/api/diagnostics/export`  | 内网 / 有效 token | 打包最近 7 天日志为 zip                    |
| GET / POST | `/api/admin/restart`       | 仅内网        | 自重启状态（`staged`）/ 换版重启（无产物返回 409）      |
| POST       | `/api/admin/restart-only`  | 仅内网        | **只重启不换二进制**（无任何文件副作用）              |
| POST       | `/api/admin/backfill-titles` | 仅内网      | 为缺标题的任务补跑标题生成                        |
| POST       | `/internal/hook`           | 仅本地        | pre-tool-use hook 子进程回连（print 路径审批）   |
| GET        | `/*`（未命中路径）              | —          | PWA SPA fallback（`cmd/pieqi/web.go`）；`/api/`、`/internal/`、`/webhook/` 前缀返回 404 JSON |

> 注：**pieqi 服务端没有 `/health` 端点**。就绪判据是 `:3000` 是否 LISTENING（`build/restart.bat` 就这么判）；
> 脚本化探活可以打 `GET /api/tasks`（内网 200）。桥服务另有自己的 `GET /v1/health`。

***

## 发布 / 打包

```bash
# 打包发布物（前端构建 → linux/windows 交叉编译 → zip/tar.gz；产物落 build/release/，不污染交付位）
bash build/build.sh [版本号]

# 手动交叉编译（落点同样只写自己的文件名，不要碰 pieqi.new.exe / 运行位 pieqi.exe）
GOOS=linux   GOARCH=amd64 go build -o build/release/pieqi-linux-amd64 ./cmd/pieqi
GOOS=windows GOARCH=amd64 go build -o build/release/pieqi-windows-amd64.exe ./cmd/pieqi
```

* **Windows**：`build/restart.bat` 是唯一安装入口——**有 `build/pieqi.new.exe` 就换版重启，没有就只重启**；
  发布包内脚本与 `pieqi.exe` 平铺同级，`restart.bat` 会自动识别这种布局。**不要**直接双击 `pieqi.exe`（理由见「目录职责」）。

* **Linux**：systemd 托管。

* **运行依赖**：`claude` CLI（登录态）、Node.js ≥20（桥服务）、`cloudflared`（需要外网访问时）；qoder / dsh 等 ACP agent 需本机装对应 CLI。

完整发版四步（前端 build → 编 `.new` → `restart.bat` 投递 → `POST /api/admin/restart`）与验证清单见 [`AGENTS.md`](AGENTS.md)「发版流程」。

***

## 文档索引

顶层索引见 **[`docs/README.md`](docs/README.md)**。常用入口：

| 想知道什么 | 看哪里 |
|---|---|
| 术语怎么用 / 什么不做 | [`CONTEXT.md`](CONTEXT.md) |
| 怎么改代码、发版、避坑 | [`AGENTS.md`](AGENTS.md) |
| 多 Agent 架构（`agents.*`、桥 / print / ACP） | [`docs/multi-agent.md`](docs/multi-agent.md)、[`docs/phase2-acp.md`](docs/phase2-acp.md) |
| 安全模型与整改判据 | [`docs/security-hardening.md`](docs/security-hardening.md) |
| 反馈体系（P0–P3 设计） | [`docs/feedback/`](docs/feedback/README.md) |
| 关键决策记录（ADR 0001–0015） | [`docs/adr/`](docs/adr) |
| UI 真机验收（Playwright 三层） | [`docs/testing/ui-e2e.md`](docs/testing/ui-e2e.md)、[`docs/testing/ui-e2e-next.md`](docs/testing/ui-e2e-next.md)、[`docs/testing/ui-e2e-online.md`](docs/testing/ui-e2e-online.md) |
| UI 设计规范与落地 | [`docs/ui-design/UI-DESIGN-SPEC.md`](docs/ui-design/UI-DESIGN-SPEC.md)、[`docs/ui-design/IMPLEMENTATION-PLAN.md`](docs/ui-design/IMPLEMENTATION-PLAN.md) |
| 前端 V2 重构与迁移报告 | [`docs/frontend-v2/migration-report.md`](docs/frontend-v2/migration-report.md)（含已归档的设计原文 [`架构升级技术方案`](docs/frontend-v2/Pieqi%20Frontend%20V2%20架构升级技术方案.md)） |
| 版本需求流水线（S0–S5，2.2 版本） | [`docs/requirements/overview.md`](docs/requirements/overview.md) |
| 下一阶段规划（v2.1） | [`docs/v2.1/`](docs/v2.1/Pieqi%20迭代功能点清单%20v1.0.md) |

***

## 已知限制

* **qoder 等 ACP agent** 需本机安装对应 CLI（`qodercli` / `codex` 等）并支持 `--acp`；qoder 默认模型是付费档，Credits 耗尽会报 `error_code 118`（模板里已显式带 `-m Qwen3.8-Flash`）。

* **dsh（DeepSeek Harness）** 需 `npm install -g @deepseek-ai/dsh`（ACP 入口是它的 profile：`dsh --profile acp`），并在其 `acp` profile 里 pin 好模型路由。默认不注册；握手实测冷启动 >90s，故其 `init_timeout` 默认 2m。清空 pin 不会回落默认模型，会话会彻底没模型。

* **dsh 的 goal 不在 ACP 路径上**（走 SDK control request），pieqi 侧取不到；已在 Qoder/dsh 侧记录，结论见 [`docs/agents-goal-and-capabilities.md`](docs/agents-goal-and-capabilities.md)。

* **tunnel token 不持久化**：服务重启 / 隧道重建后需重新扫码获取（设计使然，避免 token 落盘）。

* **企微 / 微信渠道**为早期演示实现，默认关闭（`channels.wecom/wechat.enabled: false`），当前主推飞书。

* **print 回退路径**为整块文本输出（非逐字增量），且审批依赖 PreToolUse hook 子进程回连。

* **「Event Store / SQLite」尚未落地**：当前 `TaskEvent` 内嵌在 Task JSON 里，靠一任务一文件 + 原子重命名保证持久性。
