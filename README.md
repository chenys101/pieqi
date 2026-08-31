# Pieqi

把 **Claude Code 等 coding agent** 接入 IM（飞书 / 企微 / 微信）与移动端 PWA 的桥接服务：在手机上发消息即可创建/续问/审批 agent 任务，实时看到逐字流式输出与工具调用。

```
飞书(长连接/webhook) ─┐
企业微信 / 微信      ─┼─→ Pieqi(Go 单二进制) ─→ AgentManager ─→ 多种 agent 传输：
移动端 PWA (:3000)  ─┘        │                  ├─ claude：sdk-bridge（官方 Agent SDK 常驻桥，默认）
                               │                  ├─ claude：print 回退（claude -p stream-json）
                               │                  └─ qoder 等：原生 ACP（--acp，JSON-RPC over stdio）
                               ├── 任务调度：worktree 隔离 / 每项目并发上限 / 审批 / 续问
                               └── 安全：飞书单账号绑定 + Cloudflared 隧道 + token / 限流 / 审计
```

***

## 特性

* **多 Agent 统一驱动**：`AgentManager` + `AgentSession` 统一接口，按 `agents.*` 配置选择传输：

  * `claude` → `sdk-bridge`（默认）：常驻 Node 桥服务（`services/claude-sdk-bridge`，:18790）封装官方 Claude Agent SDK，探活失败自动拉起；桥不可用时透明回退 `print`（`claude -p --output-format stream-json`）。

  * `qoder` 等 → 原生 ACP（`qodercli --acp`，`coder/acp-go-sdk`）。

* **真流式输出**：内容增量（含思考过程）→ EventBus → WebSocket → PWA 逐字追加渲染。

* **协议级审批**：agent 请求敏感工具时任务进入 `waiting_input`，IM / PWA 弹出审批卡片，Approve / Deny（AllowOnce / AllowAlways）直达 agent。

* **任务并行**：任务可在任意本地路径运行，或建 git worktree 隔离并行（任务完成自动清理）；每项目并发上限控制。

* **跨渠道会话共享**：身份映射把各渠道用户 ID 归一为统一身份，同一用户换渠道可续接上下文。

* **飞书接入极简化**：

  * 长连接模式（`event_mode: longconn`）WSS 订阅事件，**无需公网回调**；

  * Device Flow 扫码**一键创建飞书自建应用**（`/api/larkreg`），凭据落盘并热生效。

* **外网安全访问**：飞书单账号绑定（唯一管理员 OpenID）+ Cloudflared 临时隧道（trycloudflare URL + 内存 TTL token）+ IP 失败限流拉黑 + 审计日志。

* **移动端 PWA**：任务列表（按项目分组）、事件流、干预抽屉（审批 / 追加续问 / Skill 胶囊 `/` 自动补全）、隧道控制面板、`/session/<id>` 深链接。

* **单二进制部署**：前端 `//go:embed` 嵌入，前端源码与桥服务随发布包同装。

***

## 架构

### Agent 驱动链路（三层）

| 层    | 代码                                                                        | 职责                           |
| ---- | ------------------------------------------------------------------------- | ---------------------------- |
| 调度   | `internal/core/task_runner.go`                                            | 状态机、worktree、事件发布、IM 通知、标题生成 |
| 会话管理 | `internal/agent/manager.go` + `session.go`                                | 按 taskID 管理会话、项目级并发槽、透明回退    |
| 传输   | `internal/agent/claude/`（sdk-bridge / print）、`internal/agent/acp.go`（ACP） | 具体 agent 协议实现                |

* **sdk-bridge**：`services/claude-sdk-bridge`（Node，私有 HTTP+SSE：`/v1/sessions` / `/prompt` / `/events`），封装 Claude Agent SDK 的多轮会话、权限回调、取消；Go 侧客户端见 `internal/agent/claude/bridge/client.go`，`auto_start: true` 时探活失败自动 spawn。

* **print 回退**：`claude -p --session-id/--resume` + `stream-json` 逐事件解析（`internal/agent/print*.go`）。

* **ACP**：`AgentClientProtocol` JSON-RPC over stdio，`RequestPermission` / `session/resume` 为协议级能力（`internal/agent/acp.go`）。

