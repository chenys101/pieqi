# CONTEXT.md

Pieqi **领域词汇表**（术语唯一真相）。人类产品说明看 [`README.md`](../README.md)；agent 操作规程看 [`AGENTS.md`](../AGENTS.md)。

改代码涉及领域概念时先对这里的词；与既有 ADR 冲突时新增 `docs/adr/00NN-*.md`，不要静默改词义或复活已废止词。

范围：Feedback（Task 内闭环）+ Collaboration Plane（需求级闭环）+ 明确的 Non-goals。

## Language

### 核心领域

**Feedback**:
Pieqi 的 Observe→Verify→Control→Intervene 层；围绕 Agent Task 的展示、验证、控制与干预能力集合。
_Avoid_: 输出面板, 日志

**TaskEvent**:
Agent Task 执行流中的一条持久化事件（text / user / thinking / tool_use / tool_result / status / rewind / model_switch）。Feedback 的事实源。
**只补不删**：工具名/入参后到时合并进已有的那条 `tool_use`（不追加空壳），因此同一次工具调用的事件永远只有一条。
_Avoid_: 日志条目

**Usage（上下文用量）**:
Agent 上报的「此刻上下文有多满」快照（used / size，可选 cost）。来自 ACP 的 `usage_update`，是**瞬时量而非累计量**——一轮内可到达多次，每次都覆盖前值。**不是** TaskEvent：不进事件流，持久化在 `Task.Usage`，经轻量 `task_usage` WS 推送。数字全部由 agent 给，pieqi 不估算。
_Avoid_: token 统计, 计费, 流量

**Image Prompt（图片 prompt）**:
随一轮 prompt 内联发出的图片（ACP `ContentBlock::Image`）。能否发由对端 agent 决定（须声明 `promptCapabilities.image`）；pieqi 只校验与转发。本体是**纯 base64**（无 `data:` 前缀）、只在内存过手；任务只留元数据（`TaskEvent.Images`：mime / bytes / hash）。见 ADR-0010。
_Avoid_: 附件上传, 文件, 已废止的 Visual/Screenshot

**Turn**:
一次用户消息边界内的执行段：从 EventUser(N) 到 EventUser(N+1) 之间的所有事件。Turn #1 从 Task 初始 prompt 起算。内部工具循环不产生新 Turn。是变更分组、Checkpoint 与 Rewind 的基准单位（ADR-0003）。
_Avoid_: step, iteration, agent turn

**FileChange**:
从 TaskEvent 派生的一条单文件变更（create / modify / delete）。语义是「Agent 声明改了什么」；用户 Task 开始前的未提交修改不属于 FileChange（ADR-0001）。
两个前提：(1) 工具名表**按 agent 分开**——claude 用 `Edit/Write`，dsh 用 `edit/write`，查表前不做大小写归一；(2) 若该声明的**每一次**调用都失败（`is_error`）就不算变更。
_Avoid_: git change

**Baseline**:
Task 开始时工作区代码状态的参照（Git HEAD）。只读，绝不写入用户分支（ADR-0002）。
_Avoid_: snapshot, checkpoint

**Checkpoint**:
Rewind 的恢复资产，只覆盖被 Agent 实际修改过的文件（不是整个工作区快照）。绝不包含用户 Task 开始前的未提交修改（ADR-0002）。
_Avoid_: 工作区快照, commit

**Rewind**:
把代码恢复到指定 Checkpoint。Timeline 永不删除；Rewind 以事件进入 Timeline，保持可审计。
_Avoid_: undo, git checkout

**Change Summary**:
一个 Turn 的变更人读摘要（文件数、增删统计、要点）。P0 由规则生成。
_Avoid_: commit message

**Diff**:
对单文件或一组文件的变更展示。**回顾性** Diff 来自 Checkpoint/Baseline；**前瞻性** Diff（审批前）来自工具入参。
_Avoid_: patch

**Preview**:
把 Agent 改完的代码跑起来经鉴权代理在手机上看。状态：unavailable / available / recommended / running。
两种形态：**dev server**（spawn，任务终态回收）与**静态产物**（候选目录有 `.html` 时进程内 serve，终态可回看，ADR-0017）。
静态目录是一组候选（`.pieqi/preview` 权威，另有 `proposals/` 等）；认不出时 `PreviewStatus` 用 `convention` / `hint` / `suspected` 指路，不要静默成裸 unavailable。
_Avoid_: 代码预览, Web IDE, `python -m http.server`

**产物（Artifact）**:
Agent 产出的可直接打开的文件（html / markdown / image / pdf）。变更 =「改了什么」；产物 =「有什么能看」。默认可访问；打开 URL 由服务端算；html/svg 经 `/file` 时必须 `CSP: sandbox`（不给 `allow-same-origin`）（ADR-0018）。
入口在「变更」文件清单行内，不在预览面板另列；时间线不重复列产物（ADR-0019）。文件清单按目录分组、**默认全展开**（不是默认收起）。
_Avoid_: 附件, 下载区, 文件管理

**DecisionKind**:
`waiting_input` 的两种决策：`approval`（工具权限，agent 进程仍活，resolve 续跑）与 `choice`（文本选项，进程已死，resume 新进程）。启动恢复必须维持这个区分。
_Avoid_: 把两种 waiting_input 当成同一种

**ToolKind / 风险档**:
审批与免审共用的工具类别：L0 `read/search/fetch/think`、L1 `edit/move`、L2 `execute/switch_mode/other`、L3 `delete`。未知/空 kind → L2。免审认 ToolKind，不认工具名。见 ADR-0008 / ADR-0015。
_Avoid_: 按工具名字符串做免审唯一真相

### 控制闭环（P1）

