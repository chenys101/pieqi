# 自重启后接回会话，包括发起重启的那个会话

## 背景

pieqi 的定位是**远程自迭代**：agent 改代码 → 编译 → 重启 → 验证。为了让这个闭环不需要人坐在机器前，「自重启」（`internal/core/selfrestart.go`）让服务能换掉自己的二进制并重新起来。

但自重启会**杀掉挂在本进程上的所有 agent 子进程**。老行为是启动时一律把 `running` 标成 `failed`（"进程被重启打断"），于是每次重启都留下一批死会话。

2026-10-06 的实测事故把这个问题推到了极端：三个连续任务（`cbbfe4e1` / `f2da1a92` / `f7fcd1ba`）**全部**以 `failed / 进程被重启打断` 收尾，且死法完全一致 —— 都死在最后那句"最终验证"：

| 任务 | prompt | 死亡时刻 |
|---|---|---|
| `cbbfe4e1` | 修复 sw 版本注入 | 00:03:54 杀 3000 端口进程 |
| `f2da1a92` | 无法续话排查 | 10:00:12「the real self-iteration loop」 |
| `f7fcd1ba` | 排查频繁中断原因 | 10:59:47 `POST /api/admin/restart` |

第三个任务本身就是在排查"会话为什么老是中断"，而它给出的验证手段是**让自己去触发一次重启** —— 用制造一次中断的方式验证中断问题被修复。日志铁证（`pieqi-2026-10-06.log`）：

```
10:59:48.347 self-restart: binary swapped
10:59:48.730 self-restart: new instance spawned
10:59:48.731 self-restart: new instance is serving, exiting old process
```

重启**成功**了（新 PID 32544、`/ready` 200），死的是这次验证的手段。

## 决策

分三件事做：

1. **`PIEQI_TASK_ID` 环境变量作为会话身份**。服务在 spawn agent 子进程时注入（`internal/agent/acp.go`），值来自 `SessionConfig.TaskID` → `OpenParams.TaskID` → `TaskRunner.ensureACPSession`。agent 里跑的 shell 因此能在自己发起的 HTTP 请求里带上 `X-Pieqi-Task-Id`。
2. **交接单记录发起者**（`RestartJournal.InitiatorTaskID`）。HTTP 层在**收到请求的当下**解析并 `SetInitiator`，`writeHandoff` 把它并入 `AffectedTaskIDs`。
3. **发起者拿专属接续文案**（`initiatorResumePrompt`）。明确告知"这次重启是你发起的、它成功了、**不要再次调用**"，并给出替代的只读确认方式。

## 为什么身份必须用环境变量，而不是 HTTP 头/查询参数

看起来头更简单，但**头无法自证**：agent 不知道自己的 taskID，除非服务告诉它。环境变量的可信度恰恰来自"只有本进程 spawn 的子进程才拿得到"——它天然不可伪造，不需要额外鉴权。

该端点的安全档位不变：仍是**仅内网**（同 `/api/auth/bind`，router 的 `adminGrp` gate）。伪造这个头最多影响"重启后要不要尝试接回某个 id"，而接回只作用于**真实存在的任务记录**，不存在则忽略。

## 为什么身份必须在"收到请求"时捕获，而不是退出前现取

这是整件事的根因，也是最容易写错的地方。

`LiveTaskIDs()`（收集被打断会话）在 `RestartAndExit` 内部、`waitForListener` 成功**之后**才调用。而此刻发起者那一轮**早已随 agent 子进程死去**，状态也不是 `running` —— 任何"现取"都拿不到它。

**身份只在请求进来的那一瞬间是新鲜可信的。** 所以 `postRestart` 先 `c.JSON(202)`，紧接着在同一处解析发起者并交给编排（`go s.selfRestart(initiatorFromRequest(c))`），一路 `SetInitiator` 落到 `pendingInitiator`。

