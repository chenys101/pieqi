# ADR-0021: Collaboration Plane（双路径：短路径 Task + 需求级 Loop）

- 状态：**已采纳（Accepted）**（2026-11-03：待审四条按草案全收）
- 日期：2026-11-02（同日修订：取消持久 GraphNode，改 **planned Task + 依赖边**；
  2026-11-03 定稿）
- 取代关系：
  - **吸收并升级** [ADR-0014](0014-requirement-closed-loop.md) 的领域模型（Requirement / TaskGraph / Issue）；
    0014 仍保留为历史提案，**施工以本 ADR 为准**。
  - **扩展** [ADR-0007](0007-feedback-non-goals.md) 的产品边界表述：控制面能力保留为
    **短路径**；协作平面是超集，不是另起炉灶。
  - **不推翻** ADR-0001～0005（TaskEvent 事实源、Baseline、Turn、Evidence、Checks）。
  - **存储实现** 拆到 [ADR-0022](0022-store-sqlite-task-migration.md)；本 ADR 只定产品/领域与「Store + SQLite、禁止双真相」。
- 相关讨论：dsh-superagent（借 Gate/人审/结构化报告，不借 Assignment）、
  冯若航评文（显式 Context）、存储（Store → SQLite → 可选 PG）、
  方案抉择：**B = planned Task 即计划格**（见 §3.1）。

## 背景

Pieqi 今天的定位是 **Agent Control Plane**：渠道 / PWA → 调度 → 多 agent 传输 →
单 Task 内 Feedback。短路径必须继续零摩擦可用。

真实工作常大于一个 Task：不同阶段技能与验收不同；后面阶段常依赖前面产出才能知道
「要拆成几票」；缺陷要回流。需要 **Collaboration Plane**，并用双路径保住短路径。

## 决策

### 1. 定位：Collaboration Plane ⊃ Control Plane

| | Control Plane（短路径，保留） | Collaboration Plane（长路径，新增） |
|---|---|---|
| 工作单元 | 单个 `Task` | `Requirement` 容纳多个 `Task` |
| 用户心智 | 「让 agent 干这一票」 | 「这条需求从拆到验都在这」 |
| 入口 | 新建任务 / IM 一句话 | 新建需求 → 确认计划 → 按依赖执行 |
| Feedback | 任务内 Observe→Verify→… | 全部复用 + 需求级 AC 矩阵 / Issue 回流 |
| 何时用人 | 审批、续问、验收预览 | 另加：确认拆分/展开、human-verify、非 blocker 回流 |

非目标：Web IDE / 真终端 / Computer Use；静默投机执行；TAPD/Jira Issue **双向同步**。

### 2. 双路径：短路径永远是一等公民

#### 2.1 短路径

```text
人/IM → Task（RequirementID == ""）→ 今日 Runner / Feedback / 审批
```

硬约束：不强制先建需求；`RequirementID==""` 无需求 UI/状态机；既有 API/WS/IM/自重启语义不变；长路径对短路径 nil-safe。

短路径 Task **也可带**可选 `role` / skills 提示（方便选 agent），但不进入需求图调度。

#### 2.2 长路径：Requirement + planned Task 图（渐进展开）

**计划格 = 尚未轮到执行（或尚未解除依赖）的 Task**，不再另建持久 `GraphNode`。

```text
人/IM → Requirement(draft)
      → split/expand 产出 SplitProposal（未入 Task 表）
      → 人 confirm → 落地为 Task（多为 planned）+ 依赖边 + graph_version++
      → 依赖满足的 planned → 可启动（进入既有 pending/running …）
      → 某 Task 完成 → 可选再 expand（提案 → 再 confirm → 追加 planned Task）
      → 失败 → Issue → 新建 fix 向 Task → 重验
      → 全部 AC 达成 → Requirement done
```

典型演进：

```text
v1 confirm:  Task(design, planned→run)
design 完成，expand 提案：3 个 FE
v2 confirm:  Task(fe-a|b|c, planned)，边 design→各 fe
fe 完成，expand：5 个 BE 模块
v3 confirm:  Task(be-1…5, planned)
再 expand test …
```

「发现要 3 个前端任务」= **确认后多 3 条 Task**，不是一条 Task 下挂子任务列表。

#### 2.3 Promote

孤儿 Task 写入 `RequirementID`；若尚无图，它就是需求下唯一节点（可已是 running/completed）。
不改写其 Timeline。禁止自动 Promote。

#### 2.4 轻量需求 / 零 AC

允许单 Task 需求；`verify=human` 不强制 Check。  
零 AC = 仅作挂靠容器，**不能**宣称 testing 闭环完成；有 AC 则 confirm 时须覆盖或标 `deferred`（done 前清零）。

### 3. 领域模型（方案 B）

#### 3.1 为何不坚持 GraphNode（说服结论）

曾考虑「GraphNode = 计划格，Task = 执行体」。在 1:1 且图会渐进展开的前提下：