> 旧配置 `pieqi.acp.*` 已弃用（P5 迁移），显式配置会触发告警；请使用 `agents.*` 节。详见 `docs/multi-agent.md`。

### 任务生命周期

`pending → running → waiting_input(approval) → running → completed / failed / cancelled`

* 事件类型：文本增量 / 思考增量 / 工具调用 / 工具结果 / 错误 / 决策等，全量经 EventBus 推送 WS。

* 已结束任务可 `intervene(append_prompt)` 续问，复用会话上下文。

### 安全模型（`internal/auth`）

* **内网**：直接放行（debug 模式全放行）。

* **外网**：仅经 Cloudflared 隧道 + 有效 tunnel token 可访问；token 内存存储、不持久化（重启 / 换隧道即失效）。

* **管理员绑定**：首个飞书用户经 `/api/auth/bind`（仅内网）绑定为唯一 admin，之后可在飞书移动端管理隧道。

* **限流**：认证失败 5 次/分钟 → 拉黑 10 分钟；所有外部请求记审计日志。

***

## 目录结构

```
pieqi/
├── cmd/pieqi/                 # 入口：主服务 + pre-tool-use hook 子命令 + PWA 静态托管
├── internal/
│   ├── agent/                 # AgentSession/Manager 抽象；acp.go / print*.go / provider.go
│   │   └── claude/            # claude 专属：bridge/(sdk-bridge 客户端)、proc、session
│   ├── api/                   # HTTP API：tasks / skills / ws / auth / tunnel / larkreg / hook
│   ├── auth/                  # 绑定、tunnel token、Cloudflared 管理、限流、审计、中间件
│   ├── channel/               # 渠道适配：lark（webhook + 长连接）/ wechat / receiver / sender
│   ├── config/                # viper 配置加载 + 弃用迁移
│   ├── core/                  # TaskRunner / TaskStore / EventBus / Hook / Worktree / 扫描器
│   ├── larkreg/               # 飞书 Device Flow 一键注册自建应用
│   └── model/                 # Task / TaskEvent / Message 等数据结构
├── services/claude-sdk-bridge/ # Node 常驻桥：Claude Agent SDK HTTP+SSE 封装
├── web/                       # PWA 前端（Vite），embed.go 嵌入二进制
├── docs/                      # 设计文档与规划（见下文文档索引）
├── build.sh                   # 构建 + 发布包脚本
└── config.yaml
```

运行时数据在 `~/.pieqi/`（可用环境变量 `PIEQI_HOME` 覆盖）：`tasks/`、`worktrees/`、`sessions/`、`feishu_binding.json`、`lark_credentials.json` 等，不入仓库。

***

## 快速开始

### 后端

```bash
# 构建二进制（产物在 bin/）
mkdir -p bin && go build -o bin/pieqi ./cmd/pieqi

# 或直接运行
go run ./cmd/pieqi

# 测试
go test ./internal/...
```

### 前端

```bash
cd web
npm install
npm run build        # 产出 web/dist，由 go:embed 打进二进制
npm run dev          # 开发服务器（:5174，/api、/internal 代理到 :3000）
```

> 前端改动后需 `npm run build` 再重编译 Go，才会嵌入新前端。

### 飞书接入（两种方式）

