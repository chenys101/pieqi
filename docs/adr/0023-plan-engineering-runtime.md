# ADR-0023: Plan Engineering Runtime（计划工程）

- 状态：**提议中（Proposed）**（2026-11-19）
- 日期：2026-11-19
- 相关：[plan-engineering.md](../notes/plan-engineering.md)、[2026-11-19-plan-engineering-v1.md](../superpowers/plans/2026-11-19-plan-engineering-v1.md)
- 关系：升级 ADR-0021 长路径计划质量侧；不推翻短路径与方案 B；失败重做属 Execution（另票）

## 背景

0021 解决多 Task 挂需求 + 依赖边，但仍易从口述跳到 DAG；假设当事实执行；静默改图与 §3.4 冲突。瓶颈在 Execution **之前**的规划质量。

## 决策

1. 三 Runtime 分立；短路径不强制进 PE。
2. 长路径只执行已 Publish 的不可变 `PlanRevision`（经 Execution Pin）。
3. 断言分轨 Decision/SystemFact/Assumption/OpenQuestion；Publish 门禁。
4. Pipeline 是产品能力，不是单个 Planner Agent。
5. V1 不引入 GraphNode 表；Publish 可物化为 planned Task（NodeID 钩子）。
6. **不管旧数据迁移**；N0/Attempt 全量另票。

## 后果

计划可门禁/审计/钉扎；与 0021 confirm UX 短期并存；审计以 Revision 为准避免双真相。
