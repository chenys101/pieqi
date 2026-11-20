# 失败重做模型（Execution 子系统）

| 字段 | 值 |
|---|---|
| 状态 | **已定案（Execution）**；**不是**当前主升级 |
| 上级 | [plan-engineering.md](plan-engineering.md) / ADR-0023 |
| 日期 | 2026-11-18（11-19：归入 Execution；PE 优先） |

「原票永久 failed → 下游永不起跑」是状态终态 + DepsSatisfied 只认 completed + 边引用执行实例 三约束撞车。根治是 Node（职责）与 Attempt（执行）拆开。

主升级是 Plan Engineering。本文 N0/N1 **另票**，不阻塞 PE V1。改图只经 PlanChange→新 PlanRevision。不管旧库迁移。

详见会话定案：同 node 新 attempt；非 test 只建 major；要 waive；AttemptBaseRef=合入 tip；禁止 failed 就地改 completed。
