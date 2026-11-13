# pieqi 需求执行 Workflow 方案（效率 · 质量 · 可配置）

| 字段 | 值 |
|---|---|
| 状态 | **已定案（一期已接线）**（2026-11-13） |
| 复盘样本 | `bb2f5ab5`「V2.0.1 流程优化」；前序空转 `0c2a` / `f920` / `24817` |
| 对照来源 | MiniMax-AI/mcode-stack 原则；OpenAI Symphony（`WORKFLOW.md` 契约思路） |
| 相关已定案 | [requirement-delivery.md](requirement-delivery.md)、[ADR-0021](../adr/0021-collaboration-plane-dual-path.md)、[approval-miss-and-soft-complete.md](approval-miss-and-soft-complete.md)、ADR-0008/0015 |
| 目标读者 | 改协作平面 / 拆分 / 完结闸 / 流程包的人 |
| UI 线框 | [ui-mock-requirement-workflow.html](ui-mock-requirement-workflow.html)（对齐 §4/§8/§11；非产品代码） |
| 运行位 Pack | embed `internal/workflow/packs/` + `~/.pieqi/workflows/*.yaml` 覆盖 |

---

## 0. 一句话结论

pieqi 需求 DAG **赢在可审计、可挂分支、可回流**；在算力、墙钟、质量闸上，对中小需求常输给「plan → 单会话执行」。

根因不是缺票，而是：**按角色/领地拆会话（冷启动税）+ 默认可高并行（审批与合入税）+ 票绿即可（证据闸缺失）**。

应对：

1. **裁判在代码，策略在 Workflow Pack**（可版本、可热更新、需求钉死 version）。
2. **默认少拆、同需求实现并行 = 1**；完整 DAG 是贵路径，不是默认高级。
3. **上线 ≠ 完成，有证据才关闭**；人反复当 Approve 环时抬人出环，不靠堆并行。

不把 pieqi 做成「未关闭工单常挂 agent」的通用多智能体编排产品。

---

## 1. 问题钉死（bb2f 样本）

| 指标 | 值 | 解读 |
|---|---|---|
| 墙钟（工作票） | ≈ 11.6 h | 17:51 → 次日 05:27 |
| 机时加总 | ≈ 14.9 h | cpu/wall ≈ 1.27，并行几乎没换墙钟 |
| 峰值并发 | 4 | 默认 `max_concurrent_per_project=4` 顶满 |
| 人审 | 176× approve + 5×追问 | 审批成主路径税 |
| 终验票 | 533 min / 85 approve | 含长空窗；有效机时被夸大 |
| AC / Issue | 表空 | `acceptance` 空、issues=0 |
| 需求状态 | 仍 active | 代码已合 main，完结靠人肉 |
| 前序浪费 | 同文案重复立项 + 整图 cancel | 冷启动与拆分成本被付了多次 |

用户体感「比 plan 更耗算力、质量与速度都没提升」与数据一致。

**人审构成注意**：176 需分桶（真审 / 卡未露重试 / timeout / soft-fail）。只抬 L0 治不好「该审未见」（见 soft-complete 笔记）。

---

## 2. 原则来源 → pieqi 落点

| 来源 | 原则 | pieqi 落点 |
|---|---|---|
| mcode Notes 01 | 先上下文后并行；子代理是隔离上下文的工具，不是工种工单 | 改拆分哲学；禁 FE/BE 硬拆 |
| mcode brief / evidence / handoff | Done=可检查；票绿是主张；状态进短文档 | Brief 卡；evidence_ref；handoff |
| mcode lifting-loops | 人当 Approve 环 → 抬出 | L0/`approve_turn`；容量闸 |
| mcode review-capacity | 审不过来别开并行 | 待审高风险多时禁 Start 新实现 |
| Symphony `WORKFLOW.md` | 仓内/可版本策略 + 编排只调度；热重载；成功可停在 Human Review | Workflow Pack；钉死 version；handoff ≠ done |
| Symphony by_state 限流 | 按状态并发上限 | 实现并行与全局并发解耦 |
| Symphony（**不抄**） | 未关闭 = 常挂 agent、默认高并发、PR 数当 KPI | 否决 |
| ADR-0021 | 短路径一等公民 + 长路径 Requirement | 路径分级落在双路径上，不新开状态机 |
| delivery / soft-complete | 需求分支交付；未弹卡不自动 approve；不缩超时 | 硬边界保留 |

