# Cursor ACP P1 验收 / 冒烟清单

依据 P0 结论 [`cursor-acp-p0.md`](cursor-acp-p0.md) 与计划验收项。

## 单元侧（本改动已绿）

```powershell
$env:GOTMPDIR = "$PWD\build\.gotmp"
$env:TEMP = $env:GOTMPDIR; $env:TMP = $env:GOTMPDIR
go test ./internal/agent/ ./internal/core/ -count=1 -run "Cursor|NormalizePermission|PickAllow|AutoApprove|HandleCursor|OpenCursor|AvailableAgents|RequestPermission_AutoApprove|PromoteCursorGenerateImage|DeriveFileChanges_GenerateImage"
```

覆盖：

| 验收项 | 覆盖 |
|---|---|
| 启用后出现在 `GET /api/agents` | `AvailableAgents`：默认无 cursor；`enabled` 含 `cursor` 时有 |
| 未登录可读错误 | `authenticateAfterInit` → 固定文案 `Cursor 未登录：…` |
| ask_question 不挂死 | `cursorExtBridge` + `handleCursorExtension` L0 `skipped` |
| 权限 Kind/OptionId | `NormalizePermissionKind` / `PickAllowOptionID`（hyphen + 空 Kind 模糊） |
| spawn 落点 | `%LOCALAPPDATA%\cursor-agent\agent.cmd` 进 `spawnFallbackPaths` |

## 真机冒烟（需可写 `~/.cursor/acp-sessions` + 已 `agent login` 或 `CURSOR_API_KEY`）

1. 设置页 `agents.enabled` 加上 `"cursor"`（或改 `~/.pieqi/settings.json`）后重启 / 热加载。
2. `GET /api/agents` 含 `cursor` / `Cursor Agent`。
3. 新建任务选 Cursor，纯文本一轮：PWA 见流式 delta。
4. 让模型写文件或跑 shell：弹审批卡；Approve / Deny 后进程继续。
5. 未登录环境：任务 **failed**，错误含「请本机执行 agent login，或设置 CURSOR_API_KEY」。
6. 若模型触发提问：任务不挂死；日志有 `acp extension method` / `default reply`。

### 建议的 10 个常见任务（P2 频率观察）

1. 纯问答（无工具）
2. 读一个文件
3. 写一个小文件
4. `ls` / `dir` 只读 shell
5. `npm test` 或等价
6. 删除文件（应人工审）
7. 多文件编辑
8. 显式要求「问我一个选择题」（诱发 ask_question）
9. 要求「先出计划再改」（诱发 create_plan / plan mode 注意）
10. 续问同一任务（验证 loadSession）

P2 L2 UI（ask_question 可选答、create_plan 审批卡）**仅当** 8/9 在真机高频出现再开；否则维持 L0。

## 本机沙箱备注（2026-10-29）

P0 探针在 authenticate 成功后，`session/new` 因 `EPERM mkdir ~/.cursor/acp-sessions/<id>` 失败——属沙箱写用户目录限制，不是协议缺陷。真机冒烟请在**非受限**进程（已安装的 pieqi 服务）上跑。

## 真机冒烟结果（2026-10-29，pieqi 服务进程）

前置：`build/pieqi.new.exe` 投递自重启；`PATCH /api/settings` 把 `cursor` 加入 `agents.enabled`。

| 项 | 结果 |
|---|---|
| `GET /api/agents` 含 cursor | 通过（`CURSOR_PRESENT`） |
| 纯文本一轮 | 任务 `fc0a534c-…` → `completed`，`output=pong`，有 thinking/text 事件 |
| 写文件 | 任务 `5e01db59-…` → `completed`，`workdir/smoke-write.txt` = `hello-cursor`（经 client FS，未弹 waiting_input——L1 免审开着且/或 Cursor 走 WriteTextFile） |
| shell | 任务 `d9e3ca97-…` → `completed`，输出含 `smoke-shell-ok`（本轮未观察到 waiting_input；审批链路仍由共享 `RequestPermission` + Kind 归一单测覆盖） |
| 未登录失败文案 | 单测 / `authenticateAfterInit` 固定文案；本机已能 authenticate，未再造未登录现场 |
| ask_question L0 | 单测 `cursorExtBridge` + `handleCursorExtension`；本轮模型未触发提问 |

**P2 L2 UI**：本轮未观察到高频 `cursor/ask_question` / `create_plan` → **暂不上 L2**，维持 L0 安全垫。

## 生图产物门禁（2026-10-29）

`cursor/generate_image` 通知带 `filePath`（Cursor agent 已画好）。pieqi **不代画、不自动拷贝**：

| 条件 | 行为 |
|---|---|
| `filePath` 在任务 `workDir` 内且文件存在 | 合成 `GenerateImage` tool 事件 → `DeriveFileChanges` → 反馈「变更」/artifacts |
| 宿主临时路径（会话 assets 等）或文件不存在 | 吞掉，不进产物清单 |

真机要面板能看图：提示里要求保存到工作区相对路径（如 `assets/moon.png`）。
