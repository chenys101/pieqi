# Pieqi Feedback P0 设计

> Status: Design (对应 `Pieqi「反馈体系」功能点规划.md` 的 P0 范围)
> Updated: 2026-09-03
> 关联：`CONTEXT.md`（词汇表）、`docs/adr/0001~0003`（架构决策）

---

## 1. 目标与范围

P0 终点（见功能规划 §33）：

> **Agent 改代码 → 用户手机看到 Changes → 查看 Diff → 必要时 Preview / Checks → 不满意 Rewind → 满意后继续。**

本文只覆盖 P0 主链路：

```text
Feedback 入口 → FileChange → Turn Change List → Change Summary
    → Timeline 联动 → 单文件 Diff → Baseline → Checkpoint → Rewind
    → Preview（Discovery / Lifecycle / Proxy）
```

P1+（双视角 Changes、Approval→Diff、Checks、Task Outcome、Evidence、Evidence→Continue、Rewind→Verify、视觉反馈）只定术语（`CONTEXT.md` 已收），不进本文。

## 2. 总体架构

```text
                     Task
                      │
          ┌───────────┼───────────┐
          ↓           ↓           ↓
      TaskEvent    Baseline    Checkpoint
          │           │           │
       EventUser      │           │
          ↓           │           │
        Turn          │           │
          │           │           │
          ↓           ↓           ↓
      FileChange ←── Git Diff   Rewind
          │
     ┌────┼─────┐
     ↓    ↓     ↓
   Summary Diff Timeline
               ↕
            Feedback
               │
        ┌──────┴──────┐
        ↓             ↓
     Preview        Checks(P1)
```

架构原则（ADR-0001/0002/0003）：

- **TaskEvent 是事实源**；**FileChange 是派生领域模型**（不单独持久化聚合）
- **Git 是真实代码状态的校验/差异 Provider**（只读，绝不写用户分支）
- **Checkpoint 是 Rewind 的恢复资产**（只覆盖 Agent 修改过的文件，绝不含用户 Task 开始前的未提交修改）

## 3. 领域模型与数据结构

### 3.1 Turn（派生）

- **定义**：一个 Turn = `EventUser(N)` 到 `EventUser(N+1)` 之间的全部事件（含其间所有 Agent / Tool 事件）。Turn #1 从 Task 初始 prompt（Create 预置的 seq=1 EventUser）起算。内部工具循环不产生新 Turn（ADR-0003）。
- **派生方式**：按 `Task.Events` 顺序扫描，每个 `EventUser` 开启新 Turn。Turn 号 = EventUser 序号。工具事件（tool_use/tool_result）归属其所在 Turn。
- **编号稳定性**：Turn 号由事件流唯一确定；Checkpoint 目录、Rewind 目标、API 参数全部用这个号，永不重新编号。

### 3.2 FileChange（派生）

从 tool_use / tool_result 纯函数聚合（ADR-0001），字段：

```go
type FileChange struct {
    Path        string   `json:"path"`
    Operation   string   `json:"operation"` // create | modify | delete | rename
    Turn        int      `json:"turn"`
    ToolUseIDs  []string `json:"tool_use_ids"`
    Status      string   `json:"status"`    // pending | success | failed
    Additions   int      `json:"additions,omitempty"`
    Deletions   int      `json:"deletions,omitempty"`
}
```

Tool → FileChange 映射（§4.1）。**不绑定 Git**：语义是「Agent 声明改了什么」。

### 3.3 Baseline（记录）

Task 创建时（`POST /api/tasks` 内部）自动记录，落盘在 Task JSON：

```go
type TaskBaseline struct {
    HeadSHA    string    `json:"head_sha"`     // git HEAD，只读参照
    CapturedAt time.Time `json:"captured_at"`
    DirtyPaths []string  `json:"dirty_paths,omitempty"` // Task 起始与 HEAD 不一致的文件
}
```

职责（与 Checkpoint 分离）：

