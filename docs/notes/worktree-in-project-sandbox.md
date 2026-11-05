# 立项：Agent worktree 必须落在项目沙箱内

| 字段 | 值 |
|---|---|
| 状态 | **已落地**（需求分支 `req/bb2f5ab5`） |
| 登记日 | 2026-11-05 |
| 相关 | ADR-0016（GOTMPDIR 工作区）；V2.0.1 终验「后端票单测 Access is denied」复盘 |

## 问题

pieqi 是编排层，不是 coding agent。旧默认把 git worktree 建在 `~/.pieqi/worktrees/<task>`（`PIEQI_HOME` 下），再把 Cursor/dsh/Claude 的 **cwd** 指过去。

真实 agent 的沙箱可写根通常是**用户打开的项目仓**（如 `D:\code\go\pieqi`）。仓外 cwd 导致：

- `go` 的 work dir / `t.TempDir()` 落到系统 Temp 或仓外 → `Access is denied`
- 票面「单测编不过」实为**编排抢了工作区根**，不是业务代码坏了

## 定案

| 项 | 结论 |
|---|---|
| 默认 worktree 路径 | **`<project.RepoPath>/.pieqi/worktrees/<taskID>`**（在项目树内） |
| `pieqi.worktree_base` 为空 | 走上述默认（**不再**回落 `~/.pieqi/worktrees`） |
| 显式 `worktree_base` | 仍可用作覆盖；调用方自负沙箱后果 |
| `~/.pieqi` | 只放 tasks / logs / db / settings 等 **pieqi 运行时**，不当 agent 源码 cwd |
| Cleanup | 不得删到 `RepoPath` 本身；仅移除 worktree 路径与 `pieqi/<taskID>` 分支 |

## 非目标

- 不取消 git worktree 并行隔离
- 不把 agent 沙箱策略写进 pieqi（那是宿主的事）；pieqi 只保证 **cwd 默认落在项目树内**
