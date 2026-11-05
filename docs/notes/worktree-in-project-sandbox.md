# 立项：Agent worktree —— pieqi 只建议，不定死各家策略

| 字段 | 值 |
|---|---|
| 状态 | **已落地**（需求分支 `req/bb2f5ab5`） |
| 登记日 | 2026-11-05 |
| 相关 | ADR-0016（GOTMPDIR 工作区）；V2.0.1 终验「后端票单测 Access is denied」复盘 |

## 问题

pieqi 是编排层，不是 coding agent。旧默认把 git worktree 建在 `~/.pieqi/worktrees/<task>`，再把 Cursor/dsh/Claude 的 **cwd** 指过去。

真实 agent 的沙箱可写根通常是**用户打开的项目仓**。仓外 cwd → `go` work dir / `t.TempDir()` 拒写。  
各宿主还有自己的约定（Cursor：`~/.cursor/worktrees`；dsh：不建 worktree，只用给定 cwd）。**pieqi 不该代替它们定落点。**

## 定案

| 项 | 结论 |
|---|---|
| 角色 | pieqi **只建议**隔离目录；可用 `use_worktrees: false` 完全交给宿主 |
| 建议路径（`use_worktrees: true` 且 `worktree_base` 空） | **`<RepoPath>/.worktrees/<taskID>`**（仓内、通用名，非 `.pieqi` 品牌目录） |
| `worktree_base` 非空 | 覆盖根；仓外自负沙箱后果 |
| `use_worktrees: false` | **不建** git worktree；`WorktreePath = ProjectPath` |
| `~/.pieqi` | 只放 tasks / logs / db / settings，不当 agent 源码 cwd |
| Cleanup | 不得删项目根；仅移除独立 worktree 路径与 `pieqi/<taskID>` 分支 |

## 与编译权限

建议落在项目树内时，对 Cursor 默认 `workspace_readwrite` 通常可写；再配合 ADR-0016（`GOTMPDIR` 在工作区内）即可编过。  
`t.TempDir()` 仍可能撞系统 `%TEMP%`——那是单测环境变量问题，与「是否拷源码」无关。

## 非目标

- 不模仿 Cursor 把默认指到 `~/.cursor/worktrees`
- 不取消「可选的」git worktree 并行隔离
