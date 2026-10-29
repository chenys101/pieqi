# AGENTS.md

本文件是给 AI coding agent 的仓库协作指南；人类阅读看 `README.md`。改代码前先读完这份，术语一律以 `CONTEXT.md` 为准。

## 项目是什么

Pieqi 把 Claude Code / qoder 等 coding agent 接入 IM（飞书/企微/微信）和移动端 PWA：手机上发消息即可创建任务、续问、审批工具调用，实时看逐字流式输出。Go 单二进制（前端 `//go:embed` 嵌入，`web/embed.go`），核心入口 `cmd/pieqi`。

## 常用命令

```bash
# 后端（module 名 pieqi，Go 1.25）
go build -o build/pieqi.new.exe ./cmd/pieqi  # 构建二进制（**只写 .new**，见「发版流程」）
go run ./cmd/pieqi                        # 直接运行（读同目录 config.yaml）
go test ./internal/...                    # 全部单元测试
go test ./internal/core -run TestFoo      # 单个测试
go test -tags integration ./internal/...  # 集成测试（需要 claude CLI / 桥服务在跑）

# 前端（web/：Vite + Vue3 + Tailwind + Pinia）
cd web && npm install
npm run dev         # :5174，/api、/internal 代理到 :3000
npm run build       # vue-tsc -b && vite build → web/dist
npm run typecheck   # 仅 vue-tsc --noEmit
npm run test        # vitest run（另有 npm run test:e2e = playwright）

# 发布打包
bash build/build.sh [版本号]  # 前端构建 → linux/windows 交叉编译 → zip/tar.gz（产物在 build/release/）

# Node 服务自测（各自目录先 npm install）
cd services/claude-sdk-bridge && npm test   # 桥全链路：spawn 桥 + 真实 Agent SDK 会话
```

环境变量：`PIEQI_CONFIG`（配置路径，默认 `config.yaml`）、`PIEQI_HOME`（运行时数据根，默认 `~/.pieqi`）、`PIEQI_` 前缀覆盖任意配置项（如 `PIEQI_SERVER_PORT`）。

**配置**：`config.yaml` 不入库（含个人域名/端口/凭据），模板是 `config.example.yaml`（全字段带注释）。首次运行 `cp config.example.yaml config.yaml`。**给 `config.example.yaml` 增删字段时**必须跑 `go test ./internal/config -run TestConfig_Example`——这组测试校验模板可加载、不含凭据、qoder spawn 是裸名、dsh 保持默认不注册、Access 段配置安全。

**两个必知的坑**：
1. 前端改动后必须先 `npm run build` 再重编 Go，否则二进制里还是旧前端。
2. 集成测试在 `//go:build integration` 的三个文件里（`internal/agent/claude/integration_test.go`、`resume_integration_test.go`、`internal/core/task_runner_session_integration_test.go`），默认 `go test ./internal/...` 不会跑到。

## 架构（大图）

请求流：`渠道(channel) / HTTP(api) / WebSocket → internal/core → internal/agent → 真实 agent 进程`。

入口 `cmd/pieqi/main.go` 是双模式二进制：无参数启动主服务；`pieqi pre-tool-use` 作为 Claude Code PreToolUse hook 子进程运行，从 stdin 读 hook 输入 → 回连主进程 `/internal/hook` 等人类决策 → 输出 permissionDecision（print 路径的审批闭环）。hook 通过往 worktree 写 `.claude/settings.json` 注入（`internal/core/hook_settings.go`）；`pieqi.permission_mode` 默认 `bypassPermissions`——放行由 hook 真正拦截，不是 SDK 层。

### Agent 驱动三层

| 层 | 代码 | 职责 |
|---|---|---|
| 调度 | `internal/core/task_runner.go` | 任务状态机、worktree、事件发布、IM 回执、标题生成 |
| 会话管理 | `internal/agent/manager.go` + `session.go` | 按 taskID 管会话、每项目并发槽、传输降级回退 |
| 传输 | `internal/agent/claude/`（sdk-bridge / print）、`internal/agent/acp.go` | 具体 agent 协议 |

传输三选一：
- **sdk-bridge（默认）**：常驻 Node 桥 `services/claude-sdk-bridge`（HTTP+SSE 封 Agent SDK），探活失败自动拉起，Go 客户端 `internal/agent/claude/bridge/client.go`。
- **print 回退**：`claude -p --output-format stream-json` 逐事件解析。
- **ACP**：JSON-RPC over stdio（`coder/acp-go-sdk`），用于 qoder（`qodercli --acp`）、dsh（`dsh --profile acp`）/codex。
  **★ 2026-10-22 起：接入一个新 ACP agent 只改一处 —— `internal/agent/manifest.go` 加一条。**
  不要再往 `config.AgentsConfig` 加节、不要加 `AgentXxxConfig` 类型、不要改 `ACPProviderConfigFromAgents`
  （它已改名为 `ACPProviderConfigFromRuntime`）。原因：`transport` / `agent_type` / 默认 spawn /
  超时 / 能力声明都是**代码事实**，写进配置文件只会形成第二份真相（例如 `agent_type: codebuddy`
  的值永远是 `codebuddy`）。用户侧只剩两件事可配，且都在**运行期**（`settings.json` 的 `agents` 段，
  可热改）：开不开（`enabled`）、装在哪（`spawn_overrides`）。前端不用改（目录来自 `GET /api/agents`，
  且已改为按当前设置现算）。`agent_type` 缺省时 `defaultSpawnCommand` 兜底成 `<agentType> --acp`，
  dsh 这类非 `--acp` 形态在 manifest 的 `DefaultSpawn` 里显式写。

**ACP 的模型通道有两条，读哪条决定写哪条**（`internal/agent/model_catalog.go` 的 `catalogSource`）：原生 agent（qodercli、**官方 dsh-acp**）在**标准 `configOptions`** 下发清单、用 **`session/set_config_option`** 设模型（**按轮换模型也走这条**，见 `acp.go` 的 `promptOnce`：先 set 再 prompt，顺序不能反）；另一条是 **`_meta["pieqi/configOptions"]`** 清单 + 请求 **`_meta.model`** 的兜底通道。判据是**标准字段优先**——反过来会把"两者都发"的原生 agent 误判成兜底通道，用 `_meta.model` 去设，而原生 agent 忽略 `_meta`，表现是「界面显示已切换、实际没换」。`_meta` 那条**别当成所有 ACP agent 的通用入口**（bd0cd28 就是这么误伤的 qoder）。它曾用于一份**已弃用的**本地 dsh-acp fork——该 fork 已删（见 `docs/adr/0011`，原因是它要求清空 profile pin，而 pin 一空 `promptCapabilities.image` 必为 false、图片能力全失）；**分支与回归测试刻意保留**，因为它是"标准字段恒空时怎么活"的协议容错，删掉等于假定所有 ACP agent 都合规。回归测试 `internal/agent/model_catalog_test.go`。

