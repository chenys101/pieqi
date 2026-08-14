// Package core 的流式连接器：把 AgentAdapter 的 OnContentDelta 回调接到 EventBus + 任务持久化。
//
// 这是 M2（真流式落地）的核心：ACP AgentMessageChunk.Content.Text 增量 → 安静持久化进 task
// → 只 Publish task_delta（轻量，不带完整 Task）→ WS → 前端逐字追加，替换 Phase 1 在
// appendEvent 里 200ms 合并后整块 task_updated 的路径。
//
// 设计要点（避免逐 delta 全量重绘）：
//   - 用安静的 appendTextDelta 持久化（只更 store，不 Publish task_updated），
//     不复用 TaskRunner.appendEvent（它会经 transition→task_updated 带完整 task 全量推送）。
//   - 只 Publish task_delta（带 Delta 载荷），前端增量追加 DOM，不触发 render()/renderDetail()。
//   - task_updated 仍只在状态变更（setRunning/completeTask/failTask/waiting_input 等）时发，
//     那时完整 task 已含累积文本，全量重绘也一致。
//
// 依赖方向：core → agent（单向）。internal/agent 不 import internal/core，无循环。
package core

import (
	"time"

	"pieqi/internal/agent"
	"pieqi/internal/model"
)

// DeltaHandle WireContentDelta 返回的句柄，Unwire 可拆卸回调（断开 adapter↔bus 连接）。
type DeltaHandle struct {
	adapter agent.AgentAdapter
}

// Unwire 注销回调（置空），断开 adapter 到 EventBus 的增量推送。幂等。
// 任务结束/取消时调用，避免后续误触发的增量再写入已终态 task。
func (h *DeltaHandle) Unwire() {
	if h == nil || h.adapter == nil {
		return
	}
	h.adapter.OnContentDelta(nil)
	h.adapter = nil
}

// WireContentDelta 把一个 AgentAdapter 的 OnContentDelta 回调接到 EventBus + 任务持久化。
//
// 注册后，adapter 每产出一个内容增量（AgentMessageChunk=回答 / AgentThoughtChunk=思考），
// 回调内：
//  1. 安静持久化进 task：扩最后一个同类型（text/thinking）event 的 Text（无或类型不同则新建），
//     非思考增量追加到 Task.Output。
//  2. 只 Publish task_delta（带 Delta，不带完整 Task），不 Publish task_updated，
//     前端逐字追加不被全量重绘打断。
//
// 返回 DeltaHandle，调用方在任务结束/取消时 Unwire 拆卸。
func WireContentDelta(adapter agent.AgentAdapter, bus *EventBus, store *TaskStore, taskID string) *DeltaHandle {
	w := &contentDeltaWire{bus: bus, store: store, taskID: taskID}
	adapter.OnContentDelta(w.onDelta)
	return &DeltaHandle{adapter: adapter}
}

// contentDeltaWire 持有 OnContentDelta 回调所需的依赖。taskID 固定（一个 wire 对应一个 task）。
type contentDeltaWire struct {
	bus    *EventBus
	store  *TaskStore
	taskID string
}

// onDelta OnContentDelta 回调实现：安静持久化 + 只推 task_delta。
// SessionID（agent.ContentDelta.SessionID）忽略——一个 wire 绑定一个 task，持久化以 taskID 为准。
func (w *contentDeltaWire) onDelta(d agent.ContentDelta) {
	if d.Text == "" {
		return
	}
	w.appendTextDelta(w.taskID, d.Text, d.IsThought)
	w.bus.Publish(Event{
		Type:   EventTaskDelta,
		TaskID: w.taskID,
		Delta:  &DeltaPayload{Text: d.Text, IsThought: d.IsThought},
	})
}

// appendTextDelta 安静持久化内容增量：累积进 task 的 events/output，不发 task_updated。
//
//   - text 增量（IsThought=false）：扩最后一个 text event（无或最后一个非 text 则新建），
//     并追加到 Task.Output。
//   - thought 增量（IsThought=true）：扩最后一个 thinking event（无或最后一个非 thinking 则新建），
//     不动 Output（思考过程不计入最终回答）。
//
// 与 TaskRunner.appendEvent 的 200ms 合并路径互斥：本方法专供 ACP 流式路径，
// 不经 transition，不 Publish task_updated，故不会触发前端全量重绘。
func (w *contentDeltaWire) appendTextDelta(taskID, text string, isThought bool) {
	targetType := model.EventText
	if isThought {
		targetType = model.EventThinking
	}
	now := time.Now()
	_, _ = w.store.Update(taskID, func(t *model.Task) bool {
		if len(t.Events) > 0 && t.Events[len(t.Events)-1].Type == targetType {
			// 累积到最后一个同类型 event（连续同类型增量合并到同一块）
			last := &t.Events[len(t.Events)-1]
			last.Text += text
			last.At = now
		} else {
			// 最后一个 event 类型不同（或空）：新建一个同类型 event
			t.Events = append(t.Events, model.TaskEvent{
				Seq:  len(t.Events) + 1,
				Type: targetType,
				Text: text,
				At:   now,
			})
		}
		// 非思考增量追加到 Output（回答正文累积；思考过程不计入）
		if !isThought {
			t.Output += text
		}
		return true
	})
}
