# AGENTS.md

本文件是给 AI coding agent 的**操作规程**；人类产品说明看 `README.md`；**术语以 [`docs/CONTEXT.md`](docs/CONTEXT.md) 为准**（不要在本文件重复定义领域词）。

改代码前至少扫一遍「硬规则速查」；改到对应域再读深节与外链。

## 硬规则速查

1. **术语**用 `docs/CONTEXT.md`；冲突决策写新 ADR，勿静默推翻。
2. **前端改完**必须先 `npm run build` 再编 Go（`//go:embed`）。
3. **构建落点**只写 `build/pieqi.new.exe`，绝不直接写 `pieqi.exe`；运行位在 `~/.pieqi\bin\`，安装用 **copy/cp** 不用 move。
4. **`GOTMPDIR`** 必须在工作区内（`build\.gotmp`）；工作区外（含 `%TEMP%`）编不过。见 ADR-0016。
5. **PowerShell** 禁止把 `$home`/`$HOME` 当自定义变量；禁止对目录通配 `Remove-Item * -Recurse -Force`（会清空用户目录）。
6. **鉴权**：`X-Forwarded-For` 只取**末段**；勿改回首跳（commit 25aea94）。
7. **删除 = L3**，永远人工审；工作区外写、`git push`、改基线同属硬边界（ADR-0015）。
8. **`waiting_input`**：`approval`（进程活）≠ `choice`（进程死）；启动恢复必须维持区分（见 CONTEXT `DecisionKind`）。
9. **提交**：禁止 `git add -A` / `git add .`；`git push` / `git reset` 及同族破坏性操作须先经用户批准。
10. **先提交，再编译，再重启**（宿主即 pieqi，重启打断自己）。
11. **UI**：jsdom 绿灯 ≠ 验收；尺寸/遮挡/滚动必须真机量数字（`web/e2e`，`docs/testing/ui-e2e.md`）。
12. **Preview 已删子路径**返回 410（勿改 404）；`/file` 的 html/svg 必须 CSP sandbox（CONTEXT「产物」）。
13. **文件清单默认全展开**（不是收起；ADR-0019）。AGENTS 旧文曾写收起，以 CONTEXT / 代码为准。

## 常用命令

```bash
# 后端（module 名 pieqi，Go 1.25）
go build -o build/pieqi.new.exe ./cmd/pieqi
go run ./cmd/pieqi
go test ./internal/...
go test ./internal/core -run TestFoo
go test -tags integration ./internal/...   # 需 claude CLI / 桥在跑；默认套件不含

# 前端（web/：Vite + Vue3 + Tailwind + Pinia）
cd web && npm install
npm run dev          # :5174 → 代理 :3000
npm run build        # → web/dist（嵌入前必跑）
npm run typecheck
npm run test         # vitest；真机 e2e：npm run test:e2e / test:e2e:impact

# 日常发版（Windows）
powershell -NoProfile -ExecutionPolicy Bypass -File build\preflight.ps1   # 0=已最新，10=需发
powershell -NoProfile -ExecutionPolicy Bypass -File build\release.ps1     # 构建→投递→自重启
# -NoDeploy 只构建不发布

# 发布打包（交叉编译产物，不产出 pieqi.new.exe）
bash build/build.sh [版本号]

