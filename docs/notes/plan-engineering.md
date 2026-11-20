# Plan Engineering：Pieqi 重大升级（计划工程 Runtime）

| 字段 | 值 |
|---|---|
| 状态 | **已定案（设计）**；ADR 草案 [0023](../adr/0023-plan-engineering-runtime.md)；V1 施工 [2026-11-19-plan-engineering-v1.md](../superpowers/plans/2026-11-19-plan-engineering-v1.md) |
| 日期 | 2026-11-19 |
| 决策 | **1C + 2X**：设计笔记 + ADR + V1 施工；PE 优先；失败重做降为 Execution 子能力；N0 另票不挡 PE |
| 相关 | ADR-0021、ADR-0022、[failure-rework-model.md](failure-rework-model.md) |

## 0. 一句话

Pieqi 下一跃迁是 **Plan Engineering Runtime**：不确定意图 → 可审计、过门禁、可版本发布的 `PlanRevision` → 调度执行；Discovery/失败经 PlanChange 回 PE。错误 Plan + 再好的 Attempt = 更高效返工。

## 1. 三 Runtime

| 层 | 职责 | 姿态 |
|---|---|---|
| Plan Engineering | 做什么、为何、计划是否够格 | **主升级** |
| Agent Execution | 谁做、Attempt/Worktree | 消费已 Pin 的 Revision；Node/Attempt 见 failure-rework |
| Feedback & Governance | 结果/审批/改计划 | PlanChange / Evidence |

**2X**：不管旧迁移；N0 止血另票。

## 2. Plan 五层

L0 Intent → L1 Business → L2 Solution → L3 Engineering（PlanRevision）→ L4 Execution。

## 3. 认知分轨（Publish 硬约束）

`Decision` | `SystemFact` | `Assumption` | `OpenQuestion`

未关闭 OpenQuestion、未 `accepted_risk` 的 Assumption → 不得 Publish。

## 4. V1 做 / 不做

做：Plan Artifact、分轨、门禁、不可变 Revision + Pin、物化 planned Task、最小 PlanChange、与 confirm 衔接。  
不做：全自动建模、N0/Attempt 全量、GraphNode 表、旧库迁移。

## 5. 已拍板

Q1 PE 是重大升级；Q2 不管迁移；Q3 失败重做另票；Q4 多阶段流程非单 Planner；Q5 调度只吃已 Publish Revision；Q6 禁止执行静默改边；Q7 V1 不拆 GraphNode 表。