数据模型集中在 `internal/model/task.go`：`Task` 挂 `Events []TaskEvent`、`Baseline`、`CurrentDecision`、`Intervention`、`OriginChannel`。

前端结构：`web/src/features/` 按域切分（agent/approval/dashboard/feedback/session/task/timeline），另有 `pages/`（薄路由页）、`stores/`（Pinia）、`services/api` + `services/websocket`。

### 任务生命周期与两条审批路径（关键）

`pending → running → waiting_input ⇄ running → completed | failed | cancelled`

`waiting_input` 分两种，恢复路径不同（`model.DecisionKind`）：

- **路径 A（approval）**：PreToolUse hook / ACP RequestPermission 触发，**agent 进程还活着**挂在 channel 上。恢复走 resolve → 同一进程继续。
- **路径 B（choice）**：Claude 文本提问（`[CHOICE]` 格式），Claude 已 `end_turn` 退出（**进程已死**）。恢复走 `--resume -p <选项>` → 新进程。

这直接决定重启行为：`TaskStore.load()` 启动时把 `running` 和等待 approval 的任务标记 `failed`（子进程已死），等待 choice 的任务**保留** `waiting_input`，可隔天继续选。改这块逻辑时必须维持这个区分。

### 持久化（没有 SQL 数据库）

- 一任务一文件 `~/.pieqi/tasks/<id>.json`，每次变更 `os.Rename` 原子写（`internal/core/task_store.go`，Windows 分支见 `rename_windows.go`）。内存 map 作索引。
- 文档里提到的「Event Store / SQLite」尚未落地；当前 `TaskEvent` 是内嵌在 Task JSON 里的切片，就是反馈系统的事实源。
- 启动恢复逻辑在 `TaskStore.load()`。

### 事件模型

`EventBus`（`internal/core/event_bus.go`）是内存 fan-out，订阅者慢则丢弃积压。事件类型：`task_created | task_updated | task_deleted | task_delta`；`task_delta` 只带文本增量（真流式逐字），其余带完整 Task 副本。WS 层（`internal/api/ws.go`）订阅后转发给 PWA。

### 反馈体系（Feedback 层）

`internal/core/` 里 `feedback.go / git_diff.go / checkpoint.go / prospective.go / checks.go / evidence.go / outcome.go / preview.go / visual.go / push.go` 构成 Observe→Verify→Control→Intervene 闭环。**领域词汇必须用对**，否则写出歧义代码——术语表见 `CONTEXT.md`。

事实源关系（对应 `docs/adr/`，改这块先看 ADR）：
- `TaskEvent` 是事实源，`FileChange` 从事件派生（ADR-0001）。
- Baseline 是只读 git HEAD 参照，绝不写用户分支；Checkpoint 只覆盖 Agent 改过的文件（ADR-0002）。
- Turn 以用户消息为界（ADR-0003），是变更分组 / Checkpoint / Rewind 的基准单位。
- Evidence→Continue 由**后端**把证据组装成上下文（ADR-0004）。
- Check 优先复用 Agent 已跑过的 test/lint/build 结果，而非重跑（ADR-0005）。
- ⚠️ **视觉反馈（截图 / console / network）已于 2026-10-15 整体移除**，不再是 pieqi 的能力。
  两次移除的判据见 `docs/adr/0012`（截图）与 `docs/adr/0013`（console/network，含完整判据与
  被否掉的方案）。要点：整层触发条件苛刻（preview running + 人工点击）、实际从未被使用，
  而维护成本包括一个需 `npm install` + chromium 的独立 Node 服务。
  - 四个 preview 子路径（`capture` / `console` / `network` / `screenshots`，含
    `screenshots/<id>.png`）现在**显式返回 410 Gone**。**不要**改成 404 或删掉这些判断 ——
    `previewRoute` 是 catch-all，未命中的路径会掉进**反向代理**，把 `/preview/console`
    当成页面路径发给被预览的 dev server，调用方拿到一个 HTML 后**分不清"功能没了"还是"服务坏了"**。
    （`screenshots/<id>.png` 带子路径，实测曾漏掉、掉进 302 —— 回归测试
    `TestAPI_P2_VisualSubpathsGone` 覆盖 6 条路径。）
  - `Evidence` 的 `Console`/`Network`/`Screenshots` 字段与 `EvidencePrompt` 的对应输出均已删除；
    `TestEvidencePrompt_NoVisualResidue` 用**负向断言**钉住它们不复活。
  - **preview 本身（P1）不受影响** —— 把 agent 改完的代码跑起来在手机上看，仍然可用。
  - **preview 有第二种形态（ADR-0017，2026-10-24）**：worktree 的 `.pieqi/preview/` 里只要有 `.html`（含
    `<dir>/variant-a/index.html` 这类一层子目录），`DiscoverPreview` 就判 `static`，由 pieqi **进程内直接 serve**
    （不 spawn、无端口）。用途是 UI 迭代的方案对比页/HTML 报告：零内存，且**任务终态不回收**
    （`stopIfProcess` 只收有子进程的实例），用户任务完成后仍能回看。
    **目录不只认一个**：`PreviewStaticDirCandidates` 还认 `previews/`、`proposals/`、`docs/proposals/`、
    `web/public/proposals/` 等常见落点 —— 位置不该成为正确性前提（agent 记错目录不该导致功能失效）。
    要换到别处就在 `.pieqi/preview.json` 写 `{"framework":"static","dir":"..."}`。
    认不出时 `GET /preview/status` / `POST /preview/start` 会返回 `convention` + `hint` + `suspected`
    （疑似方案页的相对路径），**不要把这些字段删成裸 unavailable** —— 用户据此知道该让 agent 往哪挪。
    **不要再让 agent 去起 `python -m http.server`** —— 那是被否掉的方案（端口注入 + 进程回收 + 终态失效）。
  - **产物（Artifact）默认即可访问（ADR-0018，同日）**：`GET /api/tasks/:id/artifacts` 列出 agent
    产出的可查看文件（html / markdown / image / pdf，事实源 `TaskEvent → DeriveFileChanges`，
    未触碰文件不进清单）；打开 URL 由服务端算（目录内 html → `/preview/<id>/<rel>`，其余 → `/file`）。
    静态产物**不需要点「启动」**：`PreviewManager.EnsureStatic` 是唯一入口（status / 代理 / 产物清单
    三处共用，别各写一份 —— 漂移症状是同一件 HTML 这次走 preview、下次退化成 `/file`）。
    ⚠️ **`/file` 返回 html / svg 时必须带 `CSP: sandbox`（且不给 `allow-same-origin`）**：
    否则 agent 生成的页面站在 pieqi 的 origin 上，能读 localStorage 里的 token、以用户身份打 `/api`
    —— 那等于把 XSS 变成默认能力。这条是"默认可访问"的前提，不是可选加固。
    前端入口**在文件清单里，不在预览面板**：产物本质是**文件**，而文件清单早就存在
    （「变更」Tab 的累计 / 本轮两个视角）。所以每行文件名右侧直接给「打开」（html）/
    「查看」（图片）——数据来自 `/artifacts` 的 `open`，前端**不自己拼 URL**（拼错的表现是
    404 或 CSP 空白页，用户分不清"不是产物"还是"通道坏了"）。**别再在预览面板里另列一份
    产物清单**：同一份 html 在两处列，条数/路径迟早对不上，用户不知道该信哪个。
  - **文件清单按目录分组、默认收起**（`features/feedback/fileGroups.ts`）：分组头带文件数
    与 +/- 合计，收起态就携带"值不值得展开"的判据；单一目录不套分组头（一组还能收起
    等于多点一次才看到内容）。TurnCard 与累计清单共用同一份分组逻辑，别各写一套。
    `PreviewSection`（预览 Tab）仍只为 static 之外的形态渲染启动/重启/停止。
  - 采集服务源码**留档**（不是删）在独立工程 `pieqi-ui-test/services/visual-capture`。
    因此不再需要 `PIEQI_VISUAL_DIR`（该变量曾用于让 pieqi 指向迁走的服务）。
  - 术语表同步：`CONTEXT.md` 的「视觉反馈（P2）」整节已改为"已整体移除"说明。
    ADR-0006 记录的是当年的决策，**不改写**；以 ADR-0012/0013 与本注释为准。
