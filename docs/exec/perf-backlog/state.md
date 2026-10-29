# 状态（唯一交接载体）

> **读法**：新会话从这里开始。读完本文件 + `plan.md` 即可开工，不需要读任何历史对话。
> **写法**：只写真事实（提交号、数字、结论），不写过程叙述。每段收尾必须更新本文件。

- **更新时间**：2026-10-29（段 C 代码落地后回填；前一次是 2026-10-22 段 A、B）
- **源清单**：`docs/performance/perf-backlog-2026-10-22.md`
- **当前阶段**：**段 A、B、C 代码均已落地并提交**；C 的封板判据（真机帧耗时 + `test:e2e:impact`）尚未补齐 —— 这是下一步唯一动作

## 进度总表

| 段 | 内容 | 分支 / worktree | 状态 |
|---|---|---|---|
| A | P0-1 合并重复 Update | **直接在 `main`** | ✅ **已完成**（`6d54416`） |
| B | P0-2 + P1-3 下发体积 | 直接在 `main` | ✅ **已完成**（见下） |
| C | P1-1 + P1-2 前端渲染 | 直接在 `main` | 🟡 **代码已完成**（`95dfe16` / `7f4814f`），**待真机封板** |
| D | P2-1 / P2-2 / P2-3 | 直接在 `main` | 待定（需重新判断是否仍值得做） |
| E | P3-1 确认类 | 无代码 | **待开工** |

## 段 C 记录（2026-10-22 提交，2026-10-29 回填）

- **P1-1**（`95dfe16`）：`mergeDeltaIntoEvents` 的 `firstText` 判定不再每条增量都
  `events.some(...)` 全量扫描，改由 session store 的 `hasText` 缓存 O(1) 读取；
  `syncFromTask` 全量替换时重算。新增 5 条测试，其中一条用 Proxy 让 `.some()` 抛错，
  证明 `hasText=true` 时**真的跳过**扫描（只断言返回值抓不到"还在扫"的回归）。
- **P1-2**（`7f4814f`）：自动折叠的 `watch([groups, status], { deep: true })` 改为标量摘要
  `(turn, 条数, 末条 id, 末条文本长度, 过程条数)`。末条 id + 文本长度是必需的 ——
  流式正文是**追加进末条**，条数与类型都不变。新增 3 条对抗性用例覆盖「增量到达后」，
  并做了变异测试（摘要退化 ⇒ 新用例变红）。
- **测试**：P1-1 后 `web: 333 passed`，P1-2 后 `336 passed`，`vue-tsc` 均通过。
- **未完成（封板判据）**：`plan.md` 要求「`npm run test:e2e:impact` 相关用例绿 + 真机帧耗时/掉帧数字」。
  这两项**没有做**，也**没有** `evidence/段C/` 记录。因此不能把 C 标成"已完成"。


## 段 B 完成记录（2026-10-22）

- **P0-2**：`model.NewTaskSummary` 改浅拷贝，裁掉 `Events` + `Baseline`。
  实测 `GET /api/tasks` **5.42 MB → 2.33 MB（-57%）**，88 个任务里含 baseline 的从 86 → **0**。
  详情端点不受影响（baseline 原样保留，实测 dirty=302 条）。
- **P1-3**：`internal/api/gzip.go` 自写中间件，标准库，无新依赖。
  实测 **3.37 MB → 1.26 MB（节省 62%）**。
  `/api/ws` 握手、`/preview/`、`/file` 三条排除路径均已端到端验证未被压缩。
- **测试**：`TestAPI_ListTasksOmitsBaselineDirtyPaths` + `TestGzip_*` 四条全绿。
  既有失败 `TestAPI_P1_PreviewAttach`（依赖真实 dev server）与本改动无关。

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

## ⚠️ worktree 方案已废止（2026-10-22）

`pieqi-perf-a` / `pieqi-perf-b` 两个 worktree **已删除**，`perf/*` 分支已删。
原因：会话进程运行在 **Low 完整性级别**，而 `D:\code\go\pieqi-perf-*` 是无低标签的浅层目录
⇒ Low 进程写不进去。实测 `workspace-write`（**含显式提权**）均被拒，只有
`danger-full-access` 可写。而子会话**无法自行提权**（scope 启动时固定）。

**现在的做法**：全部在 `D:\code\go\pieqi`（唯一可写根）**串行执行**，每段独立提交，
提交前用 `git diff --cached --name-only` 守住不混入别人的文件。

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

补**段 C 的封板证据**（代码已在 `95dfe16` / `7f4814f`，别再重做）：

1. `cd web && npm run test:e2e:impact`，确认改动相关用例绿；
2. 真机（隔离端口，**不要用 :3000**）量长会话流式期间的帧耗时/掉帧，与改动前对比；
3. 数字写进 `docs/exec/perf-backlog/evidence/段C/` 并回填本文件，C 才能改 ✅。

C 封板之后才轮到**段 D**：先按 `plan.md` 的判据重新判断 P2-1 是否仍值得做
（段 A/B 之后首页体积已从 5.42 MB 降到 1.26 MB，P2-1 可能应当关掉而不是实施）。