- Baseline → **累计真实 Diff** 的基准（只读 `git diff <head> -- <agentTouched>`）
- Checkpoint → **Rewind** 的恢复资产

### 3.4 Checkpoint（物理捕获 + 组装）

**物理捕获全部发生在 Agent 静止的边界**（零竞态——工具事件只在 `in_progress` 时到达，Turn 中途读盘会与 Agent 写入竞态，故不在中途捕获）：

```text
~/.pieqi/checkpoints/<taskID>/
  pre/              ← Task 起始 dirty 文件全量字节快照
  <turnN>/          ← Turn N 结束态快照：只含 Turn N 实际碰过的文件
```

捕获时机：

| 时机 | 动作 |
|---|---|
| Task 创建 | 扫描 `git status --porcelain`，把与 HEAD 不一致（含 untracked）的文件**全量字节**拷到 `pre/`；记录 `DirtyPaths` |
| 下一条 `EventUser` 到达（Resume/续问） | 把上一 Turn（Turn N）的 FileChange 路径中仍存在的文件，拷到 `<turnN>/` |
| Task 进入终态（completed/failed/cancelled） | 同上，捕获最后一个 Turn |

> 注意：**正在进行的当前 Turn 没有完整 Checkpoint**——Rewind 只能回到已结束的 Turn 边界。

**组装（Turn N 开始前的文件状态）**：对文件 X，

1. 若 X 在 Turn < N 中被碰过 → 取**最近一次** <turnK>/X（K < N 且 K 最大）
2. 否则若 X 在 `pre/` → 取 pre/X（用户 dirty 或 untracked 的原样）
3. 否则 → `git cat-file -e HEAD:<X>` 存在则取 git HEAD 内容；不存在则视为「Task 起始不存在」

该组装逻辑同时服务：单 Turn Diff 的 before 侧、Rewind 的恢复目标。

**绝不**把用户 Task 开始前的未提交修改标为 FileChange / Agent 改动（dirty 文件只在 `pre/` 与 `DirtyPaths` 中体现）。

### 3.5 Rewind Event（新 TaskEvent）

新增 `TaskEventType = "rewind"`（与 text/user/tool_use 平级）。复用 `Input` 承载结构化载荷（与 tool_use 一致），`Text` 放人读摘要：

```go
// Input JSON
{ "to_turn": 3, "restored": ["A.vue","B.ts","C.css"], "preview_stopped": true }
// Text
"已回退到 Turn #3 之前，恢复 3 个文件"
```

Rewind 以事件入 Timeline（持久化 + 随现有 `task_updated` 全量推送）。**Timeline 永不删除**（ADR-0002）。

### 3.6 Task 模型扩展与存储布局

```go
// model.Task 新增字段
Baseline *TaskBaseline `json:"baseline,omitempty"`
// Events 中新增类型：model.EventRewind = "rewind"
```

磁盘布局：

```text
~/.pieqi/tasks/<taskID>.json           ← Task（含 Events、Baseline）
~/.pieqi/checkpoints/<taskID>/pre/     ← Task 起始 dirty 快照
~/.pieqi/checkpoints/<taskID>/<turnN>/ ← 各 Turn 结束态快照
```

Checkpoint 文件由新模块管理（§11），随 Task 删除清理。

## 4. 派生与 Diff 语义

### 4.1 Tool → FileChange 映射规则

| Tool | Operation | 说明 |
|---|---|---|
| `Edit` | modify | `file_path`；old/new 入参 |
| `Write` | create / modify | `file_path`；create 判定：路径既不在 baseline tracked、也不在 pre/、之前也未出现 → create，否则 modify |
| `Delete` | delete | `file_path` |
| `Rename` | rename | 保留枚举；当前工具路径下很少直接产生（多为 Write+Delete 或 Bash mv），P0 允许不出现 |
| `Bash` 等 | — | **不纳入**（无法可靠解析文件语义），已知限制 |

