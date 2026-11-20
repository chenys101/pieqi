# Plan Engineering V1 Implementation Plan

> 总规：[plan-engineering.md](../../notes/plan-engineering.md)、[ADR-0023](../../adr/0023-plan-engineering-runtime.md)。不管旧迁移；N0 另票。

**Goal:** Draft → 门禁 → 不可变 PlanRevision + Pin → 物化 planned Task。

**顺序:** 类型 → 门禁 → 存储/Publish → 物化 → API → UI → CONTEXT。

| 路径 | 职责 |
|---|---|
| `internal/model/plan.go` | 领域类型 |
| `internal/core/plan_gate.go` | CanPublish |
| `internal/core/plan_store.go` | Draft/Publish/Pin |
| `internal/core/plan_materialize.go` | Revision → planned Task |
| `internal/api/plan.go` | HTTP |
| `web/.../PlanEngineeringPanel.vue` | 需求页面板 |
