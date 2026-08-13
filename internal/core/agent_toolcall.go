// Package core 的工具调用连接器：把 AgentAdapter 的 OnToolCallUpdate 回调接到任务事件流，
// 映射为 Phase 1 的 EventToolUse / EventToolResult（M3 落地）。
//
// 设计要点（同 WireContentDelta / WirePermission 的连接器范式：core→agent 单向，一个 wire 绑一个 taskID）：
//   - IsNew=true（ACP ToolCall，工具调用开始）→ 追加 EventToolUse{ToolName:Title, ToolUseID:ToolCallID}
//   - IsNew=false（ACP ToolCallUpdate，状态变更）：
//   - status=completed → 追加 EventToolResult{...}
//   - status=failed    → 追加 EventToolResult{IsError:true}
//   - 其他状态（pending/in_progress 等）→ 忽略
//   - 经 store.Update 追加事件 + Publish task_updated（工具事件离散，全量重绘合适，不用 delta）。
//
// 注：ToolCallUpdateInfo.RawInput/RawOutput 由 ACP 适配器填充（取自 ACP RawInput/RawOutput），
// 经 mapToolCallEvent 映射到 EventToolUse.Input（原样 JSON）与 EventToolResult.Result（转 string），
// 行为与 Phase 1 一致；ToolName 在 ToolCallUpdate 缺 Title 时回查同 ToolUseID 的最后一个
// EventToolUse 补全。
//
// 依赖方向：core → agent（单向），无循环。
package core

import (
	"time"

	"pieqi/internal/agent"
	"pieqi/internal/model"
)

// ToolCallHandle WireToolCall 返回的句柄，Unwire 可拆卸回调。幂等。
type ToolCallHandle struct {
	adapter agent.AgentAdapter
}

// Unwire 注销回调（置空），断开 adapter 到事件流的工具调用推送。幂等。
// 任务结束/取消时调用，避免后续误触发的工具事件再写入已终态 task。
func (h *ToolCallHandle) Unwire() {
	if h == nil || h.adapter == nil {
		return
	}
	h.adapter.OnToolCallUpdate(nil)
	h.adapter = nil
}

// WireToolCall 把一个 AgentAdapter 的 OnToolCallUpdate 回调接到任务事件流。
//
// 注册后，adapter 每产出一个工具调用更新（ACP ToolCall/ToolCallUpdate），回调内：
//  1. 映射为 EventToolUse（IsNew）或 EventToolResult（completed/failed）；
//  2. store.Update 追加事件（Seq 自增）；
//  3. Publish task_updated（全量，前端按 Seq 渲染，行为与 Phase 1 一致）。
//
// 返回 *ToolCallHandle，调用方在任务结束/取消时 Unwire 拆卸。
func WireToolCall(adapter agent.AgentAdapter, bus *EventBus, store *TaskStore, taskID string) *ToolCallHandle {
	w := &toolCallWire{bus: bus, store: store, taskID: taskID}
	adapter.OnToolCallUpdate(w.onUpdate)
	return &ToolCallHandle{adapter: adapter}
}

// toolCallWire 持有 OnToolCallUpdate 回调所需的依赖。taskID 固定（一个 wire 对应一个 task）。
type toolCallWire struct {
	bus    *EventBus
	store  *TaskStore
	taskID string
}

// onUpdate OnToolCallUpdate 回调实现：映射为 tool_use/tool_result 事件并追加 + 推送。
func (w *toolCallWire) onUpdate(info agent.ToolCallUpdateInfo) {
	ev, ok := mapToolCallEvent(info)
	if !ok {
		return // 非完成态的 ToolCallUpdate 忽略
	}
	ev.At = time.Now()
	updated, err := w.store.Update(w.taskID, func(t *model.Task) bool {
		// ToolCallUpdate 缺 Title 时，回查同 ToolUseID 的最后一个 tool_use 补全工具名。
		if ev.Type == model.EventToolResult && ev.ToolName == "" {
			if name := lookupToolName(t.Events, info.ToolCallID); name != "" {
				ev.ToolName = name
			}
		}
		ev.Seq = len(t.Events) + 1
		t.Events = append(t.Events, ev)
		return true
	})
	if err != nil || updated == nil {
		return
	}
	// 工具事件是离散的，全量重绘合适（前端按 Seq 增量渲染），不用 delta。
	w.bus.Publish(Event{Type: "task_updated", TaskID: w.taskID, Task: updated})
}

// mapToolCallEvent 把 ToolCallUpdateInfo 映射为 TaskEvent。
// 第二返回值 ok=false 表示该更新不产生事件（如 pending/in_progress 状态变更）。
func mapToolCallEvent(info agent.ToolCallUpdateInfo) (model.TaskEvent, bool) {
	if info.IsNew {
		// ACP ToolCall：工具调用开始 → EventToolUse。
		// Input 取自 RawInput（ACP RawInput，原样 JSON），行为与 Phase 1 一致。
		return model.TaskEvent{
			Type:      model.EventToolUse,
			ToolName:  info.Title,
			ToolUseID: info.ToolCallID,
			Input:     info.RawInput,
		}, true
	}
	switch info.Status {
	case "completed":
		// Result 取自 RawOutput（ACP RawOutput，转 string），为空时留空串。
		return model.TaskEvent{
			Type:      model.EventToolResult,
			ToolName:  info.Title,
			ToolUseID: info.ToolCallID,
			Result:    string(info.RawOutput),
		}, true
	case "failed":
		return model.TaskEvent{
			Type:      model.EventToolResult,
			ToolName:  info.Title,
			ToolUseID: info.ToolCallID,
			IsError:   true,
			Result:    string(info.RawOutput),
		}, true
	default:
		// pending / in_progress 等中间状态变更：忽略（前端按 tool_use/tool_result 离散渲染即可）。
		return model.TaskEvent{}, false
	}
}

// lookupToolName 在事件流中回查同 ToolUseID 的最后一个 tool_use 事件的工具名。
// 供 tool_result 在 ToolCallUpdate 未带 Title 时补全 ToolName（仿 Phase 1 pendingToolUses 映射）。
func lookupToolName(events []model.TaskEvent, toolUseID string) string {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == model.EventToolUse && events[i].ToolUseID == toolUseID {
			return events[i].ToolName
		}
	}
	return ""
}
