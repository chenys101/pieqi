# Checks 优先复用 Agent 已产生的 Tool Result，独立 Runner 兜底

Check Provider 第一阶段从 Task 事件流里复用 Agent 自己跑过的 test/lint/build（匹配 Bash tool_use + tool_result），不额外执行命令；用户点「Run Check」重跑时才用独立 Runner（Proc 模式：超时 + 流式输出 + exit code + 审计）。选复用优先是因为零额外执行、与 Agent 行为一致；代价是只能拿到 Agent 恰好跑过的命令。曾考虑一律独立执行，因重复执行与权限噪音而放弃。
