# Baseline 用只读 Git，Checkpoint/Rewind 用文件快照，绝不写用户分支

Task 在用户真实 repo 上原地执行（HTTP 路径 worktree 即项目本身），因此 Feedback 的版本机制必须不污染用户历史：Baseline = Task 开始时 Git HEAD 引用，只用于累计真实 Diff（只读）；Checkpoint = 被 Agent 实际修改文件的字节快照，是 Rewind 的恢复资产；Rewind = 用 Checkpoint 覆盖回文件，并以事件入 Timeline（历史不删除）。曾考虑纯 Git checkpoint（每 Turn 打 commit，Rewind 用 checkout/read-tree），但原地执行会污染用户分支并与用户未提交改动冲突，故弃用。

Checkpoint 只覆盖 Agent 修改过的文件；用户 Task 开始前的未提交修改在 Task 起始单独捕获，绝不误标为 Agent 改动。
