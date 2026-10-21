# Pieqi Feedback 文档索引

Feedback 体系（Observe → Verify → Control → Intervene）的完整文档集。

## 文档地图

| 文档 | 内容 | 状态 |
|---|---|---|
| [功能点规划](functional-plan.md) | 全量功能点 P0–P3 + ❌、优先级、推荐实施顺序、最终产品闭环（原文件名 `Pieqi「反馈体系」功能点规划.md`，2026-10-21 改为 ASCII 名） | Draft |
| [P0 设计](p0-design.md) | 主链路：Changes / Diff / Baseline / Checkpoint / Rewind / Preview（Discovery·Lifecycle·Proxy）、数据结构、API、状态流转、验收 | Design |
| [P1 设计](p1-design.md) | 控制闭环：双视角 Changes、Approval→Diff、Checks、Task Outcome、Evidence、Evidence→Continue、Rewind→Verify、Preview Refresh/Attach | Design |
| [P2 设计](p2-design.md) | 视觉反馈：Screenshot、Browser Console、Network Error、Screenshot→Evidence、File Rewind、Evidence Push（**a/b/c 已于 2026-10-15 移除，见 ADR-0012/0013**） | Design（部分废弃） |
| [P3 路线图](p3-roadmap.md) | 探索：DOM Inspection、Region Annotation、Visual Evidence→Agent、Task Replay | Roadmap |
| [词汇表](../../CONTEXT.md) | 领域术语 + 明确不做清单 | Stable |

## ADR 索引

| ADR | 决策 |
|---|---|
| [0001](../adr/0001-taskevent-source-of-truth-filechange-derived.md) | TaskEvent 是事实源，FileChange 是派生模型 |
| [0002](../adr/0002-readonly-git-baseline-file-snapshot-checkpoint.md) | Baseline 只读 Git，Checkpoint/Rewind 用文件快照，绝不写用户分支 |
| [0003](../adr/0003-turn-bounded-by-user-message.md) | Turn 以用户消息（EventUser）为边界 |
| [0004](../adr/0004-evidence-continue-backend-assembled.md) | Evidence → Continue 由后端组装 |
| [0005](../adr/0005-checks-reuse-agent-tool-results.md) | Checks 优先复用 Agent Tool Result，独立 Runner 兜底 |
| [0006](../adr/0006-visual-capture-playwright-service.md) | （已被 0012/0013 取代）视觉反馈用独立 Node Playwright 服务采集 |
| [0007](../adr/0007-feedback-non-goals.md) | 明确不做 Web IDE / Computer Use / 云桌面 / 真终端 / 可写文件浏览器 / 浏览器自动化 / 录屏 |
| [0008](../adr/0008-readonly-execute-downgrade.md) | 只读 shell 命令从 L2(execute) 降级为 L0(read) |
| [0010](../adr/0010-image-prompt-metadata-only.md) | 图片只留元数据，base64 绝不进任务文件 |
| [0012](../adr/0012-remove-screenshot-keep-console-network.md) | 移除截图能力，保留 console/network |
| [0013](../adr/0013-remove-visual-capture-entirely.md) | **视觉反馈层整体下线**（console/network 一并移除） |
| [0015](../adr/0015-approval-noise-reduction-keep-deletes.md) | 审批降噪：测试/构建/只读探查免审，删除一律人工 |

> 自重启（0009）、dsh-acp fork（0011）、需求闭环（0014）不属于 Feedback 层，见
> [`docs/README.md`](../README.md#adr-速查adr) 的总表。

## 实施顺序（汇总）

```text
P0  Phase 0: FileChange 派生 + feedback API + 前端 Change List/Summary   （看见改了什么）
P0  Phase 1: git diff + baseline + diff API + 前端 Diff                  （确认改了什么）
P0  Phase 2: checkpoint + rewind API + rewind 事件 + 前端 Rewind         （改错可回）
P0  Phase 3: preview（Discovery → Lifecycle → Proxy）+ 前端              （看到运行效果）
P1  a: Approval→Diff + 双视角    b: Checks    c: Task Outcome + Evidence
P1  d: Evidence → Continue       e: Rewind→Verify + Preview Refresh
P2  a: visual-capture 服务    b: Console/Network    c: Screenshot→Evidence   ⚠️ 已整体移除
P2  d: File Rewind ✅         e: Evidence Push ✅
P3  （触发条件满足时再进入设计）
```

> ⚠️ **P2 的 a/b/c 三项已废弃**：截图（ADR-0012）与 console/network（ADR-0013）于 2026-10-15
> 整体下线，`p2-design.md` 中相关章节仅作历史记录，**不要按其施工**。四个 preview 子路径
> （`capture` / `console` / `network` / `screenshots`）现在显式返回 **410 Gone**。
> P2 里真正落地的只有 **d: File Rewind** 与 **e: Evidence Push**。
>
> 完整的 ADR 索引以 [`docs/README.md`](../README.md#adr-速查adr) 为准（0008 起不在本文件的旧表里）。

## 定位

Feedback 不是一个「显示 Agent 输出的面板」，而是 **Agent Task 的 Observe / Verify / Control / Intervene 层**。价值排序：Changes → Diff → Preview/Checks → Rewind → Evidence → Intervene。

P0 终点：**Agent 改代码 → 用户手机看到 Changes → 查看 Diff → 必要时 Preview / Checks → 不满意 Rewind → 满意后继续**。