- 明确不做 Web IDE / Computer Use / 录屏（ADR-0007）：查看文件属于 Feedback，编辑文件属于 IDE。产品边界扩张类需求先对照这份非目标清单。

这部分通过 `api.Server` 的 nil-safe 注入接线（`SetFeedback / SetCheckRunner / SetPushRegistry`）：未接线时对应端点返回降级响应，`main.go` 按构建顺序逐层 wire。

## 安全模型（`internal/auth`）—— 改动前必读

完整方案见 `docs/security-hardening.md`。**被未授权访问 = 本机任意代码执行**（能驱动 agent 跑命令、能审批高危工具、能管隧道、能重绑管理员），所以鉴权只能加强不能取消。

- **主鉴权按形态二选一**：有固定域名（named）→ Cloudflare Access（`auth/access.go` 做源站侧 JWT 验签：强制 RS256、核 `aud`/`iss`、校 `exp/nbf`，只用团队域 JWKS，且限制未知 kid 触发重拉频率）；无域名（quick/纯内网）→ 外链 `?token=`（内存 TTL token，不落盘）作为唯一凭据。named 下 `token_fallback` 可关。
- **客户端 IP 只能从可信转发头推导**（`auth/auth.go`）：优先 `CF-Connecting-IP`，否则取 `X-Forwarded-For` **末段**。**绝对不要改回取首跳**——历史 bug 就是首跳可伪造，任何人加一个 `X-Forwarded-For: 127.0.0.1` 即被判为内网而完全绕过鉴权（commit 25aea94）。回归测试见 `forwarded_ip_test.go`。
- **路由按「bootstrap path（`/tunnel/start` 发首个 token）vs mutation path（需已签发 token）」分层 gate**，这条分层是为规避死锁，改动时勿破坏。
- 飞书单账号绑定管理员（`/api/auth/bind` 仅内网）+ 失败限流拉黑 + 审计日志。
- 隧道有域名回收自愈：健康巡检连续失败达阈值自动重启换域名、推送新链接给飞书管理员，PID 文件跨重启清理孤儿 `cloudflared` 进程。

## 审批免审名单与风险分级

**运行期生效的免审名单来自设置页，不是 `config.yaml`**：`Settings.AutoApproveTools()`（`internal/core/settings_store.go`）由 `auto_approve_l0` / `auto_approve_l1` 两个开关推导（默认都开），经 `main.go` 的 `runner.SetAutoApproveTools` 接线。`pieqi.auto_approve_tools` 这个配置项**已删除**（它只被解析、从未被消费，且默认值里的 `delete` 属 L3、与硬边界矛盾）；写它会在启动时得到一条弃用告警。改免审行为请改 Settings。

分级表 `riskLevelKinds` 是唯一真相，**卡片显示的风险与放行判据共用它**：L0 `read/search/fetch/think`、L1 `edit/move`、L2 `execute/switch_mode/other`、L3 `delete`。`RiskOfKind` 对**未知/空 kind 一律返回 L2**（保守侧）——所以任何让 ToolKind 变空的改动，表现都是"免审全线失效 + 卡片全显示执行命令"。

两条路径都按 ToolKind 匹配，且都需要适配器正确透传：

- **ACP（qoder/dsh/codex）**：`internal/agent/acp.go` 取 `params.ToolCall.Kind`。qodercli 把所有 shell 调用一律报 `execute`，因此只读命令会经 `internal/agent/readonly_kind.go` 的 `DowngradeReadonlyPermission` 降级为 `read`（见 `docs/adr/0008`）——**证明不了只读的一律维持 L2**。
- **sdk-bridge（Claude Code）**：ToolKind 由桥的 `TOOL_KINDS`（`services/claude-sdk-bridge/src/session.js`）按工具名映射，`internal/agent/claude/session.go` 的 `permission_needed` 分支负责透传。**桥与 Go 侧任一处漏掉都会静默退化成"全部人工审批"**，回归测试见 `readonly_kind_test.go` 与 `session_test.go` 的 `TestSessionPermissionCarriesToolKind`。

扩展时注意：免审名单认的是 **ACP ToolKind**，不是工具名；改桥的映射表要同步确认值落在 `riskLevelKinds` 认识的 kind 里（`services/claude-sdk-bridge/test/tool_kind_test.mjs` 守着这条）。

**降噪补全（2026-10-21，ADR-0015）**：白名单原来只认 POSIX 读取命令，导致测试/构建占 66% 的弹卡（实测 101 次审批里 67 次是 `npm run test`/`npx vitest`/`playwright` 一类）。现在分四层：