# 桥自测
cd services/claude-sdk-bridge && npm test
```

环境变量：`PIEQI_CONFIG`（默认 `config.yaml`）、`PIEQI_HOME`（默认 `~/.pieqi`）、`PIEQI_` 前缀覆盖配置项。

**配置**：`config.yaml` 不入库；模板 `config.example.yaml`。增删模板字段必须跑 `go test ./internal/config -run TestConfig_Example`。

## 架构要点

请求流：`渠道 / HTTP / WebSocket → internal/core → internal/agent → 真实 agent 进程`。产品大图与特性见 `README.md`。

| 层 | 代码 | 职责 |
|---|---|---|
| 调度 | `internal/core/task_runner.go` | 状态机、worktree、事件、IM、标题 |
| 会话 | `internal/agent/manager.go` + `session.go` | 按 taskID、并发槽、传输降级 |
| 传输 | `internal/agent/claude/`、`internal/agent/acp.go` | sdk-bridge / print / ACP |

- 新 ACP agent：**只改** `internal/agent/manifest.go`；启停排序在设置页（`agents.enabled`）；机器路径用 `spawn_overrides`。不要把常量再写回 `config.yaml`（`TestConfig_ExampleHasNoACPAgentSections`）。
- ACP 模型通道：标准 `configOptions` + `session/set_config_option` **优先**；`_meta` 仅兜底。判据与坑见 `internal/agent/model_catalog.go`、ADR-0011。
- 持久化：SQLite `~/.pieqi/tasks/pieqi.db`（ADR-0022）；术语见 CONTEXT「Store / SQLite」。
- Feedback / 产物 / Preview：术语与边界见 CONTEXT；设计见 `docs/feedback/` 与 ADR-0001～0007、0017～0019。

入口 `cmd/pieqi` 双模式：主服务；`pieqi pre-tool-use` 为 Claude print 路径 hook（`.claude/settings.json` 注入）。`pieqi.permission_mode` 默认 `bypassPermissions`——放行由 hook 拦截。

## 任务与审批

状态机：`pending → running → waiting_input ⇄ running → completed | failed | cancelled`。

`waiting_input` 两种（CONTEXT `DecisionKind`）：

- **approval**：进程仍活 → resolve 续跑。
- **choice**：进程已死 → `--resume` 新进程。

`TaskStore.load()`：running 与 approval 孤儿 → `failed`；choice 保留。

### 免审与风险

运行期免审来自**设置页**（`auto_approve_l0` / `auto_approve_l1`），不是 `config.yaml`。`riskLevelKinds` 是卡片显示与放行的唯一表；空 kind → L2。

- ACP：取 `ToolCall.Kind`；只读 shell 可经 `readonly_kind.go` 降级（ADR-0008）。
- sdk-bridge：`TOOL_KINDS` 映射必须落在已知 kind（漏了 = 全人工审）。

降噪四层（ADR-0015）：只读白名单 / 命令族分档 / 原文扫描 / 非 shell 参数证明。硬边界见速查第 7 条。

### 审批幂等（必守）

重复 approve 不得报「no pending」误导用户：

1. `PermissionWire.settled` 记住已决 reqID；`Resolve` 区分 Missing / Approved / Denied（后两者幂等成功）。
2. `clearStaleDecision` 摘掉已不可能应答的 approval 卡（choice 不动）。
3. 前端：提交前 `refreshTask` 现取 id；已 settled 改投 `approve_same`。

回归：`agent_perm_test.go`、`stale_decision_test.go`、`web/src/stores/approval.spec.ts`。

未弹卡却被软完成的产品方案：`docs/notes/approval-miss-and-soft-complete.md`（未立项前勿改 `completeTask` 判据）。

## 安全（`internal/auth`）

完整方案：`docs/security-hardening.md`。未授权访问 ≈ 本机任意代码执行。

- 有域名 → Cloudflare Access JWT；无域名 → 外链 `?token=`（内存 TTL）。
- 客户端 IP：`CF-Connecting-IP`，否则 `X-Forwarded-For` **末段**。
- 路由分 bootstrap（`/tunnel/start`）与 mutation；勿破坏分层。
- 飞书管理员绑定 + 失败限流 + 审计；隧道健康巡检与域名回收自愈。

## 配置迁移

`config.Load()` 用 `v.InConfig` 做弃用告警（勿用 `IsSet`，会被 default 误伤）。

ACP agent **不再由配置声明**（2026-10-22）：代码事实在 `manifest.go`；唯一保留的配置节是 `agents.claude`（桥端口/token/二进制/默认模型）。加项时显式告警，勿静默改语义。

## 发版

深潜（Low 标签、落点判据、废弃路径）：[`docs/notes/windows-low-integrity-and-release-layout.md`](docs/notes/windows-low-integrity-and-release-layout.md)。技能 `pieqi-rebuild-restart` / `long-running-service-rebuild` 为可选参考。

### 日常

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File build\preflight.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File build\release.ps1
```

脚本已设 `GOTMPDIR=build\.gotmp`。`POST /api/admin/restart` **不会返回**；用 `GET /api/admin/restart` 看 `staged` 是否回到 `false`，不要重发 POST。