---

## 3. 目标与非目标

### 目标（可度量）

| 目标 | 口径 |
|---|---|
| 中小需求效率 | 同类「单仓、≤6 AC、同作者可审」：墙钟 ≤ plan 对照的 1.3×（有对照时），机时 ≤ 1.5×；无 plan 对照时用作者可接受上限 |
| 人审 | 高风险真审中位：S1 ≤ 20 / 需求，S2 ≤ 40（bb2f 基线 176） |
| 完结 | done 必须附 AC 证据；禁止「票全绿 + 有 AC 却 acceptance/证据空」可关 |
| 重复立项 | 同 project + title/body hash 在 active 时拒绝或合并 |
| 可配置 | 改拆分文案/并行 knobs **不必发版**；需求可审计用了哪版 Pack |

### 非目标

- 不做通用多智能体编排炫技；不把并发当卖点。
- 不削弱 L3 删除等人审硬边界（ADR-0015）。
- 本期不改 Cursor 宿主弹窗；审批税优先 pieqi 侧免审与降级。
- 不把 Linear/外链 tracker 变成真相源（Store 仍是真相）。
- 质量分不替代 `can_mark_done`。

### 否决项

- ❌ 未弹卡自动 approve  
- ❌ 缩短审批超时冒充修复  
- ❌ 用更多并行 agent「堆算力」当性能方案  
- ❌ 跑中偷换 Pack version（破坏可复现）  
- ❌ 把 ValidateSplit / L3 / soft-complete 判据做成可热改脚本  

---

## 4. 产品形态：三级执行路径

创建/确认时显式选路径（UI + API），**启发式建议、可覆盖**。命名落在 ADR-0021 语言上：

| 代号 | 0021 语言 | 形态 | 适用 |
|---|---|---|---|
| **S0** | 短路径 Task | 单 Task +（可选）agent 内 plan/todos | ≤3 AC、单角色、预估 &lt;2h、无多仓 |
| **S1** | 轻量 Requirement | design/plan 一票（或人贴定案）→ 确认 → **单实现串行** → 可选独立 review/test | 需钉方案，合入面单一 |
| **S2** | 完整 Collaboration Plane | Confirm → `req/<id>` → **少票 + 严 depends** → test 波次 → outcome | 多角色强隔离、多 AC、要分支审计、要 Issue 回流 |

启发式（confirm / 创建前展示）：

| 信号 | 建议 |
|---|---|
| AC≤3 且 body 短、无「多模块」关键词 | S0 |
| 有 design 必要但单仓 | S1 |
| AC≥5 或显式多角色/多仓 | S2 |
| 已存在同文案 active 需求 | **禁止新建**，引导续跑 |

**关键**：S2 不再是「默认高级」；UI 写清成本（冷启动、人审、合入税）。

短路径 Task **不出现**流程包选择（不进需求图）。

---

## 5. Workflow Pack（可配置核心）

### 5.1 分层

| 层 | 内容 | 存放 | 热改 |
|---|---|---|---|
| **L0 契约** | JSON/提案校验、test depends、L3、软完成否决、零 AC 语义 | Go + 单测 | 否（发版 + ADR） |
| **L1 策略** | 拆分文案、Brief/DoD、并行/节点 knobs、路径倾向文案 | Workflow Pack | 是 |
| **L2 实验** | 本需求用哪套 pack/variant | Requirement 字段 | 创建时钉死 |
| **L3 观测** | wall / 人审 mix / 证据缺口 / pack 版本 | Store / Scorecard | 只读 |

原则：**模型可读的句子外置；机器必须 enforce 的规则留代码。** 缺 Pack 或渲染失败 → 回退 embed 默认，fail closed。