| 层 | 文件 | 职责 |
|---|---|---|
| 只读白名单 | `readonly_kind.go` | `ls/grep/...` **加** PowerShell cmdlet（`Get-Content`/`Select-String`/`Test-Path`/`Out-String`…）与诊断命令（`netstat`/`tasklist`） |
| 命令族分档 | `shell_runner.go` | `npm run test/typecheck/build`、`npx vitest/playwright` 按**脚本名**分档；`node -e` 只在原文干净时判读 |
| 原文扫描 | `readonly_kind.go` 的 `permissionReqHasUnsafeText` | 删除/直写/进程控制/提权痕迹**一出现就整条不降级**（含引号里的） |
| 非 shell 工具 | `noncommand_tool.go` | `lsp`/`job_output`/`todo_write` 按**参数**证明只读 |

三条硬边界**不许放宽**：**所有删除**（用户 2026-10-21 明确要求人工审）、工作区外写、`git push` 与改基线。命令名一律 lower 比较（cmdlet 大小写不敏感），`baseCommandName` 会剥 `.exe/.cmd/.bat/.com`。

### 审批的「重复投递」必须是幂等的（2026-10-23 定，教训来自 f74bcb09 会话）

**现象**：一场会话后期刷出 16 条 `approval resolve FAILED: no pending permission for decision`，
用户看到的是"审批时提示 no pending"。

**根因不是一个**，是两个都必须治：

1. **时间线错位（`PermissionWire.pending`）**：approve 之后 agent 可能**紧接着**再发一条
   `RequestPermission`，`CurrentDecision` 从 `req-N` 换成 `req-N+1`。前端手里那张旧卡
   （或者用户连点两次）回投的是 `req-N`，而 pending 表里早就没有它了。
   → `agent_perm.go` 的 `settled` 表（有界 256）记住每个 reqID 的最终结论（含**自动放行**与**超时**）。
   `Resolve` 因此分三种结果：`PermResolveMissing`（**从来不存在**，才算真错误）/
   `PermResolveApproved` / `PermResolveDenied`。后两者是幂等成功、不再驱动 adapter。
   **不要把 already-settled 又并回 no pending** —— 那些命令其实**已经执行过**了，
   给用户一句 "no pending" 是误导性的答复。
2. **卡片没人清理（`CurrentDecision`）**：`TaskRunner.clearStaleDecision` 在轮末与新轮起点
   摘掉"卡片还在、但已经不可能被应答"的 approval 卡（判据：wire 里没有这条 pending
   **且** 已过 `decisionStaleGrace`=5s；`choice` 卡与"没有 wire"一律不动 —— 保守侧）。

前端配套三条（`web/src/stores/approval.ts`）：提交前 `refreshTask` **现取** decision id
（不用渲染时刻的旧 props 值）；卡已不在则不发请求；后端回"已经处理过了"时改投
**`approve_same`**（补偿动作，不进 UI 按钮）——它的语义是把该 ToolKind 记成本会话免审，
因为那条 RequestPermission 早已返回、没有载体，这是"用户再点一次允许"唯一能兑现的效果。
失败一律 `notify.error`，不许静默吞掉。

回归测试：`agent_perm_test.go` 的 `TestWirePermission_RepeatedApproveIsIdempotent` /
`ApproveSameAfterSettledMakesKindAutoPermitted` / `UnknownDecisionStillFails` /
`AutoApprovedIsRemembered`，`stale_decision_test.go`，`web/src/stores/approval.spec.ts`。

## 配置迁移注意

`internal/config/config.go` 的 `Load()` 含弃用检测，用 `v.InConfig`（只认配置文件显式值——`v.IsSet` 会把 `SetDefault` 误报为已设置）判显式，命中即打告警，语义不贸然删除。

**2026-10-22 整改（用户要求：约定优于配置）——ACP agent 不再由配置声明**：

| 以前 | 现在 |
|---|---|
| `agents.qoder/dsh/codebuddy` 各一节 + `AgentXxxConfig` 类型 | 删除；代码事实归 `internal/agent/manifest.go` |
| `transport: acp` 决定注不注册 | manifest 的 `Kind` + 运行期 `agents.enabled` |
| `agent_type: qodercli`（值恒等于常量） | manifest 的 `ACPType` |
| `spawn_command: []`（正确值是空） | manifest 的 `DefaultSpawn`；机器相关覆盖走运行期 `spawn_overrides` |
| `pieqi.acp.*` 自动回填到 `agents.qoder.acp` | **不再回填**，改为告警并指出新落点（`settings.json` 的 `agents.spawn_overrides`） |

判据：**写在配置里的取值永远是同一个常量**（例如 `agent_type: codebuddy` 永远是 `codebuddy`），
那就不是配置而是代码事实；抄一遍只会多一个能写错的地方。改动时不要把这些节加回来 ——
`TestConfig_ExampleHasNoACPAgentSections` 与 `TestConfig_ACPAgentSectionsRemoved` 守着。

唯一保留在配置里的 agent 节是 `agents.claude`：桥端口/token、claude 二进制名、默认模型
是真正的机器相关运行期事实，无法由代码推导。

加新配置项时仍仿照既有模式：显式判 `v.InConfig`、命中即告警、不要静默改语义。

## 发版流程（改完代码，怎么让跑着的服务用上新版）

### 落点唯一性（2026-10-14 定案；同日三次修订：`.new`/`.old` 归位）

**两个目录、两种职责，绝不能合并成一个**：

| 路径 | 角色 | 唯一写者 |
|---|---|---|
| `<repo>/build/pieqi.new.exe` | **编译/交付产物**（build/ 里**唯一**的二进制） | 编译命令 / agent |
| `%USERPROFILE%\.pieqi\bin\pieqi.exe` | **运行产物**（服务真正在跑的那个） | `build\restart.bat`（`copy`，**不是** `move`） |
| `%USERPROFILE%\.pieqi\bin\pieqi.exe.old` | **回滚备份**（`execPath+".old"`） | `core.SelfRestart`（`selfrestart.go:168`）/ `restart.bat` 安装前快照 |
| `%USERPROFILE%\.pieqi\bin\pieqi.exe.failed` | 起不来的版本留证（`execPath+".failed"`） | `core.SelfRestart`（`selfrestart.go:484`） |

