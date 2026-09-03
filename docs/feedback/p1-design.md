# Pieqi Feedback P1 设计

> Status: Design（对应 `Pieqi「反馈体系」功能点规划.md` 的 P1 范围）
> Updated: 2026-09-03
> 关联：`CONTEXT.md`、`docs/adr/0001~0007`、`p0-design.md`（P1 全部建立在 P0 之上）

---

## 1. 目标与范围

P1 把 Feedback 从「看到改了什么」推进到**验证闭环 + 干预闭环**：

```text
P0：看到改了什么（Changes/Diff/Baseline/Checkpoint/Rewind/Preview）
P1：验证改得对不对（Checks / Task Outcome / Evidence）+ 干预（Approval→Diff / Evidence→Continue / Rewind→Verify）
```

覆盖规划项：双视角 Changes、Approval→Diff、Checks（Provider/Result/Rerun）、Task Outcome、Evidence Card、Evidence→Continue、Rewind→Verify、Preview Refresh/Attach。

P2（视觉反馈）见 `p2-design.md`；P3（探索）见 `p3-roadmap.md`。

## 2. 总体：P1 在 P0 之上加什么

```text
                    P0（已有）
  Changes / Diff / Baseline / Checkpoint / Rewind / Preview
                        │
        ┌───────────────┼────────────────┐
        ↓               ↓                ↓
  Approval→Diff     Checks Provider   Task Outcome
   （前瞻性 Diff）    （复用+重跑）      （结构化结果）
        │               │                │
        └───────┬───────┴───────┬────────┘
                ↓               ↓
           Evidence Card   Evidence → Continue
                │               │
                └──── Rewind → Verify（P1 增强）────┘
```

## 3. 双视角 Changes（规划 §14）

P0 的 `GET /feedback` 已同时返回 `turns`（Event 视角）与 `cumulative`（Baseline 视角）。P1 只需前端呈现两个视图：

- **Event View（本轮变化）**：按 Turn 展开「Turn #12 → 文件列表 → 统计」
- **Baseline View（累计变化）**：Task 启动至今的累计文件集与统计，点击进入累计 Diff

无新增后端逻辑；P1 交付 = Feedback 视图加切换 tab。

## 4. Approval → Diff（规划 §15）

**前瞻性 Diff**（审批前看到将发生什么，与 P0 的回顾性 Diff 互补）。

- **join 键已存在**：`Decision`（approval 路径）携带 tool_use id（Phase1: ToolUseID；ACP: ReqID=toolCallId），可对到对应的 pending tool_use TaskEvent。
- Diff 来源 = 工具入参（ADR-0001 派生，无新存储）：
  - Edit → old_string vs new_string
  - Write → 当前文件 vs 新内容
  - Delete → 当前文件 vs 空

API：

```text
GET /api/tasks/:id/approvals/:decisionId/diff
→ { "path": "src/Login.vue", "operation": "modify",
    "diff": "...", "additions": 40, "deletions": 12, "prospective": true }
```

前端：`ApprovalCard` 加「查看 Diff」→ 展开 Diff 视图 → 批准/拒绝（走既有 `POST /intervene`）。

## 5. Checks（规划 §16-18：Provider / Result / Rerun）

### 5.1 数据模型

```go
type Check struct {
    ID        string    `json:"id"`
    TaskID    string    `json:"task_id"`
    Turn      int       `json:"turn,omitempty"`  // Agent 自跑时归属的 Turn
    Name      string    `json:"name"`            // "npm test" / "go vet" ...
    Command   []string  `json:"command"`
    Status    string    `json:"status"`          // pending | running | success | failed | skipped
    Duration  string    `json:"duration,omitempty"`
    ExitCode  int       `json:"exit_code,omitempty"`
    Output    string    `json:"output,omitempty"` // 截断
    StartedAt time.Time `json:"started_at"`
    FinishedAt *time.Time `json:"finished_at,omitempty"`
}
```

### 5.2 Check Provider（P1 复用优先，ADR-0005）

- **来源一（默认）**：扫描 Task 事件流，Bash tool_use 命令匹配 check 模式（`^npm (test|lint|build|run ...)`、`^go (test|vet|build|fmt)`、`^pnpm|^yarn ...`）→ 以其 tool_result 生成 `Check`（success/failed by is_error）。零额外执行、与 Agent 行为一致。
- **来源二（重跑）**：`POST /api/tasks/:id/checks/:checkId/rerun` → CheckRunner 在 task 项目目录独立执行（复用 `Proc` spawn/超时/流式输出/exit code 模式），用户点击即授权，写入审计。

