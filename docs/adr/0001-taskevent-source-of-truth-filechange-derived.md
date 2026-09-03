# TaskEvent 是事实源，FileChange 是派生模型

Feedback 的 FileChange 不建立独立聚合存储：一律从已持久化的 TaskEvent 流纯函数派生（Edit/Write/Delete/Rename 的 tool_use 入参与 tool_result 状态），按 Turn 分组，查询时现场计算（内存缓存加速）。选择它是因为 TaskEvent 已有完整落盘与 WS 全量推送，单一事实源、永不漂移，代价是查询时计算开销——等任务量大再评估增量物化。曾考虑「事件到达时增量聚合落盘」，因双写一致性与 backfill 复杂度而放弃。