### 5.2 Pack 形状（对齐 Symphony `WORKFLOW.md` 子集）

```text
~/.pieqi/workflow-packs/<pack_id>/<version>/
  WORKFLOW.md          # YAML front matter (knobs) + 主策略正文（可选）
  templates/
    split_suggest.md
    brief_header.md
    dod_implement.md
    dod_test.md
    req_context.md
    issue_fix.md
    issue_reverify.md
  NOTES.md             # 假说一句话（AB 复盘）
```

仓库可保留已验证默认源（如 `docs/workflow-packs/`），安装/发布时 copy 到运行位；**运行期读 `~/.pieqi` + embed**，不直接绑工作区路径（Low 完整性 / 路径纠缠）。

**Knobs（示例）**

| 键 | 默认建议 | 说明 |
|---|---|---|
| `max_implement_parallel` | 1 | 同 Requirement 实现类票同时 Start 上限 |
| `suggest_max_nodes` | 5 | 写入拆分提示；超过仅警告或需 force（分期） |
| `path_bias` | `light_req` | short / light_req / full_dag |
| `require_brief_in_prompt` | true | 软：拆分文案要求；硬 400 可后期开 |

与 `pieqi.max_concurrent_per_project` **解耦**：全局可 >1（无关短任务、design），同需求实现并行默认 1。

**模板变量白名单**（未知变量 → 渲染失败该次）：  
`title`, `body`, `project_path`, `acceptance[]`, `role`, `covers_ac`, `integration_branch`, `handoff_path`, `id`/`id8`, `temp_ids`…  
禁止把整段 transcript 当变量。

**版本纪律**：已 `published` 的 version **不可 mutate**；改 = Fork 新 version。Requirement 钉死 `{ pack_id, version }`；配置热重载只影响**之后**创建/dispatch。

### 5.3 与现有代码锚点

| 能力 | 锚点 |
|---|---|
| 拆分 prompt | `internal/core/requirement_suggest.go` → `buildSplitSuggestPrompt`（改读 Pack） |
| DoD 注入 | `AppendDeliveryDoD`（正文来自 Pack；幂等标记保留） |
| Confirm | `requirement_confirm.go` / `requirement_delivery.go`（并行/节点校验） |
| 需求上下文 | `requirement_branch.go` AppendRequirementContext |
| 并发 | `max_concurrent_per_project` + **需求级**实现并行 |
| 完结 | `DeriveRequirementOutcome` / `can_mark_done` |
| 免审 | 设置页 `auto_approve_l0/l1` + `approve_turn` |

内置默认 Pack 先用 `//go:embed` 承接今日硬编码字符串（行为不变），再允许盘上覆盖。

---

## 6. 拆分策略（默认少拆）

### 6.1 拆分哲学（写入 Pack `split_suggest`）

替换现行「能并行的不要串成一条」：

1. 默认 **1 design（可选）+ 1～2 实现 + 0～1 test**；超过建议节点数须写明上下文理由（上下文劣化 / 可丢弃调研 / 工具指令冲突）。
2. **禁止按 FE/BE 工种硬拆**，除非文件集无交集且各有独立可编译验收，并列出路径前缀。
3. 默认 **串行合入**：多实现票必须 `depends_on` 成链，或显式声明不相交。
4. 每票 prompt 含 **Brief 卡**（见下）；空话 prompt 拒绝。
5. 保留交付纪律（需求分支 / 实现仅单测 / test 隔离）——与 [requirement-delivery.md](requirement-delivery.md) 一致。

### 6.2 Brief 卡（每票 prompt 头）

```text
Stakes: low | high
Hypothesis: ...
Goal / out of scope: ...
Done = <可执行检查：命令或 AC id + 测法>
Mode: do-it
Handoff: 读 docs/handoff/req-<id8>.md（若存在）；只写回结论与证据
```

### 6.3 Confirm 硬校验（服务端，分期加严）