- `tool_use` 到达 → FileChange `status=pending`；对应 `tool_result`（按 ToolUseID join）→ `success` / `failed`（is_error）。
- 同一路径在同一 Turn 多次修改 → 合并为一条 FileChange，`ToolUseIDs` 累加，增删统计按 diff 重算（§4.2）。
- NotebookEdit（前端常见）与 Edit 语义同构，P0 可选纳入。

### 4.2 三类 Diff

| 视图 | 定义 | 来源 |
|---|---|---|
| 单文件 · 单 Turn | `<turnN> 之前` vs `<turnN> 结束`（对 Turn N 碰过的文件） | 组装 before（§3.4）+ `<turnN>/` 快照 |
| 单文件 · Baseline 累计 | baseline vs 当前工作区 | `git diff <head> -- <path>`（untracked/已删按内容补齐） |
| 累计 · 全任务 | baseline vs 当前，**只对 Agent-touched 路径集** | `git diff <head> -- <FileChange 路径集>` + 新建文件按全增补齐 |

- 大文件截断（diff 超阈值只返回首段 + `truncated:true`）
- 二进制文件跳过（`binary:true`，不返回 diff 文本）
- 懒加载：diff 只在请求单文件时计算

### 4.3 用户 dirty 文件隔离原则

- 累计 Diff **只覆盖 Agent-touched 路径集**，用户 Task 起始就存在的未提交改动不会出现（除非 Agent 真碰了它——此时它已是 Agent 改动，用 `pre/` 区分 before/after）。
- Rewind 的恢复集合 = Turn N 起的 FileChange 路径集；用户从未被 Agent 碰过的 dirty 文件**永远不会被 Rewind 触碰**。

## 5. API 契约

全部挂在 `/api`（继承 `ExternalAuthMiddleware`，隧道 token 即外部凭据）。

### 5.1 `GET /api/tasks/:id/feedback`

反馈总览（手机端一屏拉齐）。

```json
{
  "task_id": "...",
  "baseline": { "head_sha": "...", "captured_at": "...", "dirty_paths": [] },
  "turns": [
    {
      "turn": 1,
      "start_event_seq": 1,
      "summary": { "files": 3, "additions": 82, "deletions": 31 },
      "changes": [
        { "path": "src/Login.vue", "operation": "modify", "turn": 1,
          "tool_use_ids": ["..."], "status": "success", "additions": 40, "deletions": 12 }
      ]
    }
  ],
  "cumulative": { "files": 8, "additions": 321, "deletions": 87 },
  "checkpoints": [1, 2, 3],
  "preview": { "state": "available", "framework": "vite", "port": 5174,
               "url": "/api/tasks/:id/preview/" }
}
```

- `turns[].summary` 与 `changes` 由后端用与前端相同的派生函数计算（单一实现，避免双份逻辑漂移）。
- `preview.state`：unavailable / available / starting / running / stopped / error。

### 5.2 `GET /api/tasks/:id/feedback/diff?path=...&turn=...`

- `turn` 省略 → Baseline 累计 diff；给定 → 该 Turn 的单文件 diff。
- 响应：

```json
{
  "path": "src/Login.vue",
  "turn": 3,
  "operation": "modify",
  "diff": "...unified diff 文本...",
  "additions": 40,
  "deletions": 12,
  "truncated": false,
  "binary": false
}
```

### 5.3 `POST /api/tasks/:id/rewind`

请求：`{ "to_turn": 3, "scope": "code" }`（P0 仅 `scope:"code"`）。

- **Task 处于 `running` → `409`**：`{"error":"Agent 执行中，暂不可回退"}`。
- 允许状态：`waiting_input / completed / failed / cancelled`。
- 成功（同步执行，文件数受 Agent 触碰数限制）：

```json
{
  "ok": true,
  "rewind_event_seq": 45,
  "to_turn": 3,
  "restored": ["A.vue", "B.ts", "C.css"],
  "preview_stopped": true
}
```