### 5.3 结果展示

```text
Checks
✓ npm test      (3.2s)
✓ npm lint      (1.1s)
✗ npm build     (exit 1, 12.4s)  → [查看输出] [重跑]
```

- 错误摘要：取 Output 尾部错误段（行数截断）。
- 重跑要求：超时、流式输出、exit code、运行状态、审计。

## 6. Task Outcome（规划 §19 ⭐）

### 6.1 定义（派生，不单独存储——延续 ADR-0001）

```go
type TaskOutcome struct {
    TaskID    string      `json:"task_id"`
    Status    string      `json:"status"`    // completed | partial | failed
    Changes   ChangeStats `json:"changes"`   // files / additions / deletions（累计，Agent-touched）
    Preview   PreviewState `json:"preview"`
    Checks    []CheckSummary `json:"checks"`
    Issues    []string    `json:"issues"`    // failed checks + task.error + 末轮 is_error
    Rewinds   []RewindInfo `json:"rewinds"`  // 本 Task 发生过的回退（审计）
    GeneratedAt time.Time `json:"generated_at"`
}
```

- **完成度判定（规则）**：Task 终态 + 存在 failed check → `partial`；终态 + 无 failed check（或 agent 未跑 check）→ `completed`；task 本身 failed → `failed`。
- 手机端主验收面：比完整 Timeline 更精简。

### 6.2 API

```text
GET /api/tasks/:id/outcome
```

（也可并入 `GET /feedback?outcome=1`；二选一，推荐独立端点 + Feedback 入口直达。）

## 7. Evidence Card（规划 §21）

```go
type Evidence struct {
    TaskID   string          `json:"task_id"`
    Scope    string          `json:"scope"`   // task | turn | outcome
    Turn     int             `json:"turn,omitempty"`
    Preview  PreviewState    `json:"preview"`
    Checks   []CheckSummary  `json:"checks"`
    Errors   int             `json:"errors"`
    Changes  ChangeStats     `json:"changes"`
    DiffBrief []string       `json:"diff_brief"` // 每文件一行摘要
    CreatedAt time.Time      `json:"created_at"`
}
```

- 可挂载到 Task / Turn / Outcome；渲染为紧凑卡片。
- `POST /api/tasks/:id/evidence`（可选：显式固化某时刻证据快照；默认随取随派生）。

## 8. Evidence → Continue（规划 §22 ⭐ 核心闭环）

用户点「带当前证据继续」→ 后端组装（ADR-0004）→ append_prompt 续问。

```text
POST /api/tasks/:id/continue
{ "evidence": true, "instruction": "请继续处理 build 失败" }   // evidence 可省略默认 true

→ 后端组装下一轮 Agent Context：
   请继续处理。
   当前证据：
   - src/Login.vue 已修改 (+40 -12)
   - npm test 通过
   - npm build 失败（exit 1）：<错误摘要>
   - Preview: ready
→ 走既有 append_prompt（Resume）路径
→ 返回 { "ok": true, "appended_prompt": "<上面组装出的文本>", "event_seq": N }
```

- 返回组装出的 prompt 便于审计与前端回显。
- 这是 Feedback 从「展示系统」升级为「**Agent Control System**」的关键能力。

## 9. Rewind → Verify（规划 §20 ⭐）

`POST /api/tasks/:id/rewind` 扩展 `{ verify: true }`：

```text
Rewind
  ↓ Restore Checkpoint（P0 已有）
  ↓ Recalculate Diff（P0 已有）
  ↓ Run Check —— 重跑该 Checkpoint 所在 Turn 的 checks（复用 §5.2 来源二）
  ↓ Refresh Preview —— 停止后重启（§10）
  ↓ 返回验证摘要
```

响应追加：

```json
{ "verification": {
    "restored_files": 3,
    "checks": [ { "name": "npm test", "status": "success" }, ... ],
    "preview": { "state": "running", "url": "/api/tasks/:id/preview/" }
} }
```

前端展示：「已回退到 Turn #3 之前 ✓ 3 files restored ✓ npm test ✓ Preview ready」。

## 10. Preview Refresh / Attach（规划 §24-25）

- **Refresh**：`POST /api/tasks/:id/preview/restart` = stop + start（Rewind→Verify 后、或非 HMR 框架改动后手动刷新）。P0 的「rewind 即停」升级为「rewind 停 + verify 时重启」。
- **Attach**：预览 URL 已随隧道可达；「Attach」= 生成深链/二维码在外部浏览器打开（`GET /api/tasks/:id/preview/attach` → `{url, qr}`）。P1/P2 之间按需排期。

