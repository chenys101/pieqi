// Package core 的用量连接器：把 agent 的上下文用量上报接到 EventBus + 任务持久化。
//
// 形状刻意与 agent_stream.go 对齐（那是内容增量的同类问题），差别只在载荷：
//   - 内容增量是**追加**型的（每段文本累进 Output/Events），必须逐条处理；
//   - 用量是**覆盖**型的（只关心此刻多满），故持久化直接整个替换，不留历史。
//
// 两条通道都按"安静持久化 + 只推轻量事件"走：不 Publish 完整 Task，避免在逐字
// 渲染期间触发前端全量重绘（理由与代价见 agent_stream.go 顶部）。
//
// 依赖方向：core → agent（单向）。
package core

import (
	"time"

	"pieqi/internal/agent"
	"pieqi/internal/model"
)

// UsageHandle WireUsage 返回的句柄；Unwire 拆卸回调（断开 adapter↔bus 连接）。
type UsageHandle struct {
	adapter agent.AgentAdapter
}

// Unwire 注销用量回调。幂等。
//
// 任务结束/取消时调用：否则迟到的 usage_update 会继续往已终态的 task 上写。
// 注意与内容增量不同 —— 用量在轮末**不该清空**（"这个任务最后占了多少上下文"
// 是终态后仍有价值的回顾信息），这里清掉的只是"还能不能继续写"。
func (h *UsageHandle) Unwire() {
	if h == nil || h.adapter == nil {
		return
	}
	if r, ok := h.adapter.(agent.UsageReporter); ok {
		r.OnUsageUpdate(nil)
	}
	h.adapter = nil
}

// WireUsage 把一个 AgentAdapter 的用量上报接到 EventBus + 任务持久化。
//
// adapter 不支持用量上报（UsageReporter 未实现，如 claude 桥/print）时返回 nil,
// 调用方需容忍 nil（Unwire 对 nil 接收者安全）—— 语义是"这个 agent 不报用量"，
// 不是故障，因此与 WireContentDelta 不同：**不注册任何回调**。
//
// 回调内做两件事：
//  1. 安静持久化：把 task.Usage 整个替换为最新快照（覆盖，不累加）。
//  2. 只 Publish task_usage（带轻量载荷，不带完整 Task）。
func WireUsage(adapter agent.AgentAdapter, bus *EventBus, store *TaskStore, taskID string) *UsageHandle {
	reporter, ok := adapter.(agent.UsageReporter)
	if !ok {
		return nil
	}
	w := &usageWire{bus: bus, store: store, taskID: taskID}
	reporter.OnUsageUpdate(w.onUsage)
	return &UsageHandle{adapter: adapter}
}

// usageWire 持有用量回调所需的依赖。taskID 固定（一个 wire 对应一个 task）。
type usageWire struct {
	bus    *EventBus
	store  *TaskStore
	taskID string
}

// onUsage OnUsageUpdate 回调实现：安静持久化 + 只推 task_usage。
//
// SessionID 忽略 —— 一个 wire 绑定一个 task，持久化以 taskID 为准（同 agent_stream）。
func (w *usageWire) onUsage(u agent.UsageInfo) {
	// Size<=0 的快照没有意义（"占用了 x/0"），丢弃。
	// ACPAgent 侧已经拦了一道，这里再拦是因为本回调也可能被别的 adapter 实现调用。
	if u.Size <= 0 {
		return
	}
	w.storeUsage(u)
	w.bus.Publish(Event{
		Type:   EventTaskUsage,
		TaskID: w.taskID,
		Usage: &UsagePayload{
			Used:    u.Used,
			Size:    u.Size,
			CostUSD: u.CostUSD,
			HasCost: u.HasCost,
		},
	})
}

// storeUsage 安静持久化用量快照（覆盖式；不发 task_updated）。
//
// 覆盖而非合并：Used/Size 是一次完整度量，只更新其中一个会让两个数来自不同时刻
// （例如 Size 涨了而 Used 还是旧的），显示出自相矛盾的进度条。
func (w *usageWire) storeUsage(u agent.UsageInfo) {
	now := time.Now()
	_, _ = w.store.Update(w.taskID, func(t *model.Task) bool {
		t.Usage = &model.TaskUsage{
			Used:    u.Used,
			Size:    u.Size,
			CostUSD: u.CostUSD,
			HasCost: u.HasCost,
			At:      now,
		}
		return true
	})
}
