# Pieqi Feedback 文档索引

Feedback 体系（Observe → Verify → Control → Intervene）的完整文档集。

## 文档地图

| 文档 | 内容 | 状态 |
|---|---|---|
| [功能点规划](Pieqi「反馈体系」功能点规划.md) | 全量功能点 P0–P3 + ❌、优先级、推荐实施顺序、最终产品闭环 | Draft |
| [P0 设计](p0-design.md) | 主链路：Changes / Diff / Baseline / Checkpoint / Rewind / Preview（Discovery·Lifecycle·Proxy）、数据结构、API、状态流转、验收 | Design |
| [P1 设计](p1-design.md) | 控制闭环：双视角 Changes、Approval→Diff、Checks、Task Outcome、Evidence、Evidence→Continue、Rewind→Verify、Preview Refresh/Attach | Design |
| [P2 设计](p2-design.md) | 视觉反馈：Screenshot、Browser Console、Network Error、Screenshot→Evidence、File Rewind、Evidence Push | Design |
| [P3 路线图](p3-roadmap.md) | 探索：DOM Inspection、Region Annotation、Visual Evidence→Agent、Task Replay | Roadmap |
| [词汇表](../../CONTEXT.md) | 领域术语 + 明确不做清单 | Stable |

## ADR 索引

| ADR | 决策 |
|---|---|
| [0001](../../adr/0001-taskevent-source-of-truth-filechange-derived.md) | TaskEvent 是事实源，FileChange 是派生模型 |
| [0002](../../adr/0002-readonly-git-baseline-file-snapshot-checkpoint.md) | Baseline 只读 Git，Checkpoint/Rewind 用文件快照，绝不写用户分支 |
| [0003](../../adr/0003-turn-bounded-by-user-message.md) | Turn 以用户消息（EventUser）为边界 |
| [0004](../../adr/0004-evidence-continue-backend-assembled.md) | Evidence → Continue 由后端组装 |
| [0005](../../adr/0005-checks-reuse-agent-tool-results.md) | Checks 优先复用 Agent Tool Result，独立 Runner 兜底 |
| [0006](../../adr/0006-visual-capture-playwright-service.md) | 视觉反馈用独立 Node Playwright 服务采集 |
| [0007](../../adr/0007-feedback-non-goals.md) | 明确不做 Web IDE / Computer Use / 云桌面 / 真终端 / 可写文件浏览器 / 浏览器自动化 / 录屏 |

## 实施顺序（汇总）

```text
P0  Phase 0: FileChange 派生 + feedback API + 前端 Change List/Summary   （看见改了什么）
P0  Phase 1: git diff + baseline + diff API + 前端 Diff                  （确认改了什么）
P0  Phase 2: checkpoint + rewind API + rewind 事件 + 前端 Rewind         （改错可回）
P0  Phase 3: preview（Discovery → Lifecycle → Proxy）+ 前端              （看到运行效果）
P1  a: Approval→Diff + 双视角    b: Checks    c: Task Outcome + Evidence
P1  d: Evidence → Continue       e: Rewind→Verify + Preview Refresh
P2  a: visual-capture 服务    b: Console/Network    c: Screenshot→Evidence
P2  d: File Rewind    e: Evidence Push
P3  （触发条件满足时再进入设计）
```

## 定位

Feedback 不是一个「显示 Agent 输出的面板」，而是 **Agent Task 的 Observe / Verify / Control / Intervene 层**。价值排序：Changes → Diff → Preview/Checks → Rewind → Evidence → Intervene。

P0 终点：**Agent 改代码 → 用户手机看到 Changes → 查看 Diff → 必要时 Preview / Checks → 不满意 Rewind → 满意后继续**。
