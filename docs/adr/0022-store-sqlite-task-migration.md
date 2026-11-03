# ADR-0022: Store 接口 + SQLite（默认）与短路径 Task 迁库

- 状态：**已采纳（Accepted）**（2026-11-03，随 ADR-0021 待审第 3 条拆出）
- 日期：2026-11-03
- 相关：
  - [ADR-0021](0021-collaboration-plane-dual-path.md)（领域：Requirement / planned Task / Issue；本 ADR 只管持久化）
  - [ADR-0001](0001-taskevent-source-of-truth-filechange-derived.md)（TaskEvent 仍是事实源；迁库不改派生关系）
  - 被取代的落盘设想：ADR-0014 的 `~/.pieqi/requirements/<id>.json`（施工勿再按 JSON 一文件一需求）

## 背景

今天 Task 是一任务一文件 `~/.pieqi/tasks/<id>.json` + 内存 map（`TaskStore`）。
Collaboration Plane 要进 Requirement / AC / Issue / 依赖边 / append-only 事件，
并让短路径 Task **同一真相**——继续纯 JSON 会变成双写或两套恢复逻辑。

ADR-0021 §5 已定方向：**Store 接口 → SQLite（默认）→ 预留 PostgreSQL**。
表结构、迁移、与现有 `TaskStore` 的替换边界放在本 ADR，避免产品文档膨胀。

## 决策

### 1. 分层

```text
调用方（TaskRunner / API / Requirement 服务）
        ↓
   Store 接口（Go）
        ↓
   SQLite 实现（默认，文件在 dataRoot 下）
        ↓
   （预留）PostgreSQL 实现 —— 不先上
```

- 业务代码只依赖接口，不 `import` 驱动细节。
- **禁止**：JSON Task 文件与 SQLite 并行双真相；迁完后短路径读写只走 Store。
- **禁止**：为协作平面另起一套只写 Requirement 的存储。

### 2. 进库实体（与 0021 对齐）

| 逻辑实体 | 落库要点 |
|---|---|
| `tasks` | 既有 Task 字段 + `requirement_id` / `role` / `skills` / `covers_ac` / `verify` / `depends_on` / `status`（含 `planned`） |
| `requirements` | id、title、body、project/cwd、status、`graph_version`、… |
| `acceptance` | 挂 requirement；含 `deferred` |
| `issues` | 同 0021；含证据链与 `fix_task_id` |
| events（task + requirement） | **append-only**；TaskEvent 语义不变 |
| DAG | Task 上的 `depends_on` **或** 等价 edge 表（主键仍是 task_id） |
| `SplitProposal` | 可不落盘或短 TTL；审计靠 Requirement 事件快照 |

**无 `graph_nodes` 表。**

### 3. 短路径 Task 迁移（P0 门槛）

1. 启动时：若仍存在 `tasks/*.json`，一次性导入 SQLite，校验条数/抽样字段，成功后再标记迁出（保留备份窗口，细节实现时定；失败则拒启或只读告警——**不得静默丢任务**）。
2. 导入后：`/api/tasks/*`、WS、IM、自重启交接单行为与迁前一致（黄金样例全绿）。
3. `TaskStore` 对外能力收敛为 Store 的 Task 子集或薄适配；新代码不新增「只写 JSON」路径。

### 4. 文件位置与配置

- SQLite 文件落在 **dataRoot**（`PIEQI_HOME` / `~/.pieqi`）下约定路径（实现时写死约定，避免再引入易漏的 env）。
- 不把连接串做成「第二套业务配置真相」；PG 预留仅接口级，默认编译/部署仍 SQLite。

### 5. 与 ADR-0001

迁库改变的是**载体**，不是事实源身份：派生（FileChange、Checks、Outcome）仍从事件来；
Requirement 级事件同样 append-only，状态变更可审计（配合 `graph_version++`）。

## 被否掉的方案

| 方案 | 原因 |
|---|---|
| 协作平面继续纯 JSON 文件 | 查询/事务/依赖边弱；与 Task 双真相 |
| 先上 PostgreSQL | 本机单用户默认过重；接口预留即可 |
| Task 留 JSON、仅 Requirement 进 SQLite | 双真相；Promote / 列表过滤必漂 |
| 持久 GraphNode 表 | 已被 0021 否决 |

## 后果

- P0（0021 分期）以本 ADR 为施工单：接口 + SQLite + Task 迁移 + Requirement CRUD 落库。
- 回归：**短路径黄金样例**是迁库唯一放行门槛；长路径 UI 不挡 P0。
- `CONTEXT.md` / AGENTS 涉及「一任务一 JSON」的表述在实现落地后改为 Store/SQLite（实现 PR 同步改，免文档抢跑）。

## 实施分期（对齐 0021 §7）

| 阶段 | 本 ADR 交付 |
|---|---|
| P0 | Store 接口、SQLite schema、Task 迁移、Requirement CRUD 持久化 |
| P1+ | planned / depends_on / Issue 等列与约束随领域字段启用；不另开存储 ADR |