- ★★ **`.new` / `.old` / `.failed` 的分工判据是"它属于编译期还是运行期"**：
  - `.new` = **待交付的编译产物** ⇒ 必须在 agent 写得进的仓库内（`build/`），且**只有一个**。
  - `.old` / `.failed` = **运行期的回滚备份与失败证据** ⇒ 必须与**正在执行的那个二进制**
    同目录才有意义，所以只在 `~/.pieqi\bin\`，由服务自己写。
  - ⚠️ **不要往 `build/` 里放 `.old`**（2026-10-14 的旧 `restart.bat` 就这么干过）：
    那会让"回滚备份"出现**两个写者、两份文件**，"哪个才是真回滚"没有唯一答案。
    `restart.bat` 安装后会把 `build/pieqi.new.exe` **删掉**，让交付位回到"干净待交付"状态。

- ★★★ **运行产物必须在仓库外** —— 理由见下一节「启动纪律」：仓库目录本身带
  `Mandatory Label\Low Mandatory Level`，Low 标签被**每个在仓库里创建的文件**继承，
  而 Windows 新进程的完整性 = `min(父令牌, 镜像文件标签)` ⇒ 跑仓库里的 exe **必然是**
  Low 进程 ⇒ 写不了 `~/.pieqi`（创建任务 500）。
- ⇒ 安装运行副本必须用 **`copy`**：新文件丢掉 Low 标签、继承目标目录的 Medium。
  `move` 会把 Low 标签一起搬过去；`xcopy`/`robocopy` 还可能连 SACL 一起搬。
- ⚠️ **agent 只能写 Low 目录**（会话是受限期），所以**交付位**必须在仓库内（`build/`）；
  **运行位**必须能写 `~/.pieqi`，只能在仓库外。**这两个需求天然指向不同目录**，
  别再试图用一个目录同时满足两者 —— 2026-10-14 的"统一到 `build/`"就是这么错的。
- **交付目录由约定推导，没有环境变量也没有配置项**（2026-10-22 修订）：
  **交付目录 = `<dataRoot>/bin`**，即默认 `%USERPROFILE%\.pieqi\bin\pieqi.new.exe`
  （见 `cmd/pieqi/main.go` 的 `resolveStagingDir`）。
  - ⚠️ **原判据 `<config.yaml 所在目录>/build` 已废弃**。它隐含假设"config.yaml 与
    go.mod 同级 = 仓库根"，但 config.yaml 实际上是**运行时产物**（含飞书凭据路径、
    隧道 token、端口、个人域名），实践中被放进 `~/.pieqi\bin`。一旦如此，该推导
    恒错：算出根本不存在的 `~/.pieqi\bin\build` ⇒ `stagingDir=""` ⇒
    `POST /api/admin/restart` 退化为 restart-only，**看起来重启成功了，其实新版从未装上**。
  - 新判据只依赖 `dataRoot`（`PIEQI_HOME` 或 `~/.pieqi`），它本来就必须存在，
    且与 config.yaml 放哪无关。
  - 曾短暂用过 `PIEQI_STAGING_DIR`，**已删除**：它把一条本可推导的约定变成了必须维护的
    配置，漏设即静默退化。**不要把它加回来**。
  - `filepath.Dir(os.Executable())` 也不作为推导依据（虽在现行布局下恰好同目录）：
    显式的 dataRoot 才是唯一事实源。算不出来时 `resolveStagingDir` 返回空串并
    **明确报错**，绝不静默退回某条猜测路径。
  - 推导结果不可用（目录不存在/是链接）时同样返回空串，**不代为创建**。
  - ⚠️ **投放必须 `cp` 不能 `mv`**：仓库里编出的 exe 带 Low 完整性标签，`mv` 会把它
    一起搬走 ⇒ 跑起来的进程写不了 `~/.pieqi`（症状：创建任务 500、日志零增长）。
- ⚠️ **构建命令绝不直接写 `pieqi.exe`**。若构建覆盖了交付位，`Restart()` 里那份
  "回滚备份"保存的就是**刚编出来的**，而不是"上次跑成功的那份"——回滚即失去意义。

### ★★★ 日常就一条命令（2026-10-23 起；用户要求：不要让人类介入发布，必须自重启）

```powershell
# 发布前先分诊：现在到底需不需要重启（只读；exit 0 = 已是最新，10 = 需要发布）
powershell -NoProfile -ExecutionPolicy Bypass -File build\preflight.ps1

# 需要发布就一条命令到位：前端 build → Go build → 投递 → 自重启 → 就绪校验
powershell -NoProfile -ExecutionPolicy Bypass -File build\release.ps1
#   -NoDeploy 只构建不发布（还没提交时用）
```

- **`GOTMPDIR` 必须指到工作区内**（脚本已自动设 `build\.gotmp`）。会话里手工编时漏了它就会报
  `creating work dir ... Access is denied`，而**工作区外的任何目录都不行**（含 `%TEMP%`）。见 `docs/adr/0016`。
- **`POST /api/admin/restart` 不会返回**：旧进程随它退场，调用它的会话会被切断。判断成败
  **一律用 `GET /api/admin/restart`**，`staged` 回到 `false` 才算换版生效；**不要重发 POST**。
- 纯 cmd 版：`build\release.bat`（同一套逻辑）；Windows 下没有 pwsh 时用它。

### 四步（手工路径，上面脚本就是这几步的封装）

```bash
# ① 前端必须先于 Go：前端是 //go:embed 进二进制的，反了就是"改了没生效"
cd web && npm run build                  # → web/dist

# ② 编后端 —— 落点是 build/pieqi.new.exe（**不是** pieqi.exe，见上）
#    ⚠️ GOTMPDIR 必须指到工作区内（build/.gotmp），否则 go 建不出 work dir
cd .. && GOTMPDIR="$PWD/build/.gotmp" go build -o build/pieqi.new.exe ./cmd/pieqi

# ③ 投递：运行副本必须在**仓库外**（仓库内的 exe 带 Low 标签，见「启动纪律」）
#     build\restart.bat（Windows）已封装：build/pieqi.new.exe --copy--> %USERPROFILE%\.pieqi\bin\pieqi.exe
#     必须 copy 不能 move（move 会带着 Low 标签一起搬）；安装后交付物被删，回到"干净待交付"