| GraphNode 分立 | planned Task |
|---|---|
| 两套实体、派发时拷贝 role | **一个实体**：role/skills/AC/依赖都在 Task 上 |
| 未开工的格不进任务列表 | 列表需过滤 `planned`（产品可做） |
| 短期更「纯」 | 与短路径、Promote、人的心智一致 |

**后续拓展**（多 agent、skill 路由、AC 矩阵、回流）并不依赖 GraphNode；它们依赖的是
`Task.role`、依赖边、Requirement、Issue。  
唯一仍需要的「非 Task」是 **未确认的拆分提案**（见下）——寿命短，不是第二执行模型。

**结论：采纳方案 B。** 用户理解「planned Task ≈ 还没轮到 / 还没确定开跑的那一格」正确；
与 GraphNode 等价的是 **已 confirm 落地的 planned Task**，不是未确认的 LLM 草稿。

#### 3.2 核心类型

```text
Requirement
  id, title, body, project/cwd, status, acceptance[], issues, events, graph_version

AcceptanceItem
  id, text, verifiable, deferred?

Task（短路径与长路径同一模型，增量字段）
  …既有字段…
  requirement_id?     空 = 短路径
  role?               design|frontend|backend|test|review|fix|other
  skills[]?           role→skills 目录上的提示，可覆盖
  agent?              偏好 agent
  covers_ac[]?
  verify?             auto|human|either
  depends_on[]        同需求内前置 Task id（DAG）

Issue                 同 0014（severity / covers_ac / 证据链 / fix_task_id）

SplitProposal         短暂：split/expand 的结构化草案，confirm 前不进 Task 表
                      拒确认则丢弃；通过则创建/更新 planned Task + 边
```

#### 3.3 Task 状态（长路径增量）

在既有 `pending → running → waiting_input ⇄ … → completed|failed|cancelled` 之上：

| 状态 | 含义 |
|---|---|
| `planned` | 已纳入需求图，**依赖未满足或尚未到调度**；不跑 agent |
| （可选）从 planned 启动时进入现有 `pending`/`running` | 短路径创建可继续直接 pending/running，不经 planned |

纪律：

- 依赖未完成：保持 `planned`，不得开跑。
- **已进入 running/终态** 的 Task：不可改 role 冒充重来；纠错 → Issue / 新 Task。
- `planned` 在下一次 expand 确认前可改提案侧描述；confirm 后未开跑前允许人删/改（记需求事件）；开跑后冻结。
- 仪表盘默认：**短路径活跃 + 长路径非 planned**；需求页才展示全图含 planned。

#### 3.4 校验（fail closed）

1. 有 AC：当前版本中每条未关闭 AC 被至少一个未取消 Task `covers_ac` 覆盖，或 `deferred`。
2. `verify=auto` 可落到 Check；`human` 等人验。
3. `depends_on` 无环；`fix` 主要由 Issue 回流创建。
4. 每次 confirm：`graph_version++` + Requirement 事件快照；禁止静默改图。

`role → skills`：配置/目录约定，不是独立聚合。

#### 3.5 Outcome / Issue 回流

两层结果不要混：

| | 含义 | 用途 |
|---|---|---|
| **Task Outcome** | 单 Task 结束后的派生结果（变更、Checks、预览、本票问题串） | 任务页 Feedback（既有能力） |
| **Requirement Outcome** | 需求总账：每条 AC 是否达成、开放 Issue 列表 | 需求页 AC 矩阵 |
| **Issue** | 结构化缺陷（钉 AC + 证据链），**可派活** | 回流的唯一入口 |

纪律：从验证失败回到再开发，**只经 Issue**；不改写已终态 Task 冒充「再跑一轮计划」。

**正向（验）：**

```text
planned Task（test / review 等）依赖满足 → 开跑
  → DeriveChecks / DeriveOutcome（任务级；Check 优先复用 agent 结果，ADR-0005）
  → DeriveRequirementOutcome：
        按 covers_ac 汇总 AC 绿/红
        verify=human：等人点过/不过才更新该 AC
```

**出 Issue（发现）：**

自动 Check 红、人验拒、review 打回 → 创建 Issue：

```text
Issue
  id, title, detail
  severity          blocker | major | minor
  covers_ac[]
  source_task_id    发现它的验证向 Task
  source_turn / check_id   证据链
  status            open
  fix_task_id       （空）
```

Requirement Outcome 上对应 AC 显示未达成，并挂开放 Issue。

**回流（派修）：**

```text
Issue open
  ├─ blocker → 自动生成 Fix Task（仍写 Requirement 事件，可审计）
  └─ major/minor → 推人，确认后才生成 Fix Task

Fix Task（新建，不改写旧 test Task）
  role: fix（或 frontend/backend 等）
  covers_ac: 与 Issue 对齐
  requirement_id: 同一需求
  depends_on: 可选
  status: planned → 开跑

Issue.status = fixing，fix_task_id = 新 Task
```

修完后：

