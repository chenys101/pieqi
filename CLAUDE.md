# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目是什么

Pieqi 把 Claude Code 等 coding agent 接入 IM（飞书/企微/微信）和移动端 PWA：在手机上发消息即可创建/续问/审批任务，实时看到逐字流式输出与工具调用。Go 单二进制（前端 `//go:embed` 嵌入），核心入口 `cmd/pieqi`。详细功能与目录结构见 `README.md`。

## 常用命令

```bash
# 后端
go build -o bin/pieqi ./cmd/pieqi        # 构建二进制
go run ./cmd/pieqi                        # 直接运行（读取同目录 config.yaml）
go test ./internal/...                    # 跑所有单元测试
go test ./internal/core -run TestFoo      # 跑单个测试
go test -tags integration ./internal/...  # 含集成测试（需要 claude CLI / 桥服务在跑）

# 前端（web/ 目录，Vite + Vue3 + Tailwind + Pinia）
cd web && npm install
npm run dev         # 开发服务器 :5174，/api、/internal 代理到 :3000
npm run build       # vue-tsc 类型检查 + vite build → web/dist
npm run typecheck   # 仅类型检查（vue-tsc --noEmit）
npm run test        # vitest 单测

# 发布打包
./build.sh [版本号]  # 前端构建 → 交叉编译 linux/windows → zip/tar.gz

# Node 服务自测（一次性验证脚本，非 CI；均在各自目录 npm install 后）
cd services/claude-sdk-bridge && npm test   # 桥全链路：spawn 桥 + 真实 Agent SDK 会话
cd services/visual-capture && npm test      # 截图/console/network 采集（缺 chromium 自动 SKIP）
```

环境变量：`PIEQI_CONFIG`（配置文件路径，默认 `config.yaml`）、`PIEQI_HOME`（运行时数据根，默认 `~/.pieqi`）、`PIEQI_` 前缀覆盖任意配置项（如 `PIEQI_SERVER_PORT`）。

**配置**：`config.yaml` 不入库（含个人域名/端口/凭据），模板是 `config.example.yaml`（全字段带注释）。首次运行 `cp config.example.yaml config.yaml`；给 `config.example.yaml` 增删字段时，`go test ./internal/config -run TestConfig_Example` 会校验模板仍可加载且不含凭据。

**坑**：前端改了之后必须 `npm run build` 再重编 Go，才会嵌入新前端。集成测试在 `//go:build integration` 三个文件里，默认 `go test ./internal/...` 不会跑到。

前端结构：`web/src` 按 feature 域切分（`features/` 下 agent/approval/dashboard/feedback/session/settings/task/timeline），`pages/` 薄路由页、`stores/`（Pinia）、`services/api` + `services/websocket`；Vite dev 端口 :5174，代理 `/api`、`/internal` 到 :3000。

## 架构（大图）

请求流：`渠道(channel) / HTTP(api) / WebSocket → internal/core → internal/agent → 真实 agent 进程`。

入口 `cmd/pieqi/main.go` 是双模式二进制：无参数启动主服务；`pieqi pre-tool-use` 作为 Claude Code PreToolUse hook 子进程运行，从 stdin 读 hook 输入 → 回连主进程 `/internal/hook` 等人类决策 → 输出 permissionDecision（print 路径审批闭环）。hook 通过往 worktree 写 `.claude/settings.json` 注入（`internal/core/hook_settings.go`），且 `pieqi.permission_mode` 默认 `bypassPermissions`——放行由 hook 真正拦截，不是 SDK 层。

### Agent 驱动三层

| 层 | 代码 | 职责 |
|---|---|---|
| 调度 | `internal/core/task_runner.go` | 任务状态机、worktree、事件发布、IM 回执通知、标题生成 |
| 会话管理 | `internal/agent/manager.go` + `session.go` | 按 taskID 管会话、每项目并发槽、传输降级回退 |
| 传输 | `internal/agent/claude/`（sdk-bridge / print）、`internal/agent/acp.go` | 具体 agent 协议 |

传输三选一（`agents.*` 配置）：
- **sdk-bridge（默认）**：常驻 Node 桥（`services/claude-sdk-bridge`，HTTP+SSE 封 Agent SDK），探活失败自动拉起，客户端 `internal/agent/claude/bridge/client.go`。
- **print 回退**：`claude -p --output-format stream-json` 逐事件解析。
- **ACP**：原生协议 JSON-RPC over stdio（`coder/acp-go-sdk`），用于 qoder/codex。

数据模型集中在 `internal/model/task.go`：`Task` 挂 `Events []TaskEvent`、`Baseline`、`CurrentDecision`、`Intervention`、`OriginChannel`。

