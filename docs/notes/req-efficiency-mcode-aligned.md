# pieqi 需求执行效率与质量方案（对齐 mcode-stack + bb2f 复盘）

| 字段 | 值 |
|---|---|
| 状态 | **提案**（2026-11-12） |
| 复盘样本 | `bb2f5ab5`「V2.0.1 流程优化」；前序空转 `0c2a` / `f920` / `24817` |
| 对照开源 | [MiniMax-AI/mcode-stack](https://github.com/MiniMax-AI/mcode-stack)（本地 clone：`D:\code\go\mcode-stack`） |
| 相关已定案 | [`requirement-delivery.md`](./requirement-delivery.md)、ADR-0021、[`approval-miss-and-soft-complete.md`](./approval-miss-and-soft-complete.md) |
| 目标读者 | 改协作平面 / 拆分 / 完结闸的人 |

## 0. 一句话结论

pieqi 需求 DAG 赢在「可审计、可挂分支、可回流」；在 **算力、墙钟、质量闸** 上，对中小需求常输给「plan → 单会话执行」。根因不是缺票，而是：**按角色/领地拆会话（冷启动税）+ 默认可高并行（审批与合入税）+ 票绿即可（证据闸缺失）**。

mcode-stack 的核心主张可直接迁移：

> **子代理是隔离上下文的工具，不是按工种分活的工单系统。先管上下文，再谈并行；上线≠完成，有证据才关闭；人反复当审批环时，应抬人出环（lifting），而不是再加会话。**

## 1. bb2f 样本数字（问题钉死）

| 指标 | 值 | 解读 |
|---|---|---|
| 墙钟（工作票） | ≈ 11.6 h | 17:51 → 次日 05:27 |
| 机时加总 | ≈ 14.9 h | `cpu/wall ≈ 1.27`，并行几乎没换墙钟 |
| 峰值并发 | 4 | 默认 `max_concurrent_per_project=4` 顶满 |
| 人审 | 176× approve + 5×追问 | 审批成主路径税 |
| 终验票 | 533 min / 85 approve | 间隔合计 ≈499 min，含 196/90 min 空窗 |
| AC / Issue | 表空 | `acceptance=null`，`issues=0` |
| 需求状态 | 仍 `active` | 代码已合 main（`cbb4efb`），完结靠人肉且失败过一次 |
| 前序浪费 | 3 条同文案 requirement + `0c2a` 19 张 cancel | 重复立项 + 整图作废 |

用户体感「比 plan 更耗算力、质量与速度都没提升」与数据一致。

## 2. mcode-stack 原则 → pieqi 映射

| mcode 来源 | 原则 | pieqi 落点 |
|---|---|---|
| Notes 01 + `subagent-split-check` | 默认主环；仅在**测到的上下文劣化 / 可丢弃并行调研 / 工具指令冲突**时拆 | 改 `buildSplitSuggestPrompt`：**默认少票、默认串行合入面**；禁止「按 FE/BE 工种」硬拆 |
| `review-capacity-check` | 审不过来就别再开并行 | Confirm / Start 前：`pending_review` 队列 + 并发上限按「待人审高风险项」动态收紧 |
| `agent-brief` | 委派前写 stakes / Done=可检查项 / 模式 | 每票 prompt 强制 Brief 卡；缺 `Done=` 拒绝 confirm |
| `evidence-first-review` | 先证据后总结；票绿是主张不是证据 | test / mark-done：必须粘贴命令与输出；无独立证据不得 `done` |
| `req-tpm`（intake→outcome） | 受理就写死预期效果；ship≠close；有证据才关 | Requirement 创建强制 Expected Outcome；`can_mark_done` 改认 AC 证据矩阵 |
| `req-design` | ≥2 选项 + 决策记录 + 检查点 | design 票产出落 `docs/notes` 或 requirement payload，后续票只读该文档 |
| `handoff-doc` | 状态进文件，会话可清 | 需求级 `handoff.md`（或 SQLite 字段）；票间传结论，不传整段 transcript |
| `lifting-loops` | 识别人当 Approve/Judge/Retry 环并抬出 | 把「176 approve」标为 Approve loop → 抬到 L0/L1/`approve_turn` + 自动只读降级 |
| `independent-review` | 审阅用干净上下文：diff+需求+测果 | 可选 review 票：只注入 diff_stat / AC / test log，不继承实现会话 |
| `engineering-small-prs` | 小可审变更 | 实现票合并策略：每票小 diff；禁止无 depends 的多路同文件乱写 |
| `harness-experiment` | 改 harness 一次一变、看 flipped tasks | 本方案分期上线，每期用 bb2f 类 golden 复跑对比 |

**刻意不抄：** Meegle / 飞书文档双写（pieqi 已有 Requirement Store）；`magic-move-deck` 等与执行效率无关的内容栈。

## 3. 目标与非目标

**目标（可度量）**

1. 同类「单仓、≤6 AC、同作者可审」需求：墙钟 ≤ plan 路径的 **1.3×**，机时 ≤ **1.5×**。
2. 人审次数：高风险审批中位 ≤ **20 / 需求**（bb2f 为 176）。
3. 完结：`done` 必须附 AC 证据；禁止「票全绿 + acceptance 空」可关。
4. 重复立项：同 project + 同 title/body hash 在 `active` 时拒绝或合并。

**非目标**

- 不做通用「多智能体编排产品」炫技；不把并发当卖点。
- 不削弱 L3 删除等人审硬边界（ADR-0015）。
- 不在本期改 Cursor 宿主弹窗；审批税优先用 pieqi 侧免审与降级。

## 4. 产品形态：三级执行路径

创建/确认时显式选路径（UI + API），默认由规模启发式建议：

```text
S0 短路径（默认倾向）
  单 Task +（可选）agent 内 plan/todos
  适用：≤3 AC、单角色、预估 <2h、无多仓

S1 计划闸路径（对齐「plan 模式体感」）
  design/plan 一票（或人贴已有定案）→ 确认 → 单实现票串行 → 可选独立 review/test
  适用：需方案钉死，但合入面单一

S2 需求 DAG（长路径，现行 Collaboration Plane）
  Confirm → req/<id> → 少票 + 严格 depends → test 波次 → outcome
  适用：多角色强隔离、多 AC、要分支审计、要 Issue 回流
```

启发式（confirm 前展示，可覆盖）：

| 信号 | 建议 |
|---|---|
| AC≤3 且 body 短、无「多模块」关键词 | S0 |
| 有 design 必要但单仓 | S1 |
| AC≥5 或显式多角色/多仓 | S2 |
| 已存在同文案 active 需求 | 禁止新建，引导续跑 |

**关键：** S2 不再是「默认高级」；它是**贵且可审计**的路径。UI 文案写清成本。

## 5. 拆分策略改造（默认少拆）

### 5.1 改写 `buildSplitSuggestPrompt` 的拆分哲学

现行文案含「能并行的不要串成一条」——对 bb2f 类需求会系统性多拆。改为：

1. **默认 1 design（可选）+ 1～2 实现 + 0～1 test**；超过 5 节点必须在 JSON 外（或 `_meta`）写明「上下文理由」（对应 subagent-split-check 三条）。
2. **禁止按 FE/BE 工种拆**，除非触及文件集无交集且各有独立可编译验收。
3. **默认串行合入**：多实现票必须 `depends_on` 成链，或显式声明「无文件交集」并列出路径前缀；否则 confirm 拒绝。
4. **每票必须含 agent-brief 块**（见下）；缺则 parse 失败。
5. 保留交付纪律（需求分支 / 实现仅单测 / test 隔离）——与 [`requirement-delivery.md`](./requirement-delivery.md) 一致。

### 5.2 Brief 卡（注入每票 prompt 头）

```text
Stakes: low | high
Hypothesis: ...
Goal / out of scope: ...
Done = <可执行检查：命令或 AC id + 测法>
Mode: do-it
Handoff: 读 docs/notes/<req>-handoff.md（若存在）；只写回结论与证据，勿复读全仓无目标探索
```

### 5.3 Confirm 硬校验（服务端）

| 规则 | 行为 |
|---|---|
| 节点数 > N（建议 N=5）且无 justification | 400 |
| 同角色多票且 `covers_ac` 交集大、路径未声明不相交 | 400 或降级警告 + 需 `force` |
| test 无 `depends_on` | 已有，保持 |
| 缺 `Done=` / 空话 prompt | 400 |
| 并行实现票数 > `max_parallel_implement`（建议默认 **1**，配置可调） | 400 |

配置建议：`pieqi.max_concurrent_per_project` 与「实现票并行」解耦——全局并发可 >1（允许 design 与无关短任务），**同一 requirement 的实现并行默认 1**。

## 6. 上下文：handoff 替代冷启动复读

每需求维护一份短 handoff（文件或 Store 字段均可；优先文件便于 agent 读）：

路径建议：`docs/handoff/req-<id8>.md`（gitignore 或可提交由项目定；pieqi 自仓可提交）。

内容对齐 mcode `handoff-doc`：Goal / Decisions / Constraints / Current state（含命令证据）/ Next / Watch outs。

**调度行为：**

- 后票 Start 时自动把 handoff 路径与「只读该文档 + covers_ac 相关代码」写入 prompt。
- design 票结束必须更新 handoff（否则不得依赖它的实现票 Start——软拦或硬拦可配置）。
- 禁止把前票 transcript 整段塞进后票（防上下文税；对照 Notes 02/05）。

## 7. 审批税：抬人出 Approve loop

bb2f 的 176 approve 是典型 lifting-loops **Approve** 角色。

分期：

**P0（行为）**

- 需求执行会话默认打开：`auto_approve_l0` + 已有只读降级（ADR-0008/0015）。
- 实现票启用 `approve_turn`（V2.0.1 已合入能力）：同 turn 同类工具不再弹。
- 终验票：隔离实例下对「编译 / 单测 / 只读探活」预授权；**改生产配置 / 删文件 / push** 仍 L3。

**P1（度量驱动）**

- 任务落盘：`approval_count`、`approval_wait_sec`、`agent_active_sec`（墙钟 − 等待）。
- 需求详情展示「人审税」；超过阈值提示「建议 S0/S1 或收紧并行」。

**不做：** 缩短超时冒充修复；未弹卡自动 approve（否决项，见 soft-complete 笔记）。

## 8. 证据优先完结（对齐 req-ship / req-outcome）

### 8.1 受理（intake）

创建 Requirement 时：

- 强制至少一条 **Expected Outcome**：指标或可观察行为、基线（可「待测」+ 测法）、目标、观察窗口。
- 可与 AC 合一：AC 行扩展 `method` / `baseline` / `target`；`deferred` 保持。

无 outcome 的「想法列表」只允许进 S0 短任务，不进 S2。

### 8.2 关单闸

替换当前「`can_mark_done: true` 而 acceptance 空」：

```text
can_mark_done =
  全部非 deferred AC 有 evidence_ref
  AND（无 open blocker Issue）
  AND（若有 test 票：最后成功 test 附隔离环境证明）
  AND integration_branch 已合入声明的目标（或显式「仅分支交付」）
```

`evidence_ref`：命令 + 退出码 + 摘要/日志路径（agent 写入；人可改）。

### 8.3 test 票

- 报告必须含「总评 + 每条 AC 的命令证据」；缺证据 → 不得 `completed`（已有 FAIL 总评拦截，扩到「无证据」）。
- 长空窗（如 >30min 无事件）标记 `stalled`；展示「等待人审」与「agent 在跑」分离——避免 533min completed 被当成有效机时。

## 9. 容量与并行闸门（review-capacity）

Confirm / 批量 Start 前计算：

```text
queue = 本需求未验收完成票 + 用户全局 waiting_input(approval) 高风险项
if queue.high_stakes >= 2: 禁止再 Start 新实现票（可 Start 只读/design）
```

UI：显示「审不过来」而不是静默排队到隔夜。

## 10. 可观测性（否则永远吵体感）

今日 bb2f 的 `usage` 全空，无法谈 token。最低集：

| 字段 | 来源 |
|---|---|
| `wall_sec` / `wait_approval_sec` / `agent_sec` | runner 计时 |
| `approval_count` / `append_prompt_count` | interventions |
| `event_seq` | 已有 |
| `diff_stat` | 已有 |
| `path` | S0/S1/S2 |
| agent `usage` 快照 | 已有模型，需汇总到需求级 |

需求详情一页：路径、节点数、机时、人审、是否可关、证据缺口。

## 11. 分期施工

### P0 — 止损（约 1 周，优先改行为不改大模型）

1. 改 `buildSplitSuggestPrompt`：少拆、禁工种拆、默认同需求实现并行=1。  
2. Confirm 校验：节点上限、并行实现上限、Brief/`Done=`。  
3. 同文案 active 需求去重。  
4. 需求执行默认打开 L0 免审 + 推动 `approve_turn`。  
5. `can_mark_done`：acceptance 空 → false。  
6. 文档：本笔记定案后更新 `requirement-delivery.md` 增「路径分级」。

**验收：** 用「V2.0.1 同规模假需求」干跑拆分，节点 ≤5 且实现串行；旧 bb2f 复盘页能显示「若用新规则会怎样」的对照说明即可。

### P1 — 上下文与证据（约 1～2 周）

1. 需求级 handoff 自动读写。  
2. AC evidence_ref + outcome API。  
3. 计时与人审税落盘 + 详情展示。  
4. S0/S1/S2 创建入口与启发式。  
5. test 无证据不得 completed；stalled 状态。

**验收：** 新需求从创建→关单，库内 acceptance 非空；人审次数与机时可查询。

### P2 — 抬环与审阅（机动）

1. lifting：对高频 kind 自动建议免审配方（仍需人开一次）。  
2. 可选 independent-review 票模板。  
3. harness-experiment：固定 3 条 golden 需求，改拆分策略前后对比墙钟/人审/关单率。

## 12. 成功指标（上线后 2 周）

| 指标 | 基线（bb2f） | 目标 |
|---|---|---|
| 中位实现并行度 | ~3–4 | ≤1（S2）或 S0/S1 |
| 中位人审 / 需求 | 176 | ≤20（S1）/ ≤40（S2） |
| 关单时 acceptance 非空率 | 0% | 100% |
| 同文案重复 active | 有 | 0 |
| 用户主观「不如 plan」占比 | — | 问卷或事后 tag 下降 |

## 13. 风险与否决项

| 风险 | 缓解 |
|---|---|
| 少拆导致单票过大、上下文糊掉 | S1 允许中途 handoff 清会话；Notes 01：先测劣化再拆 |
| 串行变慢 | 用「少冷启动 + 少审批等待」换；并行为可选 opt-in |
| 过严 confirm 烦 | 启发式 + `force`；S0 一键逃逸 |
| 免审漏危险操作 | L3 不动；只抬 L0/L1/turn |

**否决：** 未弹卡自动 approve；缩短超时冒充修复；用更多并行 agent「堆算力」当性能方案。

## 14. 建议的立即下一步

1. 评审本提案：是否采纳「S0/S1/S2 + 同需求实现并行默认 1」。  
2. 若采纳：先合 P0 的 split prompt + confirm 校验 + `can_mark_done`（小 PR，可测）。  
3. 用下一条真实需求走 S1，与历史 bb2f 指标并排记一笔。

---

## 附录 A — mcode-stack 目录速查（本地）

```text
D:\code\go\mcode-stack\
  notes/01-subagent.md          # 先上下文后并行
  stacks/ronny/skills/          # split / brief / capacity / evidence / handoff …
  stacks/req-tpm/skills/        # intake → design → ship → outcome
  stacks/jiachen/skills/lifting-loops/  # 抬人出环
```

## 附录 B — pieqi 关键改动锚点

| 改动 | 锚点 |
|---|---|
| 拆分 prompt | `internal/core/requirement_suggest.go` → `buildSplitSuggestPrompt` |
| Confirm 校验 | `internal/core/requirement_confirm.go` / `requirement_delivery.go` |
| 并发 | `internal/config` `max_concurrent_per_project` + 需求级实现并行 |
| 完结 | outcome API / `acceptance` 表 / `can_mark_done` |
| DoD 注入 | `AppendDeliveryDoD`（保留，叠加 Brief） |
| 免审 | 设置页 `auto_approve_l0/l1` + `approve_turn` |