行为：组装 before-Turn-3 状态 → 覆盖/删除/恢复文件 → 若 preview 运行则停止 → 追加 `rewind` TaskEvent（持久化 + 推送 `task_updated`）。

### 5.4 Baseline 记录（内部）

`POST /api/tasks` 内完成：git HEAD 记录 + `pre/` 快照 + `DirtyPaths`。无独立端点。

### 5.5 WS / 事件

- **不新增 WS 事件类型**（ADR-0001）：前端复用现有 `task_updated`（全量 Task）本地派生 turns/changes/summary。
- 唯一协议新增：`rewind` TaskEvent 类型（随 `task_updated` 全量到达，前端 normalizer 增加映射）。
- Preview 状态走轮询（`GET /feedback` 或 `GET /preview/status`），P0 不推流。

## 6. 状态流转

### 6.1 Task 生命周期（不变）+ Rewind 约束

```text
pending -> running -> waiting_input <-> running -> completed | failed | cancelled
                                        │
                                        └── rewind（仅静止态）── 不改 Task.Status，仅追加 rewind 事件
```

- Rewind 不改变 Task 生命周期状态。
- 回退后可通过 `append_prompt` 继续（新 Turn）。
  - ⚠️ 已知：Agent 会话上下文可能与恢复后的文件不同步——P1「Evidence → Continue」解决，P0 接受。

### 6.2 Preview 状态机

```text
unavailable ──(discovery 通过)──▶ available ──(用户启动)──▶ starting ──(端口探测成功)──▶ running
                                      ▲                        │
                                      └──────(退出/停止)◀──────┴──(异常退出)──▶ error
```

| 触发 | 动作 |
|---|---|
| 用户点「启动预览」 | discovery → spawn → 探测端口 → running；失败 → error |
| 用户停止 | stop（SIGTERM→KILL） |
| Task 结束/删除/服务器关停 | 全量清理（PreviewManager 回收） |
| Rewind 成功 | 停 preview（文件已变，内容过期） |

### 6.3 Checkpoint 捕获时机

与 TaskRunner 的钩子点（新增 FeedbackStore 注入）：

| 钩子点 | 动作 |
|---|---|
| `createTask` | 捕获 baseline（`pre/` + HeadSHA + DirtyPaths） |
| `Resume`（append EventUser） | 捕获上一 Turn 文件结束态 |
| 终态 transition | 捕获最后一个 Turn 结束态 |
| `deleteTask` | 清理 `checkpoints/<taskID>/` |

## 7. Preview 设计

### 7.1 Project Discovery

输入 `task.ProjectPath`，**端口绝不硬编码**（本项目自己的 vite 就是 5174）：

1. 探测项目根（及常见子目录 `frontend/ web/ app/`）最近的 `package.json`
2. Framework：devDependencies 含 vite → `vite`；next → `next`；nuxt → `nuxt`；否则若存在 `scripts.dev` → `node`
3. Command：按 lockfile（pnpm-lock → `pnpm`，yarn.lock → `yarn`，否则 `npm`）选 `run dev`
4. Port：`vite.config.*` / `next.config.*` / `nuxt.config.*` 的 port 字段 → framework 默认值（vite 5173 / next·nuxt 3000）
5. **实际端口以进程输出为准**：vite 默认 `strictPort=false`，端口被占会自动 +1 并打印 `Local: http://localhost:PORT/`——spawn 后解析 stdout 拿到真实端口，冲突自愈
6. 覆盖文件 `.pieqi/preview.json`：`{ framework, command, port, cwd, env }` 存在则优先
7. 以上任一不可得 → `unavailable`，不猜

### 7.2 Preview Lifecycle（PreviewManager）

复用 `internal/agent/claude/proc.go` 的 `Proc` 模板（幂等 spawn + 健康探测 + watcher 唯一 `Wait` + SIGTERM→KILL）：