手工等价：前端 `npm run build` → `GOTMPDIR=... go build -o build/pieqi.new.exe ./cmd/pieqi` → copy 到 `%USERPROFILE%\.pieqi\bin\pieqi.new.exe` → `POST /api/admin/restart`。

| 入口 | 有 `build/pieqi.new.exe`（非零） | 无 |
|---|---|---|
| `restart.bat` / `POST /api/admin/restart` | swap-and-restart | restart-only（不动文件） |
| `POST /api/admin/restart-only` | 仍只重启 | 只重启 |

### 三条纪律

1. 先提交，再编译出 `.new`，再重启。
2. 改了 `internal/core/selfrestart*.go` 时，请用户**资源管理器双击** `restart.bat`（会话内自重启装载不了这次修复）。
3. 重启前看在途：`GET /api/tasks`（或面板）筛 `running` / `waiting_input`。任务在 SQLite，**不要**再 `grep ~/.pieqi/tasks/*.json`。

### 验证

```bash
# 端口在听；任务 API 200；staged 已消费
curl --noproxy '*' -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:3000/api/tasks
curl --noproxy '*' -s http://127.0.0.1:3000/api/admin/restart   # staged=false
```

判版本：日志 `main.go:NNN` 与当前源码同一句是否对齐。统计日志先排除 `acp permission` 行（工具回显会污染 grep）。

**绝不要跑仓库内的 exe 当服务**（Low 完整性 ⇒ 写不了 `~/.pieqi`）。症状：:3000 在听但日志停更。详见上面 notes。

## 协作约定

- **PowerShell `$home` 陷阱**：见速查第 5 条。隔离根用 `$PieqiHome` / `$IsoHome` / `$DataRoot`；删除只用 `-LiteralPath` 单文件。
- **UI 验收**：真机 + 基线数字；隔离端口（如 `PIEQI_SERVER_PORT=3100` + 独立 `PIEQI_HOME`）；提交在重启前。`cd web && npm run test:e2e:impact`。改 `tailwind.config.ts` / 夹具后重启 dev server。
- **沙箱**：不要用改落点/加白名单/去沙箱外跑来绕开；正解是提权申请。命令保持正常工程形态。`DSH_PERMISSION_MODE=danger-full-access` 禁止（会把 approval 打成 never）。单次提权 ≠ 改该环境变量。判构建成败**只认产物**非零，别信串了的 `$LASTEXITCODE`。
- **缓存**：持久缓存用系统默认（GOCACHE 等），勿塞进工作区；`build/` 只放交付物与跟踪脚本。
- **Commit**：conventional commits + 中文；正文写为什么。`git add -- <路径…>`；commit 前看 `git diff --cached --name-only`。
- **并行**：推荐 `git worktree` 隔离开发；运行期 worktree（任务 cwd）与开发 worktree 是两件事（`docs/notes/worktree-in-project-sandbox.md`）。
- **Git 须用户批准**：`push`、`reset`，以及 `clean -f`、`checkout --`/`restore` 丢改动、`branch -D`、`push --force`、对已推送历史的 `amend`/`rebase`。判据是会否丢掉/改写别人可见的东西。
- **运行时数据**在 `~/.pieqi/`，不入仓。没有 pieqi 自己的 `sessions/` 目录（会话实体在外部 agent）。
- **依赖**：`claude` CLI、Node（桥）、可选 `cloudflared`；ACP CLI 按设置页启停。dsh 入口是 `dsh --profile acp`（不是 `--acp`），模型 pin 在 dsh 侧。

## 改域索引

| 要改… | 先读 |
|---|---|
| 术语 / Feedback 边界 | `docs/CONTEXT.md`、`docs/adr/0001`～`0007` |
| 静态 Preview / 产物 / 文件查看 | ADR-0017、0018、0019 |
| 审批降噪 / 只读降级 | ADR-0008、0015；`readonly_kind.go` |
| 自重启 / 交接 | ADR-0009；`docs/notes/windows-low-integrity-and-release-layout.md` |
| GOTMPDIR / 沙箱构建 | ADR-0016 |
| Store / Requirement 同库 | ADR-0022、0021 |
| 安全鉴权 | `docs/security-hardening.md` |
| UI 真机 | `docs/testing/ui-e2e.md` |
| Plan 批准卡 / 软完成 | `.cursor/rules`（本地）、`docs/notes/approval-miss-and-soft-complete.md` |