| 规则 | P0 | 后期 |
|---|---|---|
| test 无 `depends_on`（有实现节点时） | 已有，保持 | — |
| 同需求实现并行 Start > `max_implement_parallel` | **硬拦** | — |
| 空 prompt / 缺 Done= | 警告或 400 | 400 |
| 节点数 > N 且无 justification | 警告 + force | 可 400 |
| 同角色多票 covers_ac 大交集且未声明不相交 | 警告 | 可 400 |

过严 Confirm 用启发式 + force + S0 一键逃逸缓解。

### 6.4 Task.role 怎么对待

| role | 去留 | 作用 |
|---|---|---|
| `design` / `test` / `fix` / `review` | **保留** | DoD 分支、test depends、失败开 Issue、软完成 FAIL 拦截 |
| `frontend` / `backend` | **保留枚举** | 对后端几乎等价（皆实现类）；主要用于 UI 泳道与弱标签 |
| 空 / `other` | 按实现类 | `isImplementRole` 已如此 |

**影响说明**：role **不是**按工种调度队列；真正行为在 DoD / test 回流 / Confirm。FE/BE 硬拆的危害来自**拆分文案 + 泳道诱导**，不是枚举本身。  
可选演进：UI 泳道合并为 `implement`，底层仍可存 fe/be。

实现并行计数必须用 `isImplementRole`（fe+be 都算），不能只数同 role。

---

## 7. 上下文：handoff

每需求维护短 handoff（优先可读文件；Store 字段可双写）：

- 路径建议：`docs/handoff/req-<id8>.md`（gitignore 与否由项目定）
- 内容：Goal / Decisions / Constraints / Current state（含命令证据）/ Next / Watch outs

调度：

- 后票 Start：注入 handoff 路径 +「只读该文档 + covers_ac 相关代码」
- design 结束应更新 handoff（可配置软/硬拦）
- **禁止**把前票 transcript 整段塞进后票

---

## 8. 审批税与容量

### 8.1 抬人出 Approve loop

**P0 行为**

- 需求执行会话默认推动：`auto_approve_l0` + 既有只读降级（ADR-0008/0015）
- 实现票推动 `approve_turn`（ACP 路径已有；hook 路径无则别承诺全路径）
- 终验隔离实例：编译/单测/只读探活可预授权；改生产配置/删文件/push 仍 L3

**产品落点钉死（二选一，定案时选）**

- A：创建/Start 时 **nudge** 打开 L0（改设置）  
- B：requirement 会话 **覆盖** 设置（权限面更大）  

推荐先 A。

**共依赖**：soft-complete「该审必见」——否则人审税指标不会单独因免审下降。

### 8.2 容量闸（review-capacity）

Confirm / 批量 Start 前：

```text
queue = 本需求未验收完成票 + 用户全局 waiting_input(approval) 高风险项
if high_stakes_pending >= 2: 禁止 Start 新实现票（可 Start 只读/design）
```

UI 显示「审不过来」，禁止静默排到隔夜。

可借鉴 Symphony：`max_concurrent_by_state`——实现态与 Human Review 态分槽。

---

## 9. 证据优先完结

### 9.1 受理（intake）

- S1/S2：建议强制至少一条 Expected Outcome（可与 AC 合一：method / baseline / target；deferred 保持）
- 无 outcome 的「想法列表」只允许 S0

### 9.2 关单闸

替换「`can_mark_done: true` 而有 AC 却无证据」：

```text
can_mark_done =
  全部非 deferred AC 有 evidence_ref
  AND 无 open blocker Issue
  AND（若有 test 票：最后成功 test 附隔离环境证明）
  AND integration_branch 已合入声明目标（或显式「仅分支交付」）
```

**零 AC 容器**（ADR-0021 §2.4）：维持「无开放 Issue 可 done」，**不得**宣称 testing 闭环。  
提案「acceptance 空 → false」**过粗**，定案须写清上述例外。

`evidence_ref`：命令 + 退出码 + 摘要/日志路径（agent 写，人可改）。

现行 `hasCompletedCover`（有 completed 覆盖票即 green）必须升级为证据门槛，否则票绿虚关复现。

### 9.3 test 票