```go
type PreviewInstance struct {
    TaskID      string
    ProjectPath string
    Framework   string
    Command     []string
    Port        int      // 解析自进程输出
    Proc        *Proc
    State       string   // starting | running | stopped | error
}
type PreviewManager struct { mu sync.Mutex; instances map[string]*PreviewInstance }
```

- 空闲端口：spawn 前 `net.Listen("127.0.0.1:0")` 探测，必要时以 `--port <free>` / `PORT=<free>` 覆盖（vite 优先，其余尽力）
- **只绑 127.0.0.1**（与主服务 0.0.0.0 相反，隔离）
- watcher goroutine 唯一 `cmd.Wait()` → 感知异常退出 → state=error
- 清理：Task 结束/删除、服务器关停、Rewind

### 7.3 Preview Proxy

**单隧道只有单 hostname，Preview 用子路径暴露**：

```text
Mobile ──▶ https://<trycloudflare>/api/tasks/:id/preview/* 
            (ExternalAuthMiddleware + 任务归属校验)
            └─▶ PreviewManager 注册表查 taskID→127.0.0.1:PORT ──▶ dev server
```

- **防任意端口靠设计**：代理只转发到注册表里该 taskID 绑定的端口，**绝不接受客户端传入的端口/URL**
- 鉴权：继承 `ExternalAuthMiddleware`（隧道 token）；任务归属校验（Task 存在；多用户归属为 P1）
- 转发 WebSocket upgrade（vite HMR）
- **HTML 重写中间件**：对 `text/html` 响应注入 `<base href="/api/tasks/:id/preview/">`，并把根绝对 `src`/`href`（如 `/@vite/client`）改写成子路径前缀。覆盖 vite/next/nuxt 常见形态
- **子进程安全**：Preview 子进程不继承 Pieqi 的隧道 token 相关环境变量（防 dev server 代码读到凭据）
- 已知限制：客户端路由写死根绝对路径的复杂 SPA 在子路径下可能失效（P0 接受）

## 8. 前端

### 8.1 派生与 normalizer

- `web/src/types/api.ts`：新增 `FileChangeDto / FeedbackBundleDto / DiffDto / RewindRequestDto`、`TaskEventTypeDto` 加 `"rewind"`
- `normalizer.ts`：增加 `rewind` 事件映射 → Timeline 渲染「已回退到 Turn #N 之前」卡片
- **派生函数前后端同源**：turns/changes/summary 的派生逻辑建议抽成共享纯函数（后端一份权威，前端本地快速派生用同一规则），避免双份漂移

### 8.2 Feedback 视图

- PC：会话页内 Feedback 面板（Task → Timeline / Feedback 双入口）
- Mobile：Feedback Drawer
- 数据：Change List / Summary 本地从 `sessionStore.events` 派生；单文件 Diff 调 `GET /feedback/diff`
- 手机端主验收面：Change Summary → 展开文件 → Diff

### 8.3 Timeline ↔ Feedback 联动

- Turn 卡片 → 跳到 Timeline 对应 Turn 起始事件（`start_event_seq`）
- Timeline 事件 → 打开 Feedback 对应 Turn
- 双向跳转基于 `seq`/`turn`，不依赖新状态

## 9. 验收标准

