# Pieqi Feedback Context

Pieqi 的「反馈体系」：围绕 Agent Task 的 Observe → Verify → Control → Intervene 层。
用户看到 Agent 改了什么（Changes）、验证改得对不对（Preview / Checks）、必要时回退（Rewind）、带证据继续（Continue）。
它不是一个「显示 Agent 输出的面板」。

## Language

### 核心领域

**Feedback**:
Pieqi 的 Observe→Verify→Control→Intervene 层；围绕 Agent Task 的展示、验证、控制与干预能力集合。
_Avoid_: 输出面板, 日志

**TaskEvent**:
Agent Task 执行流中的一条持久化事件（text / user / thinking / tool_use / tool_result / status / rewind）。Feedback 的事实源。
_Avoid_: 日志条目

**Turn**:
一次用户消息边界内的执行段：从 EventUser(N) 到 EventUser(N+1) 之间的所有事件（含其间所有 Agent / Tool 事件）。Turn #1 从 Task 初始 prompt 起算。内部工具循环不产生新 Turn。Turn 是变更分组、Checkpoint 与 Rewind 的基准单位。
_Avoid_: step, iteration, agent turn

**FileChange**:
从 TaskEvent 派生的一条单文件变更（create / modify / delete），记录路径与关联的 tool_use。语义是「Agent 声明改了什么」；用户 Task 开始前的未提交修改不属于 FileChange。
_Avoid_: git change

**Baseline**:
Task 开始时工作区代码状态的参照（Git HEAD）。用于累计真实 Diff 的基准；只读，绝不写入用户分支。
_Avoid_: snapshot, checkpoint

**Checkpoint**:
Rewind 的恢复资产，只覆盖被 Agent 实际修改过的文件（不是整个工作区快照）。Checkpoint #N 对应序号 N 的文件状态；用于把代码安全恢复到该状态。绝不包含用户 Task 开始前的未提交修改。
_Avoid_: 工作区快照, commit

**Rewind**:
把代码恢复到指定 Checkpoint 对应的状态。Timeline 永不删除；Rewind 以事件形式进入 Timeline，保持历史可审计。
_Avoid_: undo, git checkout

**Change Summary**:
一个 Turn 的变更人读摘要（文件数、增删统计、要点）。P0 由规则生成。
_Avoid_: commit message

**Diff**:
对单文件或一组文件的变更展示。**回顾性** Diff 来自 Checkpoint/Baseline 对照；**前瞻性** Diff（审批前）来自工具入参（Edit old/new）。
_Avoid_: patch

**Preview**:
项目的运行态反馈能力：把 Agent 改完的代码真实跑起来（dev server），经鉴权代理在手机上查看。可选能力：unavailable / available / recommended / running。
_Avoid_: 代码预览, Web IDE

### 控制闭环（P1）

**Approval**:
waiting_input 时的权限/决策中断（approval / choice 两种）。审批卡直接可进入前瞻性 Diff。
_Avoid_: 权限弹窗（范围过窄）

**Check**:
验证命令的统一结果（name / status / command / duration / exitCode / output）。第一阶段优先复用 Agent 已跑过的 test/lint/build。
_Avoid_: CI（外部概念）

**Task Outcome**:
Task 最终的结构化结果（状态、Changes、Preview、Checks、Issues、Actions）。比完整 Timeline 更适合手机端验收。
_Avoid_: summary（与 Change Summary 冲突）

**Evidence**:
多个反馈 Provider 结果的聚合，可挂载到 Task / Turn / Outcome，并作为继续让 Agent 工作的上下文。
_Avoid_: report

**Continue**:
用户带着当前 Evidence 让 Agent 继续的下一条消息（由后端把证据整理成 Agent Context）。
_Avoid_: 续问（不含证据语义）

### 视觉反馈（P2）

**Screenshot**:
Preview 页面的截图，可直接作为视觉证据。P2 由浏览器采集服务（Playwright）生成。
_Avoid_: 录屏

**Browser Console**:
Preview 页面运行时采集到的 console.error / console.warn。
_Avoid_: 日志流

**Network Error**:
Preview 页面运行时的 4xx / 5xx / failed 请求。
_Avoid_: 抓包

**Visual Evidence**:
Screenshot / Console / Network 聚合成的浏览器证据，可挂载到 Evidence 并进入 Continue。
_Avoid_: 截图文件（无证据语义）

**Evidence Push**:
把 Task Outcome / Evidence 推送到 IM / Webhook。通过 Notification Provider 抽象，不绑定具体 IM。
_Avoid_: 消息轰炸

**File Rewind**:
把单个文件恢复到指定 Turn 的状态（Rewind 的文件粒度）。
_Avoid_: git restore

### 探索（P3）

**DOM Inspection**:
从截图像素反查页面 DOM 节点/选择器。
_Avoid_: 浏览器控制台

**Region Annotation**:
用户在截图/页面上框选区域，映射为 DOM 区域并作为给 Agent 的视觉输入。
_Avoid_: 标注涂鸦

**Task Replay**:
按 Task → Turn → Change → Check → Evidence 重放 Agent 如何把项目改成现状。可为未来 Project Brain 提供数据基础。
_Avoid_: 录屏回放

### 不做（Non-goals）

**Web IDE 化**:
可写文件浏览器 / 直接编辑文件属于 IDE，不是 Feedback。查看文件属于 Feedback。
_Avoid_: 编辑模式

**Computer Use / 云桌面 / 真终端工作台**:
会把 Pieqi 从 Agent Control Plane 带向 Computer Agent；基础设施与产品边界明显膨胀。
_Avoid_: 虚拟桌面, PTY 工作台

**完整 Browser Automation**:
Preview 的目标是 Observe，不是 Control Browser。
_Avoid_: 浏览器控制

**Task 录屏**:
成本高、存储重；截图已覆盖当前验收需求。
_Avoid_: 屏幕录制
