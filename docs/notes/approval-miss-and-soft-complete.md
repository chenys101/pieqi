# 优化方案登记：审批卡未弹出 + 轮次被软完成

| 字段 | 值 |
|---|---|
| 状态 | **已定案**（需求 `bb2f5ab5` / V2.0.1；施工以本文件为准） |
| 登记日 | 2026-11-03 |
| 定案日 | 2026-11-04 |
| 触发会话 | `662450a6-93f0-403f-8df8-819cd8b17895`（协作平面双路径 / cursor ACP） |
| 相关 | ADR-0015（审批降噪）、`PermissionWire`、`completeTask` 轮末兜底、Plan 批准 UI 规则 |

## 现象（用户侧）

同一会话里**多次**出现：

1. **该审的步骤没有弹出审核卡**（用户感知「没弹窗」）；
2. 随后会话**默认就关了**——任务变成 `completed`，计划项仍 `in_progress`，最后一条 `tool_use` 没有 `tool_result`。

不是「用户点了拒绝」，也不是「计划做完了」。

## 证据（本机会话落盘 + 日志）

会话 `662450a6-…`（agent=`cursor`，约 03:03–09:59）：

| 时刻 | 后端事实 | 用户感知 / 后果 |
|---|---|---|
| 多次 | 日志有 `approval shown (waiting for human decision)`，`CurrentDecision` 曾被挂上 | 界面**未稳定弹出**可点的审核卡（「未弹审核」） |
| 09:55:28 | 最后一张卡：`git checkout HEAD -- …task_store_test.go`（`call-094b663d-…`） | **无**对应 `approval settled` / intervention |
| 09:55:29→09:59:22 | 约 4 分钟空窗后 agent 又吐 thinking/text，再发新 `tool_use` | 悬挂工具从未回结果 |
| 09:59:29 | `status=completed`；plan 末项仍 `in_progress` | 「默认就关闭了」 |

后端侧：`PermissionWire` 认为在等人类；轮次却被标成成功终态。两层语义拧着。

## 根因拆成两条（不要揉成一个 bug）

### A. 审核卡未可靠露出（Control / UI）

后端已 `approval shown` ≠ 前端用户已看见并可点。

可疑链路（登记用，施工前再钉死）：

- WS `task_updated` / `CurrentDecision` 到达与当前会话页焦点不同步；
- 多会话并行时审批卡挂在「非当前打开」的任务上（同日日志里可见另一任务 `f9d0060f-…` 交错审批）；
- sticky / banner 被滚动或布局遮挡；
- Plan 批准与工具审批两条卡形态不一致（已有 `.cursor/rules/pieqi-plan-accept-ui.mdc`）。

**判据**：日志已有 `approval shown`，但用户未操作、也无 settled —— 优先查「卡没露」而不是「用户没点」。

### B. 轮次软完成（状态机）

现行规则（`task_runner` ACP 轮末）：

> Prompt **正常返回** + 任务**已有产出** → `completeTask`  
> **不看** plan 是否做完、**不看**是否仍有未决 permission / 悬挂 `tool_use`。

对「多轮对话里的一轮正常结束」这是刻意语义；  
对「卡在未点审批 / 宿主自行收轮」则把**中断**记成**成功**，放大了 A 的伤害：没弹卡 → 没人点 → Cursor 收轮 → pieqi 标 completed。

同段代码已承认轮末会留下「没人应答」的卡，靠 `clearStaleDecision` 擦掉——说明产品已知「轮结束 ≠ 审批闭环」。

## 优化目标

1. **该审必见**：只要后端进入人工审批，当前用户视口内必须有不可忽略的决策入口（本会话页 + 全局待办）。
2. **中断不等于成功**：未决 approval、或末条 `tool_use` 无结果时，不得静默 `completed`。
3. **plan 不作闸门**：todos 仍不驱动 `TaskStatus`；但 UI 上 completed + 未完成 plan 要能区分「轮次结束」vs「目标完成」。

---

## 定案（V2.0.1）

### 终态选型（三选一 → 已选）

| 候选 | 结论 |
|---|---|
| **A. `failed` + 结构化原因** | **采纳（P0）** |
| B. 新状态 `interrupted` | **否决（本需求）**——状态表 / Dashboard / IM / 重启恢复 / 前端枚举爆炸；与「自重启 interruptedByRestart」易混名 |
| C. `completed` + `finish_reason` | **否决**——成功终态被污染；列表/通知仍当「完成」；与目标「中断≠成功」直接冲突 |

**采纳细则**：

- 走现有 `failTask`，`Task.Error` 写**稳定、可检索**的原因前缀（实现票不得随意改文案，单测钉死）：
  - pending permission 未 settle 且 Prompt 已返回 → `approval unresolved`
  - 事件流末条相关为 `tool_use` 且无匹配 `tool_result` → `tool hung without result`
- `failed` **仍可 Resume / 续问**（`isResumable` 已含 `TaskFailed`）——中断不是死档。
- **不**引入 `finish_reason` 字段；**不**新增 `TaskStatus`。
- 若日后产品要「待办优先露出中断 vs 真失败」，另开需求加 `interrupted` 或标签；**不在本需求夹带**。

