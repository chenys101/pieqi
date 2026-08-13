# Pieqi

多渠道（飞书 / 企业微信 / 微信）到 **Claude Code CLI** 的桥接服务，核心特性是**跨渠道会话共享**：同一用户从飞书发起的对话，切到微信能无缝继续。

当前为 **Pieqi 后端**形态：通过 worktree 并行运行开发任务，并提供一个**移动端监控 PWA** 来新建/查看/干预任务。

```
飞书 / 企微 / 微信 ──→ Pieqi ──→ AgentAdapter ──→ ACP (claude-code / qodercli / codex ...)
                      │              └─ 回退：claude -p --session-id / --resume
                      ├── 跨渠道统一会话
                      ├── Pieqi 后端（worktree 并行任务）
                      └── 移动端监控 PWA（:3000）
```

---

## 特性

- **跨渠道会话共享**：身份映射（`data/users.json`）把各渠道用户 ID 归一为统一身份，会话按 `user:<identity>:<session>` 共享上下文。
- **Pieqi 后端**：任务可在任意本地路径直接运行（也可建 worktree 隔离并行），每项目并发上限控制；`bypassPermissions` + PreToolUse hook 实现人类审批。
- **多 Agent 支持（Phase 2）**：经 ACP（Agent Client Protocol）统一驱动 claude-code / qodercli / codex 等任意 ACP agent；真流式（内容增量逐字）、协议级权限审批（RequestPermission → IM/PWA 卡片 → Approve/Deny）；ACP 适配器不可用时透明回退 `claude -p`。
- **移动端 PWA**：新建任务、查看事件流、补充续问、工具调用折叠、URL 会话路由（`/session/<id>` 深链接）。
- **会话恢复健壮化**：自动捕获 claude 真实 session_id 供续问；会话丢失时回退新会话，保证消息必被提交。
- **单二进制部署**：前端经 `//go:embed` 嵌入，`bridge.exe` 单文件自包含。

---

## Phase 2：ACP 多 Agent

