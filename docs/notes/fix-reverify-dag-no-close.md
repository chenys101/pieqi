# Fix→重验后 DAG 无法闭环

| 字段 | 值 |
|---|---|
| 状态 | **已定案 / 已落地**（方案 A） |
| 相关 | ADR-0021；`requirement-delivery.md`；另票 [planned-missing-agent-no-ui.md](planned-missing-agent-no-ui.md) |
| 代码 | `retargetDependsOn` / `RepairReverifyDeps`（`requirement_issue.go`）；调度入口幂等 repair |

## 现象

test FAIL → Issue + Fix；Fix 完成后新建 reverify，不 Resume 原测。  
终验 `depends_on` 仍挂原测 → 原票永久 `failed` ⇒ `DepsSatisfied` 永假 ⇒ 终验永远 `planned`。

## 根因

回流语义正确（失败不假绿；重验用新票）。缺口是 **DAG 未跟随回流**：依赖边仍指向死票。

## 目标

1. 源测 failed 且已派 Fix/重验时，下游依赖必须能被满足  
2. 重验绿并 Resolve 后，任务图与 Issue 状态一致可读  
3. Fix/reverify 启动前 agent 必达可与缺 agent UI 票协同，本票以 DAG 闭环为主

## 方案选择

| 方案 | 结论 |
|---|---|
| **A. spawnReverify 时改挂** DependsOn：`SourceTaskID` → `ReverifyTaskID` | **采用**。图可读；假绿仍等重验 `completed` |
| C. `DepsSatisfied` 特例（failed 源测 + Issue resolved + reverify completed） | 不采用作主路径：边仍指向死票，图与账本不一致 |
| B. Resume 源测 | 否：与 failed 账本叙事冲突 |
| D. 约定终验不 depends_on test | 挡不住已落地旧图 |

旧图兜底：`RepairReverifyDeps` 按 Issue 的 Source→Reverify 幂等改挂；`StartReadyInRequirement` / `scheduleReadyDependents` 入口调用。

## 非目标

- 不取消 总评 FAIL → failed + Issue  
- 不把 MarkDone 改成强制终验 completed  
- 不改 `completeTask` / 软完成判据  
- 不夹带 planned 缺 agent UI 大改（另票）

## 验收

1. 源测 failed + Fix + reverify 绿后，原 depends_on 源测的终验/下游 `DepsSatisfied` 为真  
2. Issue Resolve 与任务图（边指向重验票）一致可读  
3. 重验未绿前不得当源测绿（假绿门禁）

回归：`TestFixReverify_RetargetsDownstreamDeps`、`TestRepairReverifyDeps_IdempotentOldGraph`。