- 报告须含「总评 + 每条 AC 的命令证据」；无证据 → 不得 completed（扩展现有 FAIL 总评拦截）
- 长空窗（如 >30min 无事件）标记 **stalled**；展示「等待人审」与「agent 在跑」分离

---

## 10. 可观测与质量分（诊断，不开关单）

### 10.1 最低观测字段

| 字段 | 来源 |
|---|---|
| `path` | S0/S1/S2 |
| `pack_id@version` | 钉死 |
| wall_sec / wait_approval_sec / agent_sec | runner |
| approval_count + **approval_mix** | interventions |
| 节点数 / 实现并行峰值 | 图 + 调度 |
| 证据缺口 / can_mark_done | outcome |

需求详情一页可读：路径、包版本、节点、机时、人审、可否关、证据缺口。

### 10.2 打标 → 质量分（可后置）

- **Task 自动侧面**：terminal / evidence / approval_tax / stall / rework / scope（终态重算）
- **人标（可选）**：context_quality、split_necessity、worse_than_plan
- **需求三账**：delivery / efficiency / process → overall（仅排序与 AB）
- **禁止** overall≥阈值自动 done

Scorecard 与 golden 实验协议见 §13。

---

## 11. UI 交互

沿用设置页纪律：**跨任务全局 → 设置；对象级 → 就地**。线框：[ui-mock-requirement-workflow.html](ui-mock-requirement-workflow.html)。

### 11.1 设置 · 流程包

- 新 `SetGroup`「流程包」：默认包卡片 + 包列表（内置/本地/draft）
- 动作：设为默认（只影响之后新建）、Fork、编辑草稿、发布（冻结 version）
- L0 规则用 **locked 行**说明，不提供开关

### 11.2 包编辑

- Knobs + 模板 Tab（拆分 / Brief / DoD…）+ 变量插入白名单 + 示例预览
- 发布二次确认：「发布后不可改，只能 Fork」

一期可降级：只读查看模板，编辑靠改盘文件 +「重新扫描」。

### 11.3 新建需求

- 主表单不变；底部一行「流程包：xxx@version [更换]」
- Sheet 选用已发布包；可选「参加实验」（二期）
- 创建后钉死，进详情可 `?suggest=1`

### 11.4 需求详情

- 页头只读：`流程包 name@version`
- 拆分中提示「按 xxx 生成」；可预览将注入的 DoD
- **换包规则**：

| 状态 | 换包 |
|---|---|
| 未 confirm / 仅提案 | 允许，须重拆 |
| 已 confirm，票全 planned | 允许，须 ConfirmReplace + 重拆 |
| 已有 running/completed | **禁止**；引导新建对照需求 |

Confirm 时节点超建议上限：警告，不静默。

---

## 12. 分期施工

### P0 — 止损（约 1 周，优先行为）

1. 改 `buildSplitSuggestPrompt`：少拆、禁工种硬拆、默认同需求实现并行语义写入文案。  
2. Confirm/Start：**实现并行上限 = 1**（配置可调）。  
3. 同文案 active 需求去重。  
4. `can_mark_done`：有非 deferred AC 时无证据 → false；**零 AC 例外保留**。  
5. 需求执行 L0 nudge + 推动 `approve_turn`。  
6. 文档：本笔记定案后更新 `requirement-delivery.md`「路径分级」一节。  
7. Pack：**embed 默认抽出**（行为不变）+ 设置只读展示版本（可选）。

验收：同规模假需求干跑拆分，节点 ≤5 且实现串行；bb2f 复盘可对照「若用新规则」。

### P1 — Pack 热配 + 证据 + 观测（1～2 周）

1. `~/.pieqi/workflow-packs` 覆盖 + 新建需求选用 + 详情钉死展示。  
2. Fork/编辑/发布 UI（或先文件 + 扫描）。  
3. handoff 自动读写。  
4. AC `evidence_ref` + outcome API。  
5. 计时与人审税（含 mix）落盘 + 详情。  
6. S0/S1/S2 创建入口与启发式。  
7. test 无证据不得 completed；stalled 状态。

