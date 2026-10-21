# ADR-0014: 需求开发闭环（Requirement / Task Graph / Issue）

- 状态：**提议中（Proposed）**
- 日期：2026-10-16
- 相关：ADR-0001（TaskEvent 事实源）、ADR-0004（Evidence→Continue 后端组装）、
  ADR-0005（Check 优先复用）、ADR-0007（非目标清单）、ADR-0013（视觉层移除）

## 背景

pieqi 现有的 Feedback 体系（`internal/core/` 的 `feedback.go / checks.go / outcome.go /
evidence.go`）解的是**单个 Task 内**的 Observe→Verify→Control→Intervene：
一个 Task 改了什么、验证通过没有、要不要回退、带证据继续。

但用户真实的工作单元常常**大于一个 Task**：一条需求往往要拆成若干开发任务，
开发完再拆出测试任务，测试发现问题还要回流成修复任务。今天这个层级**完全不存在** ——
用户只能自己在脑子里维护"哪些 Task 属于同一条需求""这条需求验收过了没有"。

需求（2026-10-16 提出）要求打通：

```
需求 → 拆分开发任务 → 执行任务 → 拆分测试任务 → 执行测试
     → 输出测试结果 → 提交 bug/issue
```

## 决策

新增**三个**领域概念（Requirement / Task Graph / Issue），**全部建立在既有事实源之上**，
不引入第二套存储、不新增 agent 驱动机制。

### 1. Requirement 是 Task 的容器，不是新的事实源

**ADR-0001 规定 `TaskEvent` 是唯一事实源、其余一切派生。** 本 ADR 不让 Requirement
成为例外：Requirement 自己**也持有一条 `Events []TaskEvent` 事件流**（与 `Task.Events`
同构），其状态变更全部以事件形式进入该流。因此"需求级 Replay"（呼应 P3 Task Replay）
不需要另建机制。

Requirement 落盘沿用任务级既有模式：`~/.pieqi/requirements/<id>.json`，
每次变更 `os.Rename` 原子写（复用 `task_store.go` + `rename_windows.go` 的 Windows 分支）。

### 2. 拆分产物是**结构化提案**，且必须经 Go 侧校验（fail closed）

任务拆分**不让 agent 的自由文本直接变成任务**。agent 必须输出结构化 JSON
（`AcceptanceItem[]` + `TaskGraph`），由 **Go 侧**校验后才成为可执行的任务图：

- 每条验收标准（AC）**至少被一个 test 节点 `CoversAC` 覆盖** → 否则**拒绝该拆分**
- Task Graph 无环 → 有环拒绝

拒绝语义与 `RiskOfKind` 对未知 kind 一律返回 L2 的保守原则一致：
**证明不了可验收，就当它不可验收**，而不是"尽力而为地执行一个自己都不完整的计划"。

### 3. `splitting` 是**等待人工确认**的状态

拆分结果不是自动执行的。理由：拆分质量决定其下所有 Task 的质量，
无人确认的自动拆分等于批量生产垃圾任务。这与 `waiting_input` 的
approval / choice 二分（`model.DecisionKind`）是同一思路 ——
**不可逆或高杠杆的动作必须先经人类决策**。

### 4. Issue 从字符串升级为结构化实体，并获得**回流**能力

现状：`TaskOutcome.Issues` 是 `[]string`（`outcome.go:44`），只能看、不能派活。

Issue 新增 ID / severity / 证据链（`SourceTaskID` + `SourceTurn` + `CheckID`）/
`CoversAC` / `FixTaskID` / `Status`。其中 **`CoversAC` 是这套设计的支点**：
它把"测试发现的缺陷"与"需求的验收标准"直接连起来，使系统能回答
"这条需求哪条验收标准还没被满足"。

**回流**是"闭环"与"流水线"的分水岭：Issue → 生成 `Kind=dev` 的修复节点 →
修完重跑原 test 节点 → Issue `resolved`。没有回流，Issue 就是终点。

回流触发按 severity 分流（避免自动雪崩，同时不让人漏掉严重问题）：
`blocker` 自动生成修复任务，其余推 IM 由人确认。

### 5. 执行与验证能力**全部复用**，不新增

