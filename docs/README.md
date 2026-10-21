# Pieqi 文档索引

> 本目录放设计与规划文档。**读的人不一样，入口也不一样**：
>
> - 想知道「这东西是什么、怎么跑起来」→ 回仓库根 [`README.md`](../README.md)
> - 想改代码（发版流程、沙箱纪律、审批分级、血泪坑）→ [`AGENTS.md`](../AGENTS.md)
> - 想知道某个词到底指什么、哪些事明确不做 → [`CONTEXT.md`](../CONTEXT.md)（术语唯一真相）

## 目录地图

| 目录 / 文件 | 内容 | 状态 |
|---|---|---|
| [`adr/`](adr) | **架构决策记录**（ADR 0001–0015），改相关代码前先读对应 ADR | 长期有效 |
| [`feedback/`](feedback/README.md) | 反馈体系（Observe→Verify→Control→Intervene）：功能点全量规划 + P0–P3 设计 | Design / Roadmap |
| [`testing/`](testing) | UI 真机验收（Playwright 三层）、下版待办、线上复量方案 | 已落地 |
| [`ui-design/`](ui-design) | UI 设计规范（`UI-DESIGN-SPEC.md`）、原型（`prototype/`）、落地实施方案 | Stable |
| [`frontend-v2/`](frontend-v2) | 前端 V2 重构的契约基准（`baseline.md`）、迁移完成报告、**设计原文归档**（`Pieqi Frontend V2 架构升级技术方案.md`） | 已完成（设计原文仅存档） |
| [`requirements/`](requirements/overview.md) | 2.2 版本需求流水线 S0–S5（规划 / PRD / 技术方案 / 排期 / 实施记录） | 已放行 |
| [`v2.1/`](v2.1) | 下一阶段综合功能迭代规划 v1.0 与功能点清单 | 规划 |
| [`superpowers/plans/`](superpowers/plans) | 飞书 IM 绑定 + cloudflared 隧道、飞书长连接 + Device Flow 的实施计划 | 历史存档 |
| [`spikes/`](spikes/claude-sdk-bridge/README.md) | P0 spike 存档（claude-sdk-bridge 验证脚本，非产品代码） | 历史存档 |
| [`multi-agent.md`](multi-agent.md) | 多 Agent 技术方案（`agents.*` 配置、桥 / print / ACP、P0–P5 演进） | 现行 |
| [`phase2-acp.md`](phase2-acp.md) | Phase 2：`claude -p` → ACP 迁移规划 | 历史（已实现） |
| [`multi-agent-evaluation.md`](multi-agent-evaluation.md) | 对多 Agent 方案的评估意见（答辩记录） | 历史存档 |
| [`comm-arch-upgrade-evaluation.md`](comm-arch-upgrade-evaluation.md) | 对通信架构升级方案的评估意见 | 历史存档 |
| [`security-hardening.md`](security-hardening.md) | 外网安全整改方案（named / quick 两种部署形态） | **现行，改鉴权前必读** |
| [`agents-goal-and-capabilities.md`](agents-goal-and-capabilities.md) | claude / qoder / dsh 三家的 goal 能力证据链与结论（2026-10-18） | 结论有效 |
| [`dsh-acp-official-verification-report.md`](dsh-acp-official-verification-report.md) | 官方 dsh-acp 兼容性验证报告（2026-10-09） | 历史存档 |
| [`dsh-image-capability-source-verification.md`](dsh-image-capability-source-verification.md) | dsh 图片能力：上游源码复核记录 | 证据存档 |
| [`dsh-sandbox-investigation.md`](dsh-sandbox-investigation.md) | DSH 沙箱实现机制调查报告（对端，不是 pieqi 的沙箱） | 参考资料 |

> **仓库根目录不再放文档**：所有设计与规划文档都在 `docs/` 下。原先孤悬在根的
> 《Pieqi Frontend V2 架构升级技术方案》已迁入 [`frontend-v2/`](frontend-v2/Pieqi%20Frontend%20V2%20架构升级技术方案.md) 并加注"历史提案"横幅
> （其 §6/§23/§26 与最终实现不一致，见横幅内对照表）。新增文档请放进对应子目录，不要放根。

## ADR 速查（`adr/`）

| ADR | 一句话 |
|---|---|
| [0001](adr/0001-taskevent-source-of-truth-filechange-derived.md) | `TaskEvent` 是事实源，`FileChange` 一律从事件派生 |
| [0002](adr/0002-readonly-git-baseline-file-snapshot-checkpoint.md) | Baseline 用只读 git HEAD，Checkpoint/Rewind 用文件快照，绝不写用户分支 |
| [0003](adr/0003-turn-bounded-by-user-message.md) | Turn 以**用户消息**为边界，而非 agent 协议 turn |
| [0004](adr/0004-evidence-continue-backend-assembled.md) | Evidence → Continue 由**后端**组装上下文 |
| [0005](adr/0005-checks-reuse-agent-tool-results.md) | Checks 优先复用 agent 已跑过的 tool result，独立 Runner 兜底 |
| [0006](adr/0006-visual-capture-playwright-service.md) | （已被 0012/0013 取代）视觉反馈用独立 Node Playwright 服务采集 |
| [0007](adr/0007-feedback-non-goals.md) | 明确不做 Web IDE / Computer Use / 云桌面 / 真终端 / 浏览器自动化 / 录屏 |
| [0008](adr/0008-readonly-execute-downgrade.md) | 只读 shell 命令从 L2(execute) 降级为 L0(read) |
| [0009](adr/0009-self-restart-resumes-initiator.md) | 自重启后接回会话，**包括发起重启的那个会话** |
| [0010](adr/0010-image-prompt-metadata-only.md) | 图片只留元数据，base64 绝不进任务文件 |
| [0011](adr/0011-deprecate-dsh-acp-fork.md) | 弃用 dsh-acp 本地 fork，改用官方版 + 恢复 profile pin |
| [0012](adr/0012-remove-screenshot-keep-console-network.md) | 移除截图能力，保留 console/network |
| [0013](adr/0013-remove-visual-capture-entirely.md) | 移除 console/network，**视觉反馈层整体下线** |
| [0014](adr/0014-requirement-closed-loop.md) | 需求开发闭环（Requirement / Task Graph / Issue）—— 提议中 |
| [0015](adr/0015-approval-noise-reduction-keep-deletes.md) | 审批降噪：测试/构建/只读探查免审，**删除一律人工** |

> 约定：新决策与既有 ADR 冲突时**新增** `adr/00NN-*.md`，不改写旧 ADR。

## 测试相关

| 文档 | 内容 |
|---|---|
| [`testing/ui-e2e.md`](testing/ui-e2e.md) | 真机验收主文档：三层分工、夹具、基线更新、已知坑 |
| [`testing/ui-e2e-next.md`](testing/ui-e2e-next.md) | 下个版本的待办（还没做的事） |
| [`testing/ui-e2e-online.md`](testing/ui-e2e-online.md) | 部署后线上复量的设计方案 |

日常命令：`cd web && npm test`（逻辑层）→ `npm run test:e2e:impact`（按改动相关）→ 部署后 `npm run test:e2e:online`。