同时它必须**并入** `AffectedTaskIDs`，不能只记在 `InitiatorTaskID` 里：新进程的 `TaskStore.load()` 是按 `interruptedByRestart` 集合决定"保留 running 还是判 failed"的。只记前者，任务在 load 时就成了终态 failed，接续逻辑压根看不到它 —— 这正是修复前发起者必死的机制。

## 为什么发起者需要一段不同的 prompt

发起者是唯一一个"自己触发了动作、却永远拿不到响应"的角色：它发出的 `POST` 还没返回，旧进程就随它退出了。

用通用文案（"请继续完成你原本的任务"）会让它倾向于**再试一次**，而重试的后果是把刚起来的新进程也杀掉 → 又产生一个待接回的发起者 → **重启循环**。这是本功能最危险的失败模式，因此 `initiatorResumePrompt` 给出的是**可核对的事实**而非命令：

- 新版本号是什么（让它能核对跑起来的是不是自己要的版本）
- 为什么那个请求不会返回（预期行为，不是失败）
- 要确认状态该改用 `GET /api/admin/restart`（只读，不触发重启）

回归测试 `TestResumeInterruptedWithInitiator_PromptWarnsAgainstRetry` 用"文案必须含劝阻重试"钉死这条。

## 跨 agent 可行性（实测，非推断）

续问路径在 `internal/agent/acp.go:NewSession` 早已是**协议层通用**的：按 `agentCaps.LoadSession` 自动二选一走 `session/load` 或 `session/resume`，无需按 agent 分支。用探针实测（`probe_dsh_caps/`、`probe_dsh_resume/`）：

| agent | 能力 | 结论 |
|---|---|---|
| dsh | `LoadSession=false` → 走 `session/resume` | **实测能接回**：杀进程后新进程 resume，暗号逐字命中 |
| claude (sdk-bridge) | `options.resume = sdkSessionId` | 支持 |
| claude (print) | `--resume <sid>`，有 `ErrNoConversation` 处理 | 支持 |
| qoder | 本机未安装，`agents.qoder` 休眠 | **未验证** |

dsh 探针的关键结论是**不是"resume 返回了成功"，而是"上下文真的回来了"** —— 暗号（`紫水晶-7391`）逐字命中，排除了"resume 成功但只拿到空壳会话"这个最危险的失败模式。

## 代价与边界

- **不保证一定接得回**：接续本身仍可能失败（会话在 agent 侧已过期、模型路由不可用等），此时 `ResumeInterrupted` 会把任务标 failed 并写明"自重启后自动接续失败"，不静默。
- **qoder 未验证**：本机没有 qodercli，`agents.qoder` 是休眠配置。逻辑上走同一条 `session/resume` 路径，但**没有实机证据**，接入时应用同一探针验一遍。
- **环境变量只对 ACP 路径注入**：claude print 路径（`claude -p`）不经 ACP spawn，目前不带 `PIEQI_TASK_ID`；sdk-bridge 路径由 Node 桥侧继承环境变量，天然带得上。这影响的是"claude 会话能否标识自己为发起者"，不影响它被接回（旁观者路径）。
- **仍需一次"由旧版本发起"的重启来装载修复**：修复代码写完时，跑着的旧进程没有交接单逻辑，它写不出单子 → 新进程读不到 → 静默按普通启动处理。这是所有跨进程修复的固有代价，`RestartJournal` 的注释里已写明。
- **`createSnapshot` 对 `selfupdate/` 目录报错**（`read ... Incorrect function.`）是独立的小问题（checkpoint 快照遇到非普通文件），不影响本决策。

## 与既有 ADR 的关系

- **ADR-0003（Turn 以用户消息为界）**：接续不是新 turn，而是把被打断的那个 turn 续完 —— prompt 由服务生成而非用户发出，这一点与"turn 由用户消息界定"不冲突：服务自愈文案计入该 turn 的收尾。
- **ADR-0002（Baseline 只读参照）**：接回会话后仍在同一 worktree / 同一 Baseline 上继续，无常变更动。
- 本 ADR 与 ADR-0008（只读降级）无关，但共用同一动机：**把用户体感里的"老是中断/老是审批"降到最低**。