| 环节 | 复用的既有能力 |
|---|---|
| 任务执行 | `task_runner.go` 状态机 / worktree / 事件发布 |
| 并发控制 | `agent/manager.go` 的每项目并发槽（需求内额外遵守 DAG 拓扑序） |
| 测试执行 | `CheckRunner`（5min 超时 / 8KB 保尾截断 / 落盘审计） |
| 测试结果派生 | `DeriveChecks`（**零额外执行**，遵守 ADR-0005） |
| 结果聚合 | `DeriveOutcome` → 新的 `DeriveRequirementOutcome` |
| 推送通知 | `PushRegistry` + `NotificationProvider`（lark / webhook） |
| API 接线 | `api.Server` 的 nil-safe 注入（`SetFeedback` 等同款模式） |

**特别地：测试任务必须优先产出可自动化的 Check**（即 `checkCommandRe` 认识的形式），
这样可直接白捡 `CheckRunner` 的整套执行/超时/审计基础设施。

## 数据模型

```go
type Requirement struct {
    ID         string           `json:"id"`
    Title      string           `json:"title"`
    Body       string           `json:"body"`
    Acceptance []AcceptanceItem `json:"acceptance"` // 拆分产物
    Status     string           `json:"status"`     // draft|splitting|executing|testing|done|cancelled
    Graph      TaskGraph        `json:"graph"`
    Issues     []Issue          `json:"issues"`
    Events     []TaskEvent      `json:"events"`     // 需求级事实源（同构 Task.Events）
    OriginChannel string        `json:"origin_channel,omitempty"`
    CreatedAt  time.Time        `json:"created_at"`
}

type AcceptanceItem struct {
    ID         string `json:"id"`         // AC-1, AC-2 ...
    Text       string `json:"text"`
    Verifiable bool   `json:"verifiable"`
}

type TaskGraph struct {
    Nodes []GraphNode `json:"nodes"`
    Edges []GraphEdge `json:"edges"` // from → to（to 依赖 from）
}

type GraphNode struct {
    TaskID   string   `json:"task_id"` // 指向 model.Task.ID
    Kind     string   `json:"kind"`    // dev | test
    Label    string   `json:"label"`
    CoversAC []string `json:"covers_ac,omitempty"` // 仅 test 节点
}

type GraphEdge struct{ From, To string }

type Issue struct {
    ID            string    `json:"id"`             // ISS-1
    RequirementID string    `json:"requirement_id"`
    Title         string    `json:"title"`
    Detail        string    `json:"detail"`
    Severity      string    `json:"severity"`       // blocker|major|minor
    SourceTaskID  string    `json:"source_task_id"` // 哪个测试任务发现的
    SourceTurn    int       `json:"source_turn"`
    CheckID       string    `json:"check_id,omitempty"`
    CoversAC      []string  `json:"covers_ac"`
    FixTaskID     string    `json:"fix_task_id,omitempty"`
    Status        string    `json:"status"`         // open|fixing|resolved|wontfix|duplicate
    CreatedAt     time.Time `json:"created_at"`
    ResolvedAt    *time.Time `json:"resolved_at,omitempty"`
}
```

`Task` 新增一个反向指针 `RequirementID string \`json:"requirement_id,omitempty"\``
（空 = 独立任务，保持既有单 Task 用法完全不变）。

## 状态机

```
Requirement:
  draft ──split──> splitting ──confirm──> executing ──dev done──> testing
                                            ↑                        │
                                            └────fix task（回流）─────┘
                                                                     │
                                                       全部 AC 达成 ──┴──> done
```

两条纪律：

1. `testing → executing` 的**回流只能经 Issue**，不能直接改 Task 状态 ——
   保证每次回流都有缺陷记录可审计。
2. Requirement 状态机与 Task 状态机**正交**：Task 仍是
   `pending → running → waiting_input ⇄ running → completed|failed|cancelled`，不改动。

## 拆分机制的实现先例：`generateTitle`

拆分**不需要新机制**。`task_runner.go:751` 的 `generateTitle` 已经示范了完全相同的模式：

```go
// 一次性 claude -p，要求模型只输出结构化内容，Go 侧清洗 + 校验 + 落库
cmd := exec.CommandContext(ctx, "claude", "-p", prompt, "--output-format", "text")
title := cleanTitle(string(out))   // ★ Go 侧校验：清洗 + 拒绝"元回复"
if title == "" { return }          // ★ 校验不过就放弃，不落库
```