**Approval**:
waiting_input 时的权限/决策中断（approval / choice）。审批卡可进入前瞻性 Diff。
_Avoid_: 权限弹窗（范围过窄）

**Check**:
验证命令的统一结果。优先复用 Agent 已跑过的 test/lint/build（ADR-0005）。
_Avoid_: CI（外部概念）

**Task Outcome**:
Task 最终的结构化结果（状态、Changes、Preview、Checks、Issues、Actions）。比完整 Timeline 更适合手机端验收。
_Avoid_: summary（与 Change Summary 冲突）

**Evidence**:
多个反馈 Provider 结果的聚合，可挂到 Task / Turn / Outcome，并作为继续工作的上下文。
_Avoid_: report

**Continue**:
用户带着当前 Evidence 让 Agent 继续（由**后端**把证据整理成 Agent Context，ADR-0004）。
_Avoid_: 续问（不含证据语义）

### 推送与细粒度回退（仍有效；原 P2 存活项）

**Evidence Push**:
把 Task Outcome / Evidence 推送到 IM / Webhook。通过 Notification Provider 抽象，不绑定具体 IM。
_Avoid_: 消息轰炸

**File Rewind**:
把单个文件恢复到指定 Turn 的状态（Rewind 的文件粒度）。
_Avoid_: git restore

### Collaboration Plane（需求级闭环）—— ADR-0021（存储 ADR-0022）

> Feedback 解的是**单个 Task 内**闭环。Collaboration Plane 是超集：需求拆成若干 Task（含 planned），按依赖执行，验证失败经 Issue 回流。施工以 ADR-0021 为准；ADR-0014 为历史提案（含已取消的 GraphNode）。

**Short Path（短路径）**:
人/IM → 单个 `Task`（`RequirementID == ""`）→ Runner / Feedback / 审批。一等公民；长路径对短路径 nil-safe。
_Avoid_: 强制先建需求

**Long Path（长路径）**:
人/IM → `Requirement` → `SplitProposal` → 人 confirm → planned Task + `depends_on` → 依赖满足后开跑 → AC 全绿且无开放 Issue → done。
_Avoid_: 静默改图, 未确认提案直接落 Task

**Requirement（需求）**:
文件夹 + 验收总账：多个 Task、AC / Issues / 需求级事件与 `graph_version`。绑定显式 project/cwd。零 AC 时仅作挂靠容器，**不能**宣称 testing 闭环完成。
_Avoid_: 工单, ticket, 项目（Project 另有所指）, GraphNode

**planned Task（计划态任务）**:
已纳入需求图、尚未调度的 Task；**不跑 agent**。计划格 = planned Task，不再另建 GraphNode。仪表盘默认过滤 planned。
_Avoid_: GraphNode, 子任务列表挂在单 Task 下

**SplitProposal（拆分/展开提案）**:
split / expand 的短暂草案（内存）；confirm 前不进 Task 表。API：`PUT/GET/DELETE .../proposal`，`POST .../confirm`。校验 fail closed。
_Avoid_: 计划（与 Plan Mode 冲突）, 未确认即落库的 Task

**Task 依赖边（depends_on）**:
同需求内前置 Task id 的 DAG。无独立 graph_nodes 表。
_Avoid_: Task Graph 作为与 Task 并列的实体名（旧 0014 用语）

**Acceptance Item / AC（验收标准）**:
Requirement 上的可验收条目。Task 用 `covers_ac` 声明覆盖；Issue 钉同一 AC。
_Avoid_: 需求点, 功能点

**Issue（问题单）**:
结构化可派活缺陷：severity / covers_ac / 证据链 / fix_task_id。验证失败 → 再开发**只经 Issue**；不改写已终态 Task 冒充再跑。
_Avoid_: bug（口语）, 报错, 改写旧 Task Timeline

**Store / SQLite**:
Task 与 Requirement 的持久化落在 `~/.pieqi/tasks/pieqi.db`（ADR-0022）。`TaskStore` 仍是内存索引；遗留 `tasks/*.json` 启动时迁入。禁止 Task/Requirement 双库双真相。
_Avoid_: 一任务一 JSON 为现行事实源, sessions/ 目录

### 视觉反馈（P2）—— 已废止

> Screenshot / Browser Console / Network Error / Visual Evidence 于 2026-10-15 整体移除（ADR-0012、ADR-0013）。不再是领域概念，禁止复活字段或 API。
>
> 四个 preview 子路径（`capture` / `console` / `network` / `screenshots`）显式 **410 Gone**（勿改 404：会掉进预览反代，症状与「功能没了」分不清）。
>
> **Preview 本身（P1）不受影响。** 采集服务留档在 `pieqi-ui-test/services/visual-capture`。

### 探索（P3）—— 冻结

> 下列项依赖已废止的截图/视觉层，**当前不施工**；保留词条以免与 Non-goals 混淆。

**DOM Inspection** / **Region Annotation**:
从截图反查 DOM / 框选映射为视觉输入。—— 冻结。
_Avoid_: 当作现行能力

**Task Replay**:
按 Task → Turn → Change → Check → Evidence 重放。—— 未立项。
_Avoid_: 录屏回放

### 不做（Non-goals）

**Web IDE 化**:
可写文件浏览器 / 直接编辑文件属于 IDE。查看文件属于 Feedback。
_Avoid_: 编辑模式

**Computer Use / 云桌面 / 真终端工作台**:
会把 Pieqi 从 Agent Control Plane 带向 Computer Agent。
_Avoid_: 虚拟桌面, PTY 工作台

**完整 Browser Automation**:
Preview 的目标是 Observe，不是 Control Browser。
_Avoid_: 浏览器控制

**Task 录屏**:
成本高、存储重；与「不做 Computer Use / 完整浏览器自动化」同一产品边界（ADR-0007）。
_Avoid_: 屏幕录制
