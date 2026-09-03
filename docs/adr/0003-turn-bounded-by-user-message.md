# Turn 以用户消息为边界，而非 Agent 协议 turn

Turn = EventUser(N) 到 EventUser(N+1) 之间的事件段，Turn #1 从初始 prompt 起算。选用户消息边界是因为它精确对应「让 Agent 改一件事」的可验收单元，天然是变更分组与 Checkpoint 的位置，且 EventUser 已是持久化事件流里的现成锚点。不用 Claude 协议层的 turn（num_turns/end_turn）：一次用户请求可能对应多次内部 turn，且该信息当前未持久化。
