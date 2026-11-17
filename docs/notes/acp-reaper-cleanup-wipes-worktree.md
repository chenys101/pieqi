# ACP reaper 关会话清掉仍可续跑任务的 worktree

| 字段 | 值 |
|---|---|
| 状态 | **已落地** |
| 登记日 | 2026-11-17 |
| 相关 | ACP 保活 / `idle_timeout`；另票 Session not found（`acp-keepalive-session-not-found.md`） |

## 现象

任务因 Cursor ACP `account quota exhausted` 失败后，隔一段时间再「继续」：
worktree 目录不存在、`task/<taskID>` 分支也被删；未提交改动随目录一起没了。

## 根因

会话 `Close`（含 reaper 空闲回收）→ `onAgentSessionClosed` → `cleanup_worktrees` 时 `Cleanup`。

ACP 保活：`failTask` 不关会话；idle 超过 `idle_timeout` 才 reaper `Close` →
可 Resume 的任务被删掉磁盘工作区。

次要：`ensureTaskWorktree` 只认登记路径，不检查目录是否还在 → 静默用死 cwd 开 ACP。

## 目标（本票）

1. Close/reaper 清 worktree 加闸：任务仍可 Resume（如 failed 且保留 `WorktreePath`、
   非 cancelled/deleted）时跳过 Cleanup；仅取消、删任务、或明确「工作区可弃」时再清。
2. `ensureTaskWorktree`：已登记路径但目录不存在 → 重建或明确失败，禁止静默用死 cwd。
3. 可观测性：Cleanup 成功也打 INFO（task / path / 触发方：`reaper` | `cancel` | `delete`）。

代价：跳过 Cleanup 会留下更多 `.worktrees/` 与 `task/*` 分支，需另有回收策略
（按任务终态+TTL，或仅 cancelled/deleted 立即清）。

## 非目标

- 不并入 Session not found 自动恢复（另票）
- 不改额度文案

## 验收

1. `failTask` → 推进时钟过 IdleTimeout → reaper Close → worktree 目录仍在
2. `WorktreePath` 已设但目录被删 → Resume / `ensureTaskWorktree` 重建或显式错误
3. Cleanup INFO 能对上 reaper Close 与 task id（跳过时亦有 skip INFO + `trigger=reaper`）
