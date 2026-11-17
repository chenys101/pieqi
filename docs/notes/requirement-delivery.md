# 需求落地约定（交付面 + 测试分层）

| 字段 | 值 |
|---|---|
| 状态 | **已定案**（2026-11-04） |
| 相关 | ADR-0021 协作平面；复盘样本需求 `0c2a7cb4-…`（V2.0.1） |
| 代码 | 拆分 prompt / confirm DoD 注入；ConfirmReplace 护栏；test `failed` → Issue；`integration_branch` |

## 问题（样本）

工作票在 worktree 标 `completed`，代码未进主干；test 票与实现并行；票面全绿 ≠ 需求交付。

## 交付模型

```text
Confirm 拆分 → 从默认基线拉「需求分支」req/<id8>，写入 Requirement.integration_branch
实现票 Start → worktree 以该分支为 BaseRef；prompt 追加需求上下文（含合入目标）
实现票 → **仅单元测试** → merge → 需求分支（本票才算交付）
依赖齐 → test 票：按需求分支编译，**另起隔离实例**做功能测试
功能测绿 → 需求可收口 → 再合 main
```

- **分支创建时机**：首次 Confirm（不是 Create、也不是 Start）。已有 `integration_branch` 则复用，不重建。
- 票 `completed` 应对齐「已合入需求分支」，不是会话跑完。
- AC / Requirement `done` 不认「票全绿」 alone。
- 通用：test 票另起**目标项目自己的**隔离运行环境（端口/配置/数据目录由该项目决定；勿占用现网实例）。
- DoD / pack **不得**写死编排层项目术语（如 `PIEQI_HOME`、`:3000`）；见 [dod-test-pieqi-home-leaks.md](dod-test-pieqi-home-leaks.md)。

## 测试分层（硬）

| 角色 | 允许 | 禁止 |
|---|---|---|
| 实现票（frontend/backend/fix/…） | 定向单测 / 组件测 + 能编译 | 起服务、完整链路、全包回归 |
| test 票 | 依赖实现**已合需求分支**后，编译并旁路起服务做功能测 | 与未完成实现并行开跑；无 `depends_on` |

test 按**波次**拆，写全 `depends_on`；不要「一实现票一空 test」。

## 回流

- 验证失败 → **Issue** → Fix → 重验 → Resolve（ADR-0021）。
- **已接线**：test 票 `failed` → 自动 OpenIssue（blocker，带 Fix）。
- test 报告含「总评：FAIL」时，轮末不得 `completed`（`completeAfterTurn` → `test report: FAIL`），避免会话跑完却不开 Issue。
- ConfirmReplace：若已有票离开 `planned`（已开跑/完成/失败），默认拒绝；须 `force: true`。

## 路径分级（S0 / S1 / S2）

与 [workflow-execution-efficiency.md](workflow-execution-efficiency.md) §4 对齐；创建时显式选路径，启发式可覆盖：

| 代号 | 形态 | 说明 |
|---|---|---|
| **S0** | 短路径 Task | 不进需求图、无流程包；UI 跳转「新建任务」 |
| **S1** | 轻量 Requirement | 少票 + 同需求实现并行默认 1；钉死流程包 |
| **S2** | 完整 Collaboration Plane | 多票 DAG；成本文案明示冷启动 / 人审 / 合入税 |

流程包（`workflow_id` + `workflow_version`）在创建时钉死；跑中有 progressed 票禁止换包。策略文案来自 Pack；L0 契约（JSON 校验 / test depends / L3 / 软完成否决 / 零 AC）仍在代码。

关单：有非 deferred AC 时须人验 pass 或 `evidence_ref`；票绿 alone 不够。零 AC 容器仍可 done（不宣称 testing）。

## 拆分助手须遵守

见 `buildSplitSuggestPrompt`（读 Pack `split_instructions`）/ `AppendDeliveryDoDFrom`（读 Pack DoD）注入正文。
