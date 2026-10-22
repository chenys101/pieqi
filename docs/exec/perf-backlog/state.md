# 状态（唯一交接载体）

> **读法**：新会话从这里开始。读完本文件 + `plan.md` 即可开工，不需要读任何历史对话。
> **写法**：只写真事实（提交号、数字、结论），不写过程叙述。每段收尾必须更新本文件。

- **更新时间**：2026-10-22（段 A 完成后更新）
- **源清单**：`docs/performance/perf-backlog-2026-10-22.md`
- **当前阶段**：**段 A 已完成并提交**；段 B 待开工

## 进度总表

| 段 | 内容 | 分支 / worktree | 状态 |
|---|---|---|---|
| A | P0-1 合并重复 Update | ~~`perf/p0-update-merge`~~ → **直接在 `main`** | ✅ **已完成**（见下） |
| B | P0-2 + P1-3 下发体积 | 直接在 `main` | **待开工** |
| C | P1-1 + P1-2 前端渲染 | 直接在 `main` | **待开工** |
| D | P2-1 / P2-2 / P2-3 | 直接在 `main` | 待定（需重新判断是否仍值得做） |
| E | P3-1 确认类 | 无代码 | **待开工** |

## ⚠️ worktree 方案已废止（2026-10-22）

`pieqi-perf-a` / `pieqi-perf-b` 两个 worktree **已删除**，`perf/*` 分支已删。
原因：会话进程运行在 **Low 完整性级别**，而 `D:\code\go\pieqi-perf-*` 是无低标签的浅层目录
⇒ Low 进程写不进去。实测 `workspace-write`（**含显式提权**）均被拒，只有
`danger-full-access` 可写。而子会话**无法自行提权**（scope 启动时固定）。

**现在的做法**：全部在 `D:\code\go\pieqi`（唯一可写根）**串行执行**，每段独立提交，
提交前用 `git diff --cached --name-only` 守住不混入别人的文件。

## 段 A 完成记录（2026-10-22）

- **改动**：`internal/core/agent_toolcall.go`（+ 测试）
  - 新增 `FlushToolCallWindow = 200ms` 合并窗口，照 `FlushDeltaWindow` 的成例
  - `backfillExistingToolUse` 并入主 Update（`applyToolCallUpdate`），**纯等价变换**
  - `ToolCallHandle.Unwire` **先 Flush 再拆**；新增导出 `Flush()`
  - 新测试 `TestWireToolCall_BatchesIntoOnePersist`
- **实测**（真实任务底料 2950 事件，200 条工具更新）：

  | | 落盘次数 | 总耗时 |
  |---|---|---|
  | 修复前 | 200 | 4.078 s |
  | 修复后 | 1 | 23.8 ms |

  折算真实任务 2886 条工具事件：**2 分钟级 → 1 秒以内**。

- **两条既有失败**（与本改动无关，已在干净树复现）：
  `TestCheckRunner_RerunLifecycle`、`TestWaitAfterRelease_RollsBackAndKeepsServing`。

## 已固化的事实（下一个会话直接采信，不要重新验证）

1. **`dirty_paths` 前端确实不读**：`web/src` 全量 grep 只有 `types/api.ts:391` 一处**类型声明**，
   无任何取值消费方 → P0-2 的「前端不读」判据成立。`head_sha` 在列表路径同样无消费方
   （反馈面板的 bundle 走 `/api/tasks/:id/feedback`，直读 `task.Baseline`）。
2. **列表 handler 持有 `*model.Task`**（`internal/api/tasks.go:163-188`），具备就地窄化的条件（F2）。
3. **★ WS `task_updated` 下发完整 `*model.Task`，不经过 `TaskSummary`**（F1）。
   ⇒ **P0-2 收窄列表不会改善「卡着不动」**，那条链路归 P2-2。已写进 backlog 的 P0-2 开头。
4. **`internal/model` 无 `Task.Clone`**（F3）—— 任何对 `Event.Task` 的裁剪必须先拷贝。
5. **全仓无 SSE / `text/event-stream` / `Flush` 用法**，gzip 中间件不需要为流式响应留例外，
   但**必须排除** `/api/ws`、`/api/tasks/:id/preview/*path`、`/api/tasks/:id/file`。
6. `:3000` **有**监听进程（2026-10-22 实测 pid 7308，与 `~/.pieqi/bin/pieqi.exe` SHA256 一致）。
   （本节此前记"没有监听"是错的，已更正。）做真机测量**仍必须用隔离端口**。

## 下一步动作（唯一）

开**段 B**：`P0-2`（列表/快照窄化 `Baseline`）→ 再 `P1-3`（gzip）。
先读 `docs/performance/perf-backlog-2026-10-22.md` 的 P0-2 开头那段 **F1 限制**。