```text
Fix Task completed
  → 重跑（或重开）原验证向 Task / Check（同一 AC）
  → 通过：Issue → resolved，AC 转绿
  → 仍红：Issue 保持 open/fixing，或另开 Issue；禁止静默吞掉
```

**总图：**

```text
        ┌──────── Requirement ────────┐
        │  AC 矩阵 ← Requirement Outcome
        │  Issues[]
        └──────────┬──────────────────┘
                   │
     design → fe-* → be-* → test-* → …
                   │
            验证失败 / 人验拒
                   ▼
              Issue(open)
                   │ blocker 自动或人确认
                   ▼
              Fix Task(planned→run)
                   │ 完成
                   ▼
              重验同一 AC ──通过──► Issue(resolved)，AC 绿
                   └失败──────────► Issue 仍挂起

全部 AC 绿且无未处理开放 Issue → Requirement done
```

**与渐进 expand 的分工：**

| 发现时机 | 走哪条 |
|---|---|
| 设计/实现中发现「还要多几票 FE/BE」 | **expand → confirm → 新 planned Task**（补计划） |
| 验证中发现「某 AC 未达标」 | **Issue → Fix Task → 重验**（缺陷回流） |

两条路都不改写已跑完 Task 的 Timeline。

### 4. 显式 Context

长路径 Requirement 绑定 project/cwd；子 Task 继承。禁止猜仓库。短路径照旧。

### 5. 存储：Store + SQLite（细则见 ADR-0022）

```text
Store 接口 → SQLite（默认）→ 预留 PostgreSQL
```

进库：`requirements` / `acceptance` / `issues` / `tasks`（含 role、depends_on、requirement_id）/
append-only events（task + requirement）。  
**无 `graph_nodes` 表**；DAG = Task 上的 `depends_on`（或等价 edge 表，主键仍是 task_id）。  
`SplitProposal` 可不落盘或短 TTL，以 Requirement 事件留审计即可。

短路径 Task 一并迁入，禁止双真相。Schema、迁移与 `TaskStore` 替换边界 → [ADR-0022](0022-store-sqlite-task-migration.md)。

### 6. 产品与 API

- 默认 UX 仍短路径；需求入口不阻断。
- `/api/tasks/*` 不变；长路径 `/api/requirements/*`（split/expand/confirm/promote/outcome）、`/api/issues/*`。
- confirm 的请求体是对 `SplitProposal` 的接受，服务端创建 planned Task。

### 7. 分期

| 阶段 | 内容 | 短路径回归 |
|---|---|---|
| P0 | Store/SQLite + Task 迁移；Requirement CRUD | 短路径黄金样例全绿 |
| P1 | Task.role/skills/covers_ac/depends_on；`planned`；SplitProposal + confirm | 无 Requirement 时零差异 |
| P2 | DAG 调度（依赖满足才启动）；expand；Promote | planned 默认不刷爆首页 |
| P3 | Outcome / Issue 回流 | 孤儿 Task 无需求 UI |
| P4 | PWA 需求页 + 双入口真机验收 | 仪表盘仍短路径友好 |

## 被否掉的方案

| 方案 | 原因 |
|---|---|
| 持久 GraphNode 与 Task 双实体 | 1:1 时心智重复；role/skills 本就属于 Task；拓展不依赖分立 |
| 废除短路径 | 毁掉现有价值 |
| 两套存储 | 双真相 |
| Assignment 聚合 | 与 Task 抢执行绑定 |
| 未确认提案直接写成 Task | 拒确认会留下垃圾任务；故保留短暂 SplitProposal |
| 上游完成自动改图不经人确认 | 高杠杆 |
| 猜 Context / 静默投机 / TAPD 双向同步 / 先上 PG / 纯 JSON 协作平面 | 同前版 |

## 后果

- 心智：**Requirement 是文件夹+验收总账；Task 是唯一工作格（含 planned）；边表达先后。**
- 工程：Task 状态机要容纳 `planned` + 列表过滤；比 GraphNode 方案少一张表、多一个状态。
- 文档：CONTEXT 写 Short/Long Path、planned Task、SplitProposal、role→skills；不写 GraphNode 为领域词。
- 0014 的 GraphNode 用语视为被本 ADR 取代。

## 已决问题（2026-11-03 按草案全收）

1. **Promote 已 completed Task**：允许挂靠；不改写其 Timeline。
2. **零 AC 挂靠型需求**：允许作容器；**不得**宣称 testing 闭环完成。
3. **存储**：拆 [ADR-0022](0022-store-sqlite-task-migration.md)；本 ADR 只约束接口层级与「禁止双真相」。
4. **`planned`**：正式独立 `TaskStatus`，不用 `pending` + `blocked_by_deps` 冒充（避免与「已提交待跑」混淆）。

---

定稿动作：本文件 Accepted；`docs/README.md` 挂 0021/0022；0014 文首横幅；
`docs/CONTEXT.md` 术语改指本 ADR（Short/Long Path、planned Task、SplitProposal；不写 GraphNode）。