## 11. API 契约汇总（P1 新增）

| 端点 | 说明 |
|---|---|
| `GET /api/tasks/:id/approvals/:decisionId/diff` | 前瞻性 Diff（审批卡进入） |
| `GET /api/tasks/:id/checks` | Check 列表（复用 + 重跑记录） |
| `POST /api/tasks/:id/checks/:checkId/rerun` | 重跑（超时/流式/审计） |
| `GET /api/tasks/:id/outcome` | Task Outcome（结构化结果） |
| `POST /api/tasks/:id/evidence` | 固化证据快照（可选） |
| `POST /api/tasks/:id/continue` | Evidence → Continue（后端组装） |
| `POST /api/tasks/:id/rewind`（`verify:true`） | Rewind → Verify 增强 |
| `POST /api/tasks/:id/preview/restart` | Preview Refresh |
| `GET /api/tasks/:id/preview/attach` | Preview Attach（深链/二维码） |

全部挂 `/api`（继承 ExternalAuthMiddleware）。

## 12. 状态流转

- **Check 状态机**：`pending → running → success | failed`；重跑产生新 Check 记录（不覆盖旧记录，保留审计）。
- **Continue 后**：Task 进入 running（append_prompt 既有路径）；新 Turn 编号接续。
- **Rewind→Verify 失败**：verification 返回失败项，不回滚已恢复的文件（文件恢复与验证解耦，验证结果只用于提示）。
- **Task Outcome 生成时机**：Task 进终态时计算并缓存（内存/可选落盘）；未终态时实时派生供移动端中途验收。

## 13. 验收标准

| # | 功能 | 验收 |
|---|---|---|
| 1 | 双视角 Changes | Event/Baseline 两视图切换；统计一致 |
| 2 | Approval → Diff | waiting_input 审批卡可看前瞻性 Diff 后批准/拒绝 |
| 3 | Checks 复用 | Agent 跑过的 test/lint/build 出现在 Checks，状态正确 |
| 4 | Check Rerun | 重跑有超时/流式/exit code/审计；running 态可见 |
| 5 | Task Outcome | 终态生成结构化结果；完成度规则正确；手机端直达 |
| 6 | Evidence Card | 可挂 Task/Turn/Outcome；内容与 Provider 结果一致 |
| 7 | Evidence → Continue | 一键带证据续问；返回组装文本；新 Turn 接续 |
| 8 | Rewind → Verify | 回退后自动重跑 check + 重启 preview；失败项可感知 |
| 9 | Preview Refresh/Attach | restart 可用；attach 生成可打开链接 |

## 14. 已知限制 / P2 边界

- Check 复用只能识别 Agent 恰好跑过的命令；未跑过的项目 P1 不补跑（除用户主动 Rerun）。
- Continue 后 Agent 会话上下文与文件状态的可能错位：P1 通过「组装证据进 prompt」缓解，视觉证据（截图）P2 加入。
- Task Outcome 的「部分完成」判定是规则近似，不调用模型（P1 保持规则）。
- 多用户归属 / 权限细化沿用 P1 边界外。

## 15. 落地拆解

### 后端（internal/core）

| 模块 | 内容 |
|---|---|
| `checks.go` | Check 模型、事件流复用提取、CheckRunner（重跑，Proc 模式） |
| `outcome.go` | Task Outcome 派生 + 完成度规则 |
| `evidence.go` | Evidence 聚合 + 快照 |
| `continue.go` | Evidence→Continue：组装 prompt + append_prompt |
| `api/feedback_p1.go` | 上述新端点路由/处理器 |
| `api/approval_diff.go` | 前瞻性 Diff（从 Decision→tool_use 入参） |

接线：TaskRunner 终态钩子计算 Outcome；`POST /continue` 复用 `Resume` 路径。

### 前端（web/src）

| 模块 | 内容 |
|---|---|
| `features/approval` | ApprovalCard 加「查看 Diff」 |
| `features/checks` | Checks 列表 / 输出 / 重跑 |
| `features/outcome` | Task Outcome 卡（手机端主验收面） |
| `features/evidence` | Evidence Card + Continue 按钮 |
| Feedback 视图 | 双视角 tab、Rewind→Verify 结果卡、Preview restart/attach 入口 |

### 实施顺序

```text
P1-a: Approval→Diff + 双视角（快赢）
P1-b: Checks（复用 → 重跑）
P1-c: Task Outcome + Evidence Card
P1-d: Evidence → Continue（控制闭环关键）
P1-e: Rewind → Verify + Preview Refresh
```