| # | 功能 | 验收 |
|---|---|---|
| 1 | Feedback 入口 | 任意 Task 可从会话页进入 Feedback（PC 面板 / 手机 Drawer）；不依赖特定 Agent；Mobile/PC 均可访问 |
| 2 | Tool → FileChange | 一次多 Edit/Write 的 Task 正确聚合；Delete 显示为 delete；失败工具标 failed；Bash 修改不出现（已知限制） |
| 3 | Turn Change List | 按 Turn 分组；文件列表 + `+N/-M` 统计；能定位 Timeline |
| 4 | Change Summary | 规则生成（文件数/增删/路径/operation），不调模型 |
| 5 | 单文件 Diff | unified diff、增删计数、大文件截断、二进制跳过、懒加载 |
| 6 | Baseline | Task 创建时记录 HEAD + dirty 列表；用户 dirty 文件不出现在累计 Diff |
| 7 | Checkpoint | Turn 结束自动捕获；只含 Agent 碰过的文件；Task 起始 dirty 单独存 `pre/` |
| 8 | Code Rewind | `POST /rewind {to_turn}` 恢复文件到 Turn N 前；running → 409；Timeline 追加 rewind 事件、不删历史；回退后可 append_prompt 继续 |
| 9 | Timeline 联动 | Feedback ↔ Timeline 双向跳转可用 |
| 10 | Preview Discovery | 识别 vite/next/nuxt 与端口；端口不硬编码；未识别 → unavailable |
| 11 | Preview Lifecycle | start/stop/status/restart；绑 127.0.0.1；Task 结束/删除/关停自动清理；异常退出可感知 |
| 12 | Preview Proxy | 子路径可访问；继承隧道鉴权；端口仅来自注册表；WS upgrade 可用；preview 子进程不持有隧道 token |

## 10. 已知限制与 P1 边界

- **Bash 修改不可见**（FileChange 只覆盖可解析工具）；Rename 很少直接产生
- 复杂 SPA 客户端路由绝对路径在 Preview 子路径下可能失效
- 运行中不可 Rewind（409）；Rewind 后 Agent 会话上下文与文件可能不同步（P1 Evidence→Continue）
- 当前 Turn 无完整 Checkpoint，Rewind 只能回到已结束 Turn 边界
- 用户 dirty 文件在 `pre/` 全量快照；若 dirty 文件大且 Agent 不碰会白占空间（P0 接受）
- Preview 仅 GET/HEAD/WS 完整支持，其余方法尽力转发
- 多用户任务归属校验为 P1
- Checks / Task Outcome / Evidence / Evidence→Continue 属 P1，不在本文

## 11. 落地拆解

### 后端（internal/core）

| 模块 | 内容 |
|---|---|
| `feedback.go` | FileChange 派生、Turn 切分、Change Summary（纯函数，可单测） |
| `checkpoint.go` | `FeedbackStore`：`pre/` + `<turnN>/` 捕获、before 组装、Rewind 恢复 |
| `git_diff.go` | 只读 git 助手：`DiffFiltered(repo, head, paths)`、`CatFile`、`statusPorcelain` |
| `preview.go` | `PreviewManager`：Discovery / spawn / 端口解析 / 清理（复用 `agent/claude/proc.go`） |
| `api/feedback.go` | `GET /feedback`、`GET /feedback/diff`、`POST /rewind` 路由 + 处理器 |
| `api/preview.go` | `/api/tasks/:id/preview/*` 反向代理 + HTML 重写 + WS upgrade |
| `model/task.go` | `TaskBaseline` 字段、`EventRewind` 类型 |

接线：TaskRunner 注入 FeedbackStore（§6.3 钩子点）；router.go 注册新路由。

### 前端（web/src）

| 模块 | 内容 |
|---|---|
| `types/api.ts` | 新增 DTO + rewind 类型 |
| `normalizer.ts` | rewind 事件映射 |
| `features/feedback/` | Feedback 面板/Drawer、Turn Change List、Change Summary、Diff 视图 |
| `stores/feedback.ts` | 派生 turns/changes/summary（本地） |
| Timeline | rewind 卡片 + 双向跳转 |

### 实施顺序

```text
Phase 0: feedback.go 派生 + feedback API + 前端 Change List/Summary（看见改了什么）
Phase 1: git_diff + baseline + diff API + 前端 Diff（确认改了什么）
Phase 2: checkpoint + rewind API + rewind 事件 + 前端 Rewind（改错可回）
Phase 3: preview（Discovery → Lifecycle → Proxy）+ 前端启动/查看（看到运行效果）
```
