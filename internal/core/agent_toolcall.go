// Package core 的工具调用连接器：把 AgentAdapter 的 OnToolCallUpdate 回调接到任务事件流，
// 映射为 Phase 1 的 EventToolUse / EventToolResult（M3 落地）。
//
// 设计要点（同 WireContentDelta / WirePermission 的连接器范式：core→agent 单向，一个 wire 绑一个 taskID）：
//   - IsNew=true（ACP ToolCall，工具调用开始）→ 追加 EventToolUse{ToolName:Title, ToolUseID:ToolCallID}
//   - IsNew=false（ACP ToolCallUpdate，状态变更）：
//   - status=completed → 追加 EventToolResult{...}
//   - status=failed    → 追加 EventToolResult{IsError:true}
//   - 其他状态（pending/in_progress 等）→ 忽略
//   - ★ 同一个 ToolCallID 的"后到的标题/入参"**合并进已有那条**，不再追加空壳
//     （mergeToolUseIntoTask；2026-10-21 修，判据见该函数注释）
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
	"encoding/json"
	"strings"
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
	// ① 入参/工具名**后到**：补全已落盘的那条 tool_use 而不是再追加一条。
	//
	// 这一步是 2026-10-21 修的那个"又坏了"的根因所在，见 mergeToolUseIntoTask 的注释。
	if backfillExistingToolUse(w.store, w.taskID, info) {
		if updated, ok := w.store.Get(w.taskID); ok && updated != nil {
			w.bus.Publish(Event{Type: "task_updated", TaskID: w.taskID, Task: updated})
		}
	}

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
		// ② 同一个 id 已经有 tool_use、但这次更新把标题/入参补齐了：合并进那一条，
		// **不再追加**第二条只有工具名的空卡（那正是"审批卡上没命令"的样子）。
		if ev.Type == model.EventToolUse {
			if mergeToolUseIntoTask(t, info) {
				return true
			}
			// 已有那条完整时若这次带了说明文字，只补说明（见 attachToolDescription）。
			// 它不影响 tool_use 的内容，只让摘要能说"这条命令是要干什么"。
			attachToolDescription(t, info)
		}
		w.store.AppendEvent(t, ev)
		return true
	})
	if err != nil || updated == nil {
		return
	}
	// 工具事件是离散的，全量重绘合适（前端按 Seq 增量渲染），不用 delta。
	w.bus.Publish(Event{Type: "task_updated", TaskID: w.taskID, Task: updated})
}

// mergeToolUseIntoTask 把一次"新工具调用"更新合并进已有的同名工具事件；返回 true 表示已处理。
//
// 判据（全部满足才合并）：
//   - 已有同 ToolCallID 的 tool_use 事件；
//   - 已有那条**入参或工具名是空的**（已完整的说明它才是权威的那条，不能覆盖）；
//   - 这次带来的入参/工具名**非空**。
//
// 为什么必须有这一步（2026-10-21 线上复现）：
// dsh 的 tool_call 通知**先发一条没有任何入参的**（标题也可能缺），入参要么随后的
// tool_call_update 带、要么根本不带（pwsh 有完整 command，edit/read 这些有时只有空壳）。
// 旧实现 `IsNew ⇒ AppendEvent` 无条件追加，于是同一调用落了两条 tool_use：
// 后一条入参为空、只有工具名。时间线渲染出来就是"有个 pwsh，但看不见命令"，
// 而审批卡当时拿到的也正是这条空壳 —— 用户看到的就是"审批只展示工具名"。
//
// 事件是**事实源**，所以只能补全不能删；补全对派生层（FileChange、Check）是安全的：
// 它们都按 (ToolName, Input) 取值，多一个字段只会让它们第一次真正取到值。
func mergeToolUseIntoTask(t *model.Task, info agent.ToolCallUpdateInfo) bool {
	if info.ToolCallID == "" || (len(info.RawInput) == 0 && info.Title == "") {
		return false
	}
	for i := len(t.Events) - 1; i >= 0; i-- {
		ev := &t.Events[i]
		if ev.Type != model.EventToolUse || ev.ToolUseID != info.ToolCallID {
			continue
		}
		changed := false
		if len(ev.Input) == 0 && len(info.RawInput) > 0 {
			ev.Input = info.RawInput
			changed = true
		}
		if ev.ToolName == "" && info.Title != "" {
			ev.ToolName = info.Title
			changed = true
		}
		if changed {
			t.UpdatedAt = time.Now()
		}
		return true
	}
	return false
}

// attachToolDescription 给已有 tool_use 补上"这条调用是干什么的"（description）。
//
// 为什么要单独一步：dsh 的 pwsh 入参里除 command 还有 description（人写的意图说明），
// 但开工通知常常先到、入参后到；而"后到的那份入参"与已落盘的那份可能**只差 description**。
// mergeToolUseIntoTask 出于"不覆盖权威入参"的判据不会动它，于是时间线/审批摘要里
// 就少了那句最能一眼读懂的话。这里只补一个键，不动其它字段。
//
// 只加不覆盖：已有 description 时保持原样（那是 agent 自己给的原文）。
func attachToolDescription(t *model.Task, info agent.ToolCallUpdateInfo) bool {
	if info.ToolCallID == "" || len(info.RawInput) == 0 {
		return false
	}
	var incoming map[string]any
	if json.Unmarshal(info.RawInput, &incoming) != nil {
		return false
	}
	desc, _ := incoming["description"].(string)
	if strings.TrimSpace(desc) == "" {
		return false
	}
	for i := len(t.Events) - 1; i >= 0; i-- {
		ev := &t.Events[i]
		if ev.Type != model.EventToolUse || ev.ToolUseID != info.ToolCallID || len(ev.Input) == 0 {
			continue
		}
		var cur map[string]any
		if json.Unmarshal(ev.Input, &cur) != nil {
			return false
		}
		if s, _ := cur["description"].(string); strings.TrimSpace(s) != "" {
			return false
		}
		cur["description"] = desc
		merged, err := json.Marshal(cur)
		if err != nil {
			return false
		}
		ev.Input = merged
		t.UpdatedAt = time.Now()
		return true
	}
	return false
}

// backfillExistingToolUse 用一条 status 更新里携带的入参/工具名，补全已有的 tool_use 事件。
// 返回 true 表示确实改了内容（调用方据此推送 task_updated）。
func backfillExistingToolUse(store *TaskStore, taskID string, info agent.ToolCallUpdateInfo) bool {
	if info.ToolCallID == "" || (len(info.RawInput) == 0 && info.Title == "") {
		return false
	}
	touched := false
	_, err := store.Update(taskID, func(t *model.Task) bool {
		touched = mergeToolUseIntoTask(t, info)
		// 已有那条完整、只是少了说明文字：同样要补（它不改变 tool_use 的内容）。
		// attachToolDescription 返回它是否真的写入了。
		if attachToolDescription(t, info) {
			touched = true
		}
		return touched
	})
	return touched && err == nil
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