1. **一键注册（推荐）**：本机打开 PWA → 设置 → 添加渠道 → 扫码，自动创建飞书自建应用并写入凭据。
2. **手动配置**：在[飞书开放平台](https://open.feishu.cn)创建自建应用，填 `config.yaml` 的 `channels.lark`（长连接只需 `app_id` + `app_secret`；webhook 模式还需 `verify_token` + `encrypt_key`）。

### 关键配置（`config.yaml`）

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

pieqi:
  max_concurrent_per_project: 4
  base_branch: master
  hook_timeout: 30m        # 审批等待上限（print 回退路径）
  hook_tools: [Bash, Write, Edit, NotebookEdit]

auth:
  cloudflared:
    default_ttl: 15m       # 隧道 token 有效期：15m | 1h | 4h
  ratelimit:
    max_failures_per_min: 5
```

***

## HTTP API

### 任务（需内网 / tunnel token）

| 方法           | 路径                              | 说明                                         |
| ------------ | ------------------------------- | ------------------------------------------ |
| GET / POST   | `/api/tasks`                    | 任务列表（按项目分组）/ 新建（`project_path` + `prompt`） |
| GET / DELETE | `/api/tasks/:id`                | 任务详情 / 删除                                  |
| POST         | `/api/tasks/:id/intervene`      | 审批决策 / 追加续问（append\_prompt）                |
| POST         | `/api/tasks/:id/cancel`         | 取消任务                                       |
| GET          | `/api/skills` · `/api/commands` | Skill / 自定义命令补全数据源                         |
| GET          | `/api/ws`                       | WebSocket 实时事件推送                           |

### 认证与隧道

| 方法            | 路径                                       | 访问策略          | 说明                           |
| ------------- | ---------------------------------------- | ------------- | ---------------------------- |
| POST / DELETE | `/api/auth/bind`                         | 仅内网           | 绑定 / 解绑管理员飞书身份               |
| GET           | `/api/auth/status`                       | 公开            | 绑定状态（前端启动轮询）                 |
| POST          | `/api/tunnel/start`                      | 飞书移动端 + 外网    | 启动 Cloudflared 隧道并签发首个 token |
| POST          | `/api/tunnel/stop` · `/reset` · `/renew` | token + 飞书移动端 | 停止 / 重置 / 续期                 |
| GET           | `/api/tunnel/status` · `/qrcode`         | 公开（token 脱敏）  | 隧道状态 / 二维码                   |

### 飞书一键注册（仅内网）

| 方法         | 路径                              | 说明                      |
| ---------- | ------------------------------- | ----------------------- |
| POST       | `/api/larkreg/start`            | 启动 Device Flow，返回扫码 URL |
| GET        | `/api/larkreg/poll` · `/status` | 轮询注册进度 / 状态             |
| GET / POST | `/api/larkreg/config`           | 读取 / 手动写入渠道配置           |

### 其他

| 方法   | 路径               | 说明                                      |
| ---- | ---------------- | --------------------------------------- |
| GET  | `/health`        | 健康检查                                    |
| POST | `/internal/hook` | pre-tool-use hook 子进程回连（仅本地，print 路径审批） |

***

## 发布 / 打包

```bash
./build.sh    # 前端构建 → Go 编译 → 二进制 + config.yaml + README + 启动脚本 打 zip/tar.gz

# 手动交叉编译
GOOS=linux   GOARCH=amd64 go build -o build/pieqi-linux-amd64 ./cmd/pieqi
GOOS=windows GOARCH=amd64 go build -o build/pieqi-windows-amd64.exe ./cmd/pieqi
```

* Windows：双击 `start.bat`（`restart.bat` 重启）。

* Linux：systemd 托管。

* 运行依赖：`claude` CLI（claude 登录态）、Node.js（桥服务 / TS 适配器）、`cloudflared`（需要外网访问时）。

***

## 文档索引

* `docs/multi-agent.md` — 多 Agent 架构设计（`agents.*` 配置、P0–P5 演进）

* `docs/phase2-acp.md` — Phase 2：`claude -p` → ACP 迁移规划

* `docs/multi-agent-evaluation.md` / `docs/comm-arch-upgrade-evaluation.md` — 方案评估

* `docs/superpowers/plans/` — 飞书 IM 绑定 + cloudflared 隧道 / 飞书长连接与 Device Flow 实施计划

* `docs/spikes/claude-sdk-bridge/` — Claude Agent SDK 桥接技术验证

* `.trae/specs/migrate-to-acp-protocol/` — ACP 迁移 spec / tasks / checklist

***

## 已知限制

* **qoder 等 ACP agent** 需本机安装对应 CLI（`qodercli` / `codex` 等）并支持 `--acp`。

* **tunnel token 不持久化**：服务重启 / 隧道重建后需重新扫码获取（设计使然，避免 token 落盘）。

* **企微 / 微信渠道**为早期演示实现，默认关闭（`channels.wecom/wechat.enabled: false`），当前主推飞书。

* **print 回退路径**为整块文本输出（非逐字增量），且审批依赖 PreToolUse hook 子进程回连。