### 任务生命周期与两条审批路径（关键）

`pending → running → waiting_input ⇄ running → completed | failed | cancelled`

`waiting_input` 分两种，恢复路径不同（`model.DecisionKind`）：

- **路径 A（approval）**：PreToolUse hook / ACP RequestPermission 触发，**claude 进程还活着**挂在 channel 上。恢复走 resolve → 同一进程继续。
- **路径 B（choice）**：Claude 文本提问（`[CHOICE]` 格式），claude 已 `end_turn` 退出（**进程已死**）。恢复走 `--resume -p <选项>` → 新进程。

这直接决定重启行为：`TaskStore.load()` 在启动时把 `running` 和等待 approval 的任务标记 `failed`（子进程已死），但等待 choice 的任务**保留** `waiting_input`，可隔天继续选。

### 持久化（没有 SQL 数据库）

- 一任务一文件 `~/.pieqi/tasks/<id>.json`，每次变更 `os.Rename` 原子写（`internal/core/task_store.go`）。内存 map 作索引。
- 文档里提到的「Event Store / SQLite」尚未落地，当前 `TaskEvent` 是内嵌在 Task JSON 里的切片，是反馈系统的事实源。
- 启动恢复逻辑在 `TaskStore.load()`。

### 事件模型

`EventBus`（`internal/core/event_bus.go`）是内存 fan-out，订阅者慢则丢弃积压。事件类型：`task_created | task_updated | task_deleted | task_delta`。`task_delta` 只带文本增量（真流式逐字），其余带完整 Task 副本。WS 层（`internal/api/ws.go`）订阅转发给 PWA。

### 反馈体系（Feedback 层，最近大头）

`internal/core/` 里 `feedback.go / git_diff.go / checkpoint.go / prospective.go / checks.go / evidence.go / outcome.go / preview.go / visual.go / push.go` 构成 Observe→Verify→Control→Intervene 闭环。**领域词汇必须用对**，否则会写出歧义代码，详见 `CONTEXT.md`（这是本仓库的术语表）。

关键事实源关系（对应 `docs/adr/`）：
- `TaskEvent` 是事实源，`FileChange` 从事件派生（ADR-0001）。
- Baseline 是只读 git HEAD 参照，绝不写用户分支；Checkpoint 只覆盖 Agent 改过的文件（ADR-0002）。
- Turn 以用户消息为界（ADR-0003），是变更分组 / Checkpoint / Rewind 的基准单位。
- Evidence→Continue 由**后端**把证据组装成上下文（ADR-0004）。
- Check 优先复用 Agent 已跑过的 test/lint/build 结果，而非重跑（ADR-0005）。
- 视觉反馈（Playwright 截图/console/network）走独立采集服务 `services/visual-capture`（ADR-0006）。
- 明确不做 Web IDE / Computer Use / 录屏等（ADR-0007）：查看文件属于 Feedback，编辑文件属于 IDE。产品边界扩张类需求先对照这份非目标清单。

这部分通过 `api.Server` 的 nil-safe 注入接线（`SetFeedback / SetCheckRunner / SetVisualCapture / SetPushRegistry`）：未接线时对应端点返回降级响应，main.go 按构建顺序逐层 wire 上去。

## 安全模型（`internal/auth`）

内网放行、外网仅 Cloudflared 隧道 + 内存 TTL token；飞书单账号绑定管理员；限流拉黑 + 审计。路由按「bootstrap path（/tunnel/start 发首个 token）vs mutation path（需已签发 token）」分层 gate，改动时勿破坏这条死锁规避。隧道有域名回收自愈（Cloudflare 周期性回收 trycloudflare 域名，健康巡检连续失败达阈值自动重启换域名并推送新链接给飞书管理员，PID 文件跨重启清理孤儿 cloudflared 进程）。

## 审批免审名单

`pieqi.auto_approve_tools`（默认 `[edit, delete, move]`）：ACP 路径下 ToolKind 命中名单的权限请求自动放行，不中断等人工审批（`TaskRunner.SetAutoApproveTools` → `internal/core/agent_perm.go`）。扩展时注意这是 ACP ToolKind 语义，不是工具名。

## 配置迁移注意

`config.go` 的 `Load()` 含弃用迁移：旧 `pieqi.acp.*` → 新 `agents.*`，用 `v.InConfig`（只认配置文件显式值，`v.IsSet` 会把 SetDefault 误报为已设置）判显式，命中即打告警并回填，语义不贸然删除。加新配置项时仿照此模式，不要静默改语义。