`cleanTitle`（`task_runner.go:792`）甚至在拒绝"模型没理解任务"的**元回复**
（"没有附带""请提供"等），命中即返回空串、保留原有兜底 —— 这正是本 ADR 决策 2
「校验不过即拒绝」的既有先例，不是新发明。

拆分照此实现：`SpawnSplitPrompt(req.Body)` → `claude -p <split prompt> --output-format text`
→ **解析 JSON** → `ValidateSplit()`（AC 覆盖 / DAG 无环）→ 通过才 `req.Graph = ...`。

两处比 `generateTitle` 更严的要求：

1. **输出必须是可解析 JSON**，不能用 `cleanTitle` 那种"best effort 清洗" ——
   解析失败即整体拒绝（拆分是一组会真实消耗算力的执行计划，不能半信半疑地接受）。
2. **`--output-format text` 拿到的 JSON 可能被 markdown 围栏包裹**（```` ```json ````）——
   必须显式剥离围栏后再解析，且剥离失败按解析失败处理。

## API

```
POST   /api/requirements                创建需求
GET    /api/requirements                列表
GET    /api/requirements/:id            详情（TaskGraph + AC 矩阵）
POST   /api/requirements/:id/split      触发拆分 → 提案（status=splitting）
POST   /api/requirements/:id/confirm    确认拆分 → 执行（status=executing）
GET    /api/requirements/:id/outcome    需求级结果（AC 矩阵 + Issues）
POST   /api/issues/:id/fix              由 Issue 生成修复任务（回流）
PATCH  /api/issues/:id                  更新状态
```

接线方式沿用 `router.go` 既有的 nil-safe 注入（`SetFeedback` / `SetCheckRunner` /
`SetPushRegistry` 模式），未接线时返回降级响应。

## 分阶段落地

| 阶段 | 内容 | 验证方式 |
|---|---|---|
| P0 | Requirement + Issue 模型、落盘、CRUD API | 单元测试 + 重启恢复（照 `TaskStore.load()`） |
| P1 | 拆分 + Go 侧校验（AC 覆盖 / DAG 无环） | **负向测试**：无覆盖、有环的提案必须被拒 |
| P2 | TaskGraph 执行（DAG 拓扑序 + 复用并发槽） | 集成测试：线性/菱形依赖各一 |
| P3 | 测试拆分 + `DeriveRequirementOutcome`（AC 矩阵） | 纯函数单测（照 `outcome_test.go`） |
| P4 | Issue 生成 + 回流成修复任务 | 端到端：注入失败 → 出 Issue → 回流 → 修复 → resolved |
| P5 | PWA 界面（AC 矩阵 + Issue 卡片） | 真机验证（遵守 `ui-change-verification`：量数字对基线） |

**每阶段必须同时做的反向约束**（否则会腐化）：
`CONTEXT.md` 同步新词；负向断言测试（如 `TestEvidencePrompt_NoVisualResidue` 那样）
钉住被否掉的方案不复活。

## 被否掉的方案

- **Issue 与外部 Jira/GitHub 双向同步**：超出 Agent Control Plane 边界
  （ADR-0007），且引入凭据管理与冲突合并两个新问题域。Issue 保持在 pieqi 内，
  需要外发时走既有 `WebhookProvider`。
- **自动执行拆分结果（不确认）**：见决策 3。拆分错误会以其下所有 Task 的失败为代价放大。
- **给 Requirement 另建一套存储/事件流**：直接违反 ADR-0001，会造成两处数据需同步。
- **测试任务无脑重跑全部用例**：违反 ADR-0005（复用优先）；
  重跑只在用户显式点击或原结果过期时发生。
- **把 Requirement 做成"项目/工单"**：术语会与 `Project`（worktree 分组基准，
  `model.Project`）冲突。见 `CONTEXT.md` 的 `_Avoid_` 项。

## 影响

- `internal/model/`：新增 `requirement.go`、`issue.go`；`task.go` 加 `RequirementID`
- `internal/core/`：新增 `requirement.go`（状态机 + 拆分校验）、`requirement_outcome.go`（派生）
- `internal/api/`：新增 `requirement.go` 路由 + nil-safe 注入
- `CONTEXT.md`：新增 Requirement / Task Split / Task Graph / Test Plan / Issue 五词
- 既有 Task 单用路径**零改动**（`RequirementID` 为空即旧行为）