# ④ 触发自重启（staging 落点由 <dataRoot>/bin 约定推导，见上）
#    先 cp 产物到 %USERPROFILE%\.pieqi\bin\pieqi.new.exe（必须 cp 不能 mv），再调接口
cp build/pieqi.new.exe "$USERPROFILE/.pieqi/bin/pieqi.new.exe"
curl --noproxy '*' -s -X POST http://127.0.0.1:3000/api/admin/restart   # 202 = 已受理（有产物则换版）
```

> 日常就用上面那条 `release.ps1`（它就是 ①②③④ 的封装）。
> `build/build.sh` 是**发布打包**脚本（交叉编译 linux+windows → zip），不产出 `pieqi.new.exe`。
> `build/build-dev.sh` / `build/deploy-dev.sh` 是 bash 版，保留给 Git Bash 环境。

### 「有没有新产物」决定走哪条路（2026-10-21 定）

一句话：**有 `build/pieqi.new.exe` 就替换并启动，没有就只重启**。这条判据在
两个入口上是一致的，调用方不必自己分支：

| 入口 | 有产物 | 没产物 |
|---|---|---|
| `build\restart.bat` | `mode=swap-and-restart`：kill :3000 → 备份 `.old` → copy 装上 → 启动 → 探活 | `mode=restart-only`：kill :3000 → **一个文件都不动** → 启动同一份 exe → 探活 |
| `POST /api/admin/restart` | 202 `mode=swap-and-restart`，换版并重启 | 202 `mode=restart-only`，只重启（**2026-10-22 起不再回 409**） |
| `POST /api/admin/restart-only` | 202，**仍然只重启**（它从不看 staged） | 202，只重启 |

- **`POST /api/admin/restart` 已合并两个场景**（2026-10-22 用户要求）：判据与 `restart.bat`
  逐字对齐 —— **"有没有产物"决定走哪条路，调用方不必自己分支**。旧的 409 把"服务卡住了，
  重启一下"也一并拒绝了，调用方只好先 GET 拿 `staged` 再二选一，那套约定分散在每个调用方。
  现在仍返回 `mode` 与 `swapped` 字段便于调用方确认走了哪条。
  - 唯一的 409 边界：**没产物且 restart-only 编排未接线** —— 回 202 "已受理但什么都没发生"
    比明确拒绝更糟。
  - **有产物时 swapped=true 有优先级**：绝不因 restart-only 也接了就退化成只重启，
    那会把新版本静默丢掉（`TestAdminRestart_StagedTakesPrecedenceOverRestartOnly` 守着）。

- 脚本的判定很直白：`ARTIFACT` 存在且非零字节 ⇒ swap；否则 restart-only。
  零字节产物是构建中断的残骸，按"没有"处理（拿它覆盖服务等于把能用的换成空的）。
- **restart-only 模式绝不产生任何文件副作用**：不 copy、不写 `.old`、不删产物。
  这三条有测试守着（`internal/core/selfrestart_reload_test.go`）。
- restart-only 与换版共用同一套"让出端口 → 落交接单 → 拉起新实例 → 等就绪 →
  退出/回滚重绑"编排（`core.SelfRestart.restartAndExit`）。**不要**给只重启另写一条
  影子路径：2026-10-13 的死锁事故（旧实例不走、新实例 bind 不上）就藏在那段时序里。
- 只重启的交接单摘要取**当前** exe（`stagedOrSelfSHA256` 的兜底），所以新进程照样
  能通过摘要校验、把在途会话接回来，行为与换版重启一致。

### 三条纪律

1. **先提交，再编译，再重启**。面板上的 agent 宿主就是 pieqi 本体 —— 重启会打断自己，
   未提交的改动就此丢失。而"提交"只保护代码、**不保护二进制**：提交完服务跑的还是旧版，
   必须先编译出 `.new` 才谈得上重启（④）。2026-10-13 实测：漏了产物，自重启反复"回滚"
   （看起来像重启失败）。
2. **改了 `internal/core/selfrestart*.go` 本身时，自重启装载不了这次修复** ——
   跑着的旧代码执行 swap，它若坏了就换不上新版。此时必须请用户**从资源管理器双击**
   `restart.bat`（不能从会话里拉起：进程仍在会话树内，轮末被回收）。
   起来之后再用 ④ 验证新逻辑。
3. **重启前查在途任务**：`grep -lE '"status": *"(running|waiting_input)"' ~/.pieqi/tasks/*.json`。
   非零 ⇒ 会被自动接回（交接单机制），但**当前会话会中断**；为零 ⇒ 零损失。

### ⚠️ 启动纪律：绝不要运行仓库内的 exe（2026-10-14 重写——前一条结论是错的）

**任何**位于 `D:\code\go\pieqi\` 里的可执行文件都不能拿来跑服务，**与谁启动无关**。
前一条写的"只与谁启动有关 / Explorer 双击跑 `build\pieqi.exe` 是安全的"**是错的**：
2026-10-14 用户**双击 `restart.bat`** 就复现了故障（日志零增长、创建任务 500）。

真正的原因（每一步都实测过）：

1. **仓库目录本身带 Low 完整性标签**：
   `icacls "D:\code\go\pieqi"` → `Mandatory Label\Low Mandatory Level:(OI)(CI)(NW)`。
   （`D:\code\go`、`D:\` 都是普通 Medium；只有项目目录是 Low 隔离区，
   还带沙箱的 AppContainer SID ACE 与 `Everyone:(DENY)(DC)` —— 建目录的沙箱留下的。）
2. **Low 标签被仓库内新建的每个文件继承** ⇒ `build\pieqi.exe` 也是 Low。
3. **Windows 新进程的完整性 = `min(父进程令牌, 镜像文件的强制标签)`** ⇒
   跑 `build\pieqi.exe`，无论 Explorer、svchost 还是计划任务启动，都是 **Low 进程**。
4. Low 进程**能读** Medium 对象（向下读允许）、**不能写**（no-write-up）：

```
open C:\Users\<user>\.pieqi\tasks\<id>.json.tmp.<pid>: Access is denied
```

⇒ `POST /api/tasks` 回 **500**；而且**日志文件一个字节都不写**（文件 sink 建不起来时
`NewLogger` **静默**降级为仅控制台）。**"端口在听但日志零增长"就是最快的判别特征。**

- **改标签修不了**：非提权 Medium 进程对仓库内文件执行
  `icacls … /setintegritylevel Medium` → `拒绝访问`（需要 WRITE_OWNER）。
- **拷贝能修**：`copy` 出的新文件丢掉 Low 标签、继承目标目录的 Medium。实测
  `build\pieqi.exe` = Low，拷到 `~/.pieqi\bin\` = Medium。**这就是 `restart.bat` 第 ④ 步。**
- **判"跑着的实例是否中招"**：看 `~/.pieqi/logs/pieqi-<date>.log` 是否还在增长。
  `main.go` 的 `logger.Info("pieqi starting")` 在 bind **之前**且**无条件**执行 ⇒
  「`netstat` 说 :3000 在听，但日志停在上一个实例的最后一行」⇒ 该实例写不了盘。
> 历史教训：2026-10-14 一度以为"只要不在此会话内启动就安全"，据此把运行位统一进
> `build/` —— 当天用户双击 `restart.bat` 就复现了 500。**限制的变量是镜像文件的标签，
> 不是启动者。** 所以运行位现在固定在 `~/.pieqi\bin\`（仓库外），绝不再搬回仓库。

### 验证（重启后逐条过）

```bash
netstat -ano -p TCP | grep ":3000 " | grep LISTENING          # 有新 PID
curl --noproxy '*' -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:3000/api/tasks   # 200
curl --noproxy '*' -s http://127.0.0.1:3000/api/admin/restart # {"available":true,"staged":false}
```
`staged` 应回到 `false` —— 服务换完二进制后会比对内容摘要并**自动清理**已消费的 staged 文件。

> 判"现在跑的是哪一版"的硬判据：日志里的 `main.go:NNN` 行号，去当前源码 grep 同一句，
> 对不上就说明跑的不是这份源码（或运行副本被并行会话覆盖）。
> ⚠️ 统计日志时**先排除 `acp permission` 行** —— 那是工具调用的回显，你自己的排查命令里
> 若含 `FATAL` 等字样会被原样记进日志、污染 grep 结果。

## 协作约定

- **UI 改动的验收纪律**（三条，血泪换来的，见技能 `ui-change-verification`）：
  1. **`npm run test` 绿灯 ≠ 验收**。jsdom 量不出**尺寸、遮挡、滚动、首屏渲染代价**——
     涉及这些的改动必须做**真机**验证，而且要**量出数字并与基线对比**
     （例：挂载 Turn `[1..12]` → `[11,12]`；DOM 节点 21,206 → 2,135）。
  2. **验证永远跑在隔离端口**（如 `PIEQI_SERVER_PORT=3100` + 独立 `PIEQI_HOME`），
     绝不拿 :3000 上的现场当试验田。**部署前**拿待部署的那一份再验一次，**部署后**线上复量。
  3. **提交（commit）必须在重启之前**——面板上的 agent 宿主就是 pieqi 本体，
     重启会打断自己；未提交的改动就此丢失。

  **这一层已落地为 `web/e2e/`（Playwright，两层：数值断言 + 视觉基线）**，规程与已知坑见
  `docs/testing/ui-e2e.md`。日常用 `cd web && npm run test:e2e:impact`（按改动只跑相关的）。
  两条别踩：**几何断言要跟基线比**（只设上界阈值抓不到"布局平移 40px"这种回归）；
  **改 `tailwind.config.ts` 或夹具后必须重启 dev server**（`reuseExistingServer` 会复用旧进程，
  而 Tailwind 的内容扫描不会重扫 —— 表现为"我改的样式没生效"，且不报错）。
- **重编译/重启**：常规发版流程见上面独立的「发版流程」一节（四步）。
  深度内容（沙箱构建配方、外部拉起、环境坑）见技能 `long-running-service-rebuild`
  与 pieqi 特有的 `pieqi-rebuild-restart`——**那两个是可选参考，不是必经步骤**。
- **⚠️ 不要用「改构建落点 / 加白名单 / 去沙箱外跑」绕开沙箱权限——正解是发起提权申请**（用户 2026-10-14 定的，长期有效）。
  所有工程都按**正常工程规范**执行：前端就是 `npm run build`，后端就是 `go build -o build/pieqi.new.exe ./cmd/pieqi`。
  **命令本身不许为迁就沙箱而变形**。
  撞到沙箱拒绝时，**发一次 `sandbox_permissions` 提权申请**（`workspace-write` 优先，不够再 `danger-full-access`），
  让审批提示出现在用户面前，由用户决定；**不要自作主张换一条能跑通的歪路**。
  - 为什么：绕行会把"真实构建命令能不能跑"这个问题**藏起来**。踩过的实例：`go build -o bin/pieqi.exe` 报
    `Access is denied` 后我改用 `-o build/pieqi.exe` 再复制 —— 命令跑通了，但**掩盖了 `bin/` 子树的 ACL 缺口**
    （该目录下已有文件零沙箱 SID ACE），下次照样踩。
  - 提权是**单次、不影响审批链路**的：它走 `approveEscalation()` 注入一次性 `request.mode`（`allowed-once`），
    **既不读也不写 `DSH_PERMISSION_MODE`**，因此**不会**把 `approval.policy` 打成 `never`。
    这与下面那种"改环境变量"是两件不同的事，别混。
  - **`DSH_PERMISSION_MODE=danger-full-access` 仍然禁止**（那是部署级的，会把 approval 打成 `never`）——
    见 `~/.dsh/profiles/acp/cordis.patch.yml` 第 77-78 行的注释。**不要把它和单次提权混为一谈。**
  - 环境事实（不必再摸索，但仍要按上面的方式申请权限）：
    **`go build` / `go test` 的常规写法不需要缓存环境变量** ——
    正常写法就是 `go build -o build/pieqi.new.exe ./cmd/pieqi`。
    dsh 已把三个系统缓存根加进 `allowedDirs` 且实测它们带工作区 SID + Low 标签（真可写）：
    `%LOCALAPPDATA%\go-build`（GOCACHE，Go 默认值）、`~\go\pkg\mod`（GOMODCACHE，默认值）、
    `%LOCALAPPDATA%\npm-cache`。**默认值已经是对的，不要常设覆盖它们。**
    ⚠️ **2026-10-23 更正（本条改写过两次，以现在这版为准）：判据只有一条 ——
    `GOTMPDIR` 必须指到工作区内。** 证据见 `docs/adr/0016`。
    - `GOTMPDIR=<repo>\build\.gotmp` ⇒ 构建成功；
    - `GOTMPDIR` 指向**工作区外**任何目录（**包括 `%TEMP%`、会话私有 Temp、已存在的空目录**）
      ⇒ `go: creating work dir: mkdir ...\go-buildNNN: Access is denied`；
    - 未设置 ⇒ 同上失败。
    另：`-o` 的输出也必须在工作区内（写 `%TEMP%` 会 `copying ...: Access is denied`）。
    ⇒ **会话内完全可以编译**：`powershell -File build\release.ps1`（自动设 GOTMPDIR）。
    本文件早前两版写过"偶发、重试即过"和"限制针对 go 进程、必须请用户自己编"，**两版都是错的**，
    错因是当时的实验全都把 GOTMPDIR 放在工作区外。
    ⚠️ 判成败**只认产物**（存在且非零字节），别读退出码 ——
    `go build ... | Select-Object` 会让 `$LASTEXITCODE` 串到别的命令上，实测据此误判过一次。
    `npm run build` / `npm test` 需要 spawn（esbuild / vite），受限沙箱下会 `EPERM`（提权即可）；
    工作区内的 `bin/` 子树曾有 ACL 缺口（写被拒）——该目录已于 2026-10-14 随落点统一到 `build/` 删除。
  - **缓存放哪里（长期纪律，2026-10-14 定）**：按"是否需要跨会话复用"分两类，
    **不要按"哪个目录方便"**：
    - **持久构建缓存** → 系统默认位置（见上），**不要放工作区**。放工作区会被 `git status`、
      被 checkpoint 快照扫、被误提交，还要额外写 `.gitignore` 去挡。
    - **一次性产物** → 系统临时目录（`$env:TEMP`，注意它是**每会话私有**、会话结束即消失，
      所以只适合一次性产物），或明确忽略的工作区子目录，且**用完即删**。
    - `build/` 只放**交付物**（`pieqi.new.exe`）与**被跟踪的脚本**，别的都不放 ——
      这样"`build/` = 交付位"这个约定才干净。
    - 判据：**"跑一次攒一个"就是错的设计**。反面案例：UI 自测脚本（现已迁到独立工程
      `pieqi-ui-test`）曾把 Chrome profile 放在 `<repo>/web/build/selftest` 且从不清理，
      攒了 41 个 / 425MB。
  - **不要为了跑通而往 `allowedDirs` 里加目录**：那个白名单只放开"文件写"，
    对**命名管道 spawn**（vite/esbuild/Chrome 的 `stdio:'pipe'`）**永远无效**，加了也白加。
- 提交信息沿用仓库现有风格：conventional commits + 中文描述，如 `fix(auth): 修复转发头走私导致的鉴权完全绕过（CRITICAL）`。
  **正文写"为什么"**：判据、被否掉的方案、踩过的坑——比"修了 bug"有用得多。
- **提交只暂存自己的文件，禁止 `git add -A` / `git add .`**（本机常有多会话并行，见下条）。
  代价是真实的：2026-10-13 一次 `git add -A` 把并行会话的 21 个文件卷进了
  「删除 dsh-acp fork」那次提交 —— 功能没丢，但那批修复的**意图与判据没有任何提交信息承载**，
  事后只能靠 rebase 拆回来。
  - 正确姿势：`git add -- <路径1> <路径2> …`，逐个列出本次真正改动的文件。
  - `git commit` 前**必看** `git diff --cached --name-only`，确认里面没有别人的文件；
    发现混入就 `git reset`（仅取消暂存，不动工作区）后重新精确暂存。
  - 提交后 `git status --short` 应只剩自己的改动；若别人的改动**消失了**，
    说明被你的 `-A` 卷走了 —— 那批改动已在 HEAD 里，别慌，但也别再继续叠提交。
- **多会话并行时用 worktree 隔离**（可选但推荐）：
  `git worktree add ../pieqi-<用途> -b <分支>`，各自目录、各自索引，`-A` 也就伤不到别人。
  - 注意本仓库**运行期**的 worktree（`~/.pieqi/worktrees/`，pieqi 给被驱动 agent 建的）与
    **开发期**的 worktree 是两件事，别混为一谈；后者只是 git 层面的隔离。
  - worktree 里跑 pieqi 要遵守既有约束：**exe 必须在工作区外运行或由 svchost/Explorer 启动**
    （见「发版流程 · 启动纪律」——会话内直启会因受限令牌写不了 `~/.pieqi`），
    且要显式设 `agents.claude.bridge.dir`（见技能 `pieqi-rebuild-restart` 坑 5/坑 8）。
  - 用完 `git worktree remove <路径>` 清理；临时探针别留在 `git worktree list` 里
    （`build/` 下的历史遗留探针 2026-10-13 已清）。
- **只有 `git push` 与 `git reset` 需要先经用户批准**（仓库所有者要求，长期有效）。
  其余 git 操作（`add` / `commit` / `fetch` / `status` / `log` / `diff` / `branch` / `stash` / `checkout` 等）**不必请示，直接执行**。
  这两条被单拎出来，是因为它们**能不可逆地丢掉或改写别人能看到的东西**（其余本地操作做错了都还能救）：
  - **`push`**：改写共享远端。本地错了能改，推上去的历史会影响别人。
  - **`reset`**：默认 `--mixed` 会把已暂存的改动退回工作区，`--hard` 更会**直接丢弃未提交的改动**——
    那些改动往往正是并行会话或用户自己尚未提交的成果，丢了无从恢复。
  - 执行时把这两条各作为**单独一次调用**发起，别和别的命令打包，这样审批提示指向的就是它本身。
  - **同族命令同样按此处理**（它们与 reset 是同一类"改写/丢弃历史"的动作，不要因为不在字面清单里就放行）：
    `reset --hard`、`clean -f/-fd/-fdx`、`checkout -- <path>` / `restore`（丢弃工作区改动）、
    `branch -D`、`push --force`、`commit --amend`（对已推送历史）、`rebase`（对已推送历史）。
    判据是**「这个动作会不会丢掉/改写别人可见的东西」**，而不是命令名是否在清单里。
  - 注：本仓库工作区沙箱连 `.git` 也拒绝写入，所以 add/commit/fetch/push 都会触发一次 harness 提权提示；
    上面说的是 agent 的**自觉边界**，与 harness 提示不是一回事。
- 运行时数据在 `~/.pieqi/`（`tasks/`、`worktrees/`、`checkpoints/`、`logs/`、`previews/`、`feishu_binding.json` 等），不入仓库；`config.yaml`、`bin/`、`build/` 同样忽略。
  - ⚠️ **没有 `sessions/` 目录**（2026-10-14 实测确认）。README/`.gitignore`/配置模板曾把它列为
    pieqi 的会话存储，属**旧说明**：pieqi 只把会话 id 存进 `tasks/<id>.json`，会话实体由外部
    agent 自己保管（dsh 在 `~/.dsh/storages/session_projcache/sessions/`；claude 由 CLI 管）。
    别把 `~/.dsh` 的内容算作 pieqi 的数据。
- 改文档涉及领域概念时同步更新 `CONTEXT.md`；引入与既有 ADR 冲突的决策时新增 `docs/adr/00NN-*.md`，不要静默推翻。
- 运行依赖：`claude` CLI（登录态）、Node.js（桥服务）、`cloudflared`（需要外网访问时）；qoder 等 ACP agent 需本机装对应 CLI 且支持 `--acp`。dsh 额外两点：ACP 入口是 profile（`dsh --profile acp`，不是 `--acp`），且模型路由只取自 dsh `acp` profile 里 `acp` 这条 entry 的 pin（出厂 pin `deepseek-official` 需 `DEEPSEEK_API_KEY`，改 pin 到本地网关等就不需要；清空 pin 不回落 `agent-default-model`，会话会彻底没模型）——路由归 dsh 侧配置，pieqi 不干预。dsh 在 manifest 里标了 `OptIn`：**默认不进选择器**，要用需在运行期设置 `agents.enabled` 里显式列出。