### P0 — 软完成收紧（后端 · 必做）

在「有产出 → `completeTask`」之前增加中断探测（ACP / print 轮末进入 `completeTask` 的路径；choice 路径 B **不变**）：

| 条件 | 终态 |
|---|---|
| wire 仍有未 settle 的 pending permission，且 Prompt 已返回 | `failed`，Error=`approval unresolved`（可附 reqID） |
| 末条相关事件为 `tool_use` 且无匹配 `tool_result` | `failed`，Error=`tool hung without result` |
| 正常 end_turn、无悬挂、无 pending | 维持今日 `completed` |

**硬否决（实现票不得破）**：

- ❌ 用 plan 全 `completed` 才允许 `completeTask`
- ❌ 未弹卡时自动 `approve`
- ❌ 缩短默认审批超时冒充修复
- ❌ 把 choice 路径 B 改成 failed

### P1 — 该审必见（前端 · 必做）

**露出范围（钉死）**：

| 场景 | 必须 |
|---|---|
| 用户正在看该会话页 | `ApprovalBanner` / `ApprovalCard` 在视口内可见可点（含滚动锚定）；不可被时间线滚走后「像没弹」 |
| 用户在看别的会话 / 列表 | 侧栏或顶栏**待办计数**（复用已有 notify / other-tasks 通道，不新造通知体系） |

**本需求不做**：

- 改 Cursor 宿主弹窗
- 扩大免审白名单 / TrustedRecipes
- 强制 IM 推送（已有 notify 路径可复用则复用；缺则不作为 AC）

可选（非 AC）：前端 `decision_rendered` 与后端 `approval shown` 对照日志，便于以后告警。

### 附录项与本需求边界

| 项 | 本需求？ | 说明 |
|---|---|---|
| 空/残缺审批文案（`acp permission has no tool/command text`） | **否** | 另册；不进 ac1–ac6 |
| 超时早感知（满 30min 前催办 / 早失败提示） | **否** | 另册；**禁止**用缩默认超时冒充 |
| Cursor 连接抖动 / `peer disconnected` | **out-of-scope** | 人读失败面另开 |
| 额度耗尽文案 | **out-of-scope** | 同上 |
| 再降噪免审名单 | **否** | ADR-0015/0020 |
| 协作平面 / SQLite | **否** | 已落地，本需求不改 |

### 验收锚点（对应需求 AC，详见 `v2.0.1-flow-optimization-design.md`）

1. 卡住 L2 审批不点 → 宿主收轮后**不得**无原因 `completed`（应为 `failed` + 上表原因之一）。
2. `approval shown` 后当前会话可见可点决策 UI；非当前会话有待办计数。
3. 正常无审批短任务仍 → `completed`；choice 路径 B 行为不变。

---

## 附录：历史日志扫描（2026-11-03）

范围：`~/.pieqi/logs/pieqi-*.log`，重点看 settled 日志可靠后的窗口
（约 10-23～11-03；Nov 窗口单独复核）。**不是**全量审计，是找「还要不要另开优化项」。

### 审批漏斗（Nov：11-01～11-03 + 10-31）

| 指标 | 数量 |
|---|---|
| `approval shown` 唯一 req | 284 |
| 随后 settled | 252（**88.7%**） |
| 随后 timeout（多满 30min） | 24（**8.5%**） |
| shown 后既无 settle 也无 timeout | 8（孤儿，偏宿主收轮 / 进程死） |

结论：主病仍是本笔记的 A/B；**超时约一成**是同一病的量化后果（卡没点 → 空等半小时）。  
更早窗口（含 `f74bcb09`）shown≫settled 的尖峰是「重复投递 / no pending」旧伤，幂等 settled 表已治，**不必再开新项**，只监视 Nov 后是否复发（本窗 `no pending` 已近乎消失）。

### 建议另册 / 顺手的项（按优先级）

| 优先级 | 信号 | 量级（扫描窗） | 建议 |
|---|---|---|---|
| **另册** | 审批满 30min `TIMED OUT` | Nov ~8.5% shown | 露出失败要早感知；**不要**缩默认超时 |
| **另册** | `acp permission has no tool/command text` | ~41（11-02 一天 25） | 加长回填窗 / UI 至少展示 title |
| **另册** | Cursor ACP `peer disconnected` | 11-01 一天量级数十 | 有限退避 + 人读失败文案 |
| **另册** | `account quota exhausted` | 5 次 | 前端/IM「额度用尽」 |
| **不必新开** | `no pending permission` 重复 resolve | 旧窗 | 幂等 settled 已治 |
| **不必新开** | Plan 批准弹窗 | 规则旁路 | 继续走 pieqi-plan-accept-ui |

### 扫描后对范围的修正（定案后）

- 空文案 / 超时早感知：**不并进本需求 AC**（与上表「附录项」一致）。
- Cursor 连接 / 额度：**明确 out-of-scope**。
- P0 先于 P1 止血「默认关闭」；P1 可并行，互不阻塞合入顺序以外的依赖。