经 [Agent Client Protocol](https://agentclientprotocol.com/)（ACP）统一驱动任意 coding agent，突破 Phase 1 `claude -p` 的结构性限制（非真流式 / 审批靠 hook hack / 绑死单一 CLI / 会话续问脆弱）。

- **真流式**：ACP `SessionUpdate` 内容增量 → EventBus → WS → 前端逐字追加（替换 Phase 1 整块 text）。
- **协议级审批**：ACP `RequestPermission` → task `waiting_input(approval)` → IM/PWA 卡片 → Approve/Deny（替换 `bypassPermissions` + PreToolUse hook）。
- **多 Agent**：`AgentAdapter` 接口抽象，`ACPAgent`（JSON-RPC over stdio）+ `PrintAgent`（claude -p 回退）双实现；`AgentManager` 统一调度多会话/并发/透明回退。
- **会话持久化**：ACP `session/load` / `session/resume` 复用上下文（替换脆弱的 `--session-id` / `--resume` 匹配）；会话丢失由协议层 surface，不静默失败。

支持的 agent：
| Agent | 接入方式 |
|-------|---------|
| claude-code | 经官方 TS 适配器 `npx -y @agentclientprotocol/claude-agent-acp@latest` |
| qodercli | 原生 `qodercli --acp` |
| codex | 原生 `codex --acp` |
| 其他 | `<agent> --acp` 兜底 |

配置见 `config.yaml` 的 `pieqi.acp` 段。规划细节见 `docs/phase2-acp.md`。

---

## 技术栈

| 维度 | 说明 |
|------|------|
| 语言 | Go |
| Web | gin |
| 配置 | viper（YAML） |
| 日志 | zap |
| Agent 驱动 | Phase 2：ACP（`github.com/coder/acp-go-sdk`，JSON-RPC over stdio）驱动 claude-code（经官方 TS 适配器）/ qodercli / codex 等；回退 Phase 1 `claude -p --session-id` / `--resume` + `--output-format stream-json` 逐事件解析 |
| 前端 | Vite 构建，嵌入二进制 |
| 存储 | 文件系统 JSON（`data/`） |

---

## 目录结构

```
pieqi/
├── cmd/bridge/main.go        # 服务入口（HTTP + PWA + 渠道 + pre-tool-use hook 子命令）
├── web/
│   ├── src/                  # PWA 前端源码（main.js / styles.css）
│   ├── public/sw.js          # service worker（vite 拷入 dist 正确下发）
│   ├── index.html
│   ├── embed.go              # //go:embed 嵌入 web/dist
│   └── vite.config.js
├── internal/
│   ├── agent/                # Phase 2：AgentAdapter 接口 + ACPAgent / PrintAgent / AgentManager
│   ├── api/                  # 任务/技能/命令/WS/hook HTTP API
│   ├── config/config.go      # 配置加载
│   ├── model/                # 数据结构（Task / TaskEvent / Decision 等）
│   ├── core/                 # 调度：TaskRunner / TaskStore / EventBus / HookService / Worktree / 流解析 / ACP wire 连接器
│   └── channel/              # 渠道适配器（lark / wecom / wechat）
├── data/                     # 运行时数据（tasks / worktrees / sessions / mappings / users.json）
├── docs/phase2-acp.md        # Phase 2 ACP 迁移规划
├── config.yaml
└── README.md
```

---

## 快速开始

### 开发运行

```bash
# 构建二进制（cmd/bridge 入口）
go build -o bridge ./cmd/bridge

# 或直接运行
go run ./cmd/bridge

# 测试
go test ./internal/...
```

### 前端

```bash
cd web
npm install
npm run build        # 产出 web/dist，由 go:embed 打进二进制
npm run dev          # 开发服务器（:5174，代理到 :3000）
```

> 前端改动后需 `npm run build` 再重编译 Go，才会把新前端嵌入二进制。

### 配置

`config.yaml`（viper 加载）：

```yaml
server:
  port: 3000
claude:
  work_dir: G:/workspace
  model: deepseek-v4-pro-202606   # claude -p 使用的模型
  effort: high
  timeout: 300s
pieqi:
  enabled: true
  worktree_base: ./data/worktrees
  permission_mode: bypassPermissions
  hook_tools: [Bash, Write, Edit, NotebookEdit]
  max_concurrent_per_project: 4
  base_branch: master
```

`data/users.json`：渠道身份 → 统一身份映射。

---

## HTTP API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/health` | 健康检查 |
| GET | `/api/tasks` | 任务列表（按项目分组） |
| POST | `/api/tasks` | 新建任务（`project_path` + `prompt`） |
| GET | `/api/tasks/:id` | 任务详情 |
| POST | `/api/tasks/:id/intervene` | 审批决策 / 追加续问（append_prompt） |
| POST | `/api/tasks/:id/cancel` | 取消任务 |
| DELETE | `/api/tasks/:id` | 删除任务 |
| GET | `/api/skills` / `/api/commands` | 补全数据源 |
| GET | `/api/ws` | WebSocket 事件推送（任务/事件实时） |
| POST | `/internal/hook` | PreToolUse hook 回调（审批） |

---

## 发布 / 打包

```bash
# Linux amd64
GOOS=linux GOARCH=amd64 go build -o build/bridge-linux-amd64 ./cmd/bridge

# Windows amd64
go build -o build/bridge-windows-amd64.exe ./cmd/bridge

# 发布包：bridge + config.yaml + README + 启动脚本
# （见构建脚本，产物打 zip）
```

启动：Windows 双击 `start.bat` / 运行 `bridge.exe`；Linux 用 systemd 托管。

---

## 已知限制（第一阶段）

- **事件级流式而非逐字流式**：claude 把回答作为一个完整 text 块输出，界面上"思考过程可见 + 回答整段出现"。真逐字流式依赖代理吐出 partial delta，见 `stream_event.go` 的"形态二"预留。
- **依赖 `claude -p` + 代理**：`-p` 模式对复杂多工具运行，部分代理（如本地 15721 中转）会吞掉中间事件，导致任务"无响应"。见 `docs` 中第二阶段规划（改 ACP 协议）。

---

## 文档

- `CONTEXT.md` — 领域词汇表（身份/会话/审批/桥接）
- `docs/adr/` — 架构决策记录（如有）