验收：新需求创建→关单，库内证据非空；人审/机时可查；可换 Pack 重跑对照。

### P2 — 抬环、审阅、实验（机动）

1. 高频 kind 免审配方建议（仍需人开一次）。  
2. 可选 independent-review 票模板。  
3. Experiment 分配 API + 按 pack 聚合。  
4. 质量分 tq/rq schema（若仍需要）。  
5. Golden×3 harness 前后对比。

---

## 13. 如何摸索适合自己的配方（定默认前）

一次只拧一个旋钮；固定 Scorecard：

```text
path / pack@version / 节点 / 实现并行
wall / agent_sec / wait_approval_sec
approval_mix{true, timeout, unresolved, retry}
证据完备 / can_mark_done / 作者 tag(不如 plan?)
```

建议 golden：反例型（类 bb2f）、小型、需 design 型；（可选）真多仓、重复立项。

连续 ≥2 golden 同向 → 升默认 Pack；证伪也要写入 Decision Log。

---

## 14. 成功指标（上线后 2 周）

| 指标 | 基线（bb2f） | 目标 |
|---|---|---|
| 中位实现并行度（S2） | ~3–4 | ≤1（或走 S0/S1） |
| 中位真审 / 需求 | 176（含未分桶） | ≤20（S1）/ ≤40（S2） |
| 关单时有 AC 则证据非空 | 0% | 100% |
| 同文案重复 active | 有 | 0 |
| 「不如 plan」主观占比 | — | 下降（事后 tag） |

---

## 15. 风险与缓解

| 风险 | 缓解 |
|---|---|
| 少拆 → 单票过大、上下文糊 | S1 中途 handoff 清会话；测到劣化再拆 |
| 串行变慢 | 用少冷启动 + 少审批等待换；并行为 opt-in |
| Confirm 过严烦 | 警告 + force；S0 逃逸 |
| 免审漏危险操作 | L3 不动；只抬 L0/L1/turn |
| Pack 外置绕过纪律 | 硬闸留 L0；DoD 可外置文案不可关闸 |
| Symphony 式堆 agent | 默认并行 1 + 容量闸 + KPI 不用 PR 数 |

---

## 16. 建议的立即下一步

1. **评审拍板**（本提案）：  
   - 采纳「S0/S1/S2 作为 0021 分级 + 同需求实现并行默认 1」  
   - 采纳 Workflow Pack（契约/热配/钉死）  
   - `can_mark_done` 证据闸 + 零 AC 例外写法  
2. **P0 小 PR**：split prompt + 实现并行闸 + can_mark_done（含例外）+ 去重。  
3. 下一条真实需求走 **S1 + 实验 Pack**，与 bb2f 指标并排记一笔。  

定案后：状态改为「已定案」；同步改 `requirement-delivery.md` 增「路径分级」；可选补 ADR 补丁（Pack 与 outcome 证据语义）。

---

## 附录 A — mcode / Symphony 目录与链接（参考）

- mcode-stack（作者机）：`notes/01-subagent.md`；skills：split / brief / capacity / evidence / handoff / lifting-loops；req-tpm intake→outcome  
- Symphony 文：[Codex 编排的开源规范：Symphony](https://openai.com/zh-Hans-CN/index/open-source-codex-orchestration-symphony/) — 借 `WORKFLOW.md` 与 handoff≠done，不借高并发常驻 agent  

## 附录 B — 关键改动锚点速查

| 改动 | 锚点 |
|---|---|
| 拆分 prompt | `internal/core/requirement_suggest.go` |
| Confirm / DoD / test depends | `requirement_confirm.go` / `requirement_delivery.go` |
| 并发 | `internal/config` + 需求级实现并行 |
| Outcome | `requirement_outcome.go` |
| 免审 | 设置页 + `agent_perm.go` `approve_turn` |
| UI | `SettingsPage.vue`、`NewRequirementPage.vue`、`RequirementPage.vue` |
| 泳道 | `web/src/features/requirement/lanes.ts`（可选合并 implement） |
