// Package core 的 mode 连接器：把 ACP current_mode_update 接到 Task.Mode + 时间线。
//
// 用户主动切换走 api/mode_switch.go（落库 + 可选 live SetMode）；
// 本文件只处理 **agent 主动** 切换（exit plan 工具获批后发 current_mode_update）。
package core

import (
	"encoding/json"
	"time"

	"pieqi/internal/agent"
	"pieqi/internal/model"

	"go.uber.org/zap"
)

// ModeHandle WireMode 返回的句柄；Unwire 拆卸回调。
type ModeHandle struct {
	// any：只需要 ModeReporter；收 AgentAdapter 会逼测试 fake 实现整表接口。
	adapter any
}

// Unwire 注销 mode 回调。幂等。
func (h *ModeHandle) Unwire() {
	if h == nil || h.adapter == nil {
		return
	}
	if r, ok := h.adapter.(agent.ModeReporter); ok {
		r.OnModeUpdate(nil)
	}
	h.adapter = nil
}

// WireMode 把 adapter 的 current_mode_update 接到任务。不支持时返回 nil。
// adapter 参数为 any：运行时传 AgentAdapter，测试可只实现 ModeReporter。
func WireMode(adapter any, bus *EventBus, store *TaskStore, taskID string, logger *zap.Logger) *ModeHandle {
	reporter, ok := adapter.(agent.ModeReporter)
	if !ok {
		return nil
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	w := &modeWire{bus: bus, store: store, taskID: taskID, logger: logger}
	reporter.OnModeUpdate(w.onMode)
	return &ModeHandle{adapter: adapter}
}

type modeWire struct {
	bus    *EventBus
	store  *TaskStore
	taskID string
	logger *zap.Logger
}

func (w *modeWire) onMode(u agent.ModeUpdate) {
	if u.ModeID == "" {
		return
	}
	now := time.Now()
	updated, err := w.store.Update(w.taskID, func(t *model.Task) bool {
		from := t.Mode
		if from == u.ModeID {
			return false // 没变不写事件
		}
		t.Mode = u.ModeID
		payload, _ := json.Marshal(model.ModeSwitchPayload{From: from, To: u.ModeID})
		summary := modeSwitchSummary(t.Modes, from, u.ModeID)
		w.store.AppendEvent(t, model.TaskEvent{
			Type: model.EventModeSwitch, Input: payload, Text: summary, At: now,
		})
		return true
	})
	if err != nil {
		w.logger.Warn("mode update persist failed", zap.String("task", w.taskID), zap.Error(err))
		return
	}
	if updated == nil {
		return
	}
	w.bus.Publish(Event{Type: "task_updated", TaskID: w.taskID, Task: updated})
}

// SyncModesFromAdapter 在 Open/NewSession 成功后，把协商到的 modes 写进 Task。
// 无 ModeProvider / 清单空 → 清空 Task.Modes（隐藏 ModePicker）。
func SyncModesFromAdapter(store *TaskStore, bus *EventBus, taskID string, adapter agent.AgentAdapter) {
	mp, ok := adapter.(agent.ModeProvider)
	if !ok || store == nil {
		return
	}
	st := mp.Modes()
	var agentName string
	updated, err := store.Update(taskID, func(t *model.Task) bool {
		agentName = t.Agent
		opts := make([]model.ModeOption, 0, len(st.Available))
		for _, o := range st.Available {
			opts = append(opts, model.ModeOption{ID: o.ID, Name: o.Name, Description: o.Description})
		}
		changed := false
		if !modeOptionsEqual(t.Modes, opts) {
			t.Modes = opts
			changed = true
		}
		// 用户建任务时已选 mode（Task.Mode 非空）且仍在清单里 → 保留，
		// 不要被 agent 协商默认盖掉（否则新建页选了 Plan，进会话又变回 Agent）。
		if st.CurrentID != "" && t.Mode == "" {
			t.Mode = st.CurrentID
			changed = true
		} else if st.CurrentID != "" && t.Mode != "" && t.Mode != st.CurrentID {
			// 清单里仍有用户所选 → 不动 Task.Mode（applyMode 已在 Open 时落下）
			inList := false
			for _, o := range opts {
				if o.ID == t.Mode {
					inList = true
					break
				}
			}
			if !inList {
				t.Mode = st.CurrentID
				changed = true
			}
		}
		return changed
	})
	if len(st.Available) > 0 && agentName != "" {
		cached := make([]agent.ModeOption, 0, len(st.Available))
		for _, o := range st.Available {
			cached = append(cached, agent.ModeOption{ID: o.ID, Name: o.Name, Description: o.Description})
		}
		agent.RememberAgentModes(agentName, cached)
	}
	if err != nil || updated == nil || bus == nil {
		return
	}
	bus.Publish(Event{Type: "task_updated", TaskID: taskID, Task: updated})
}

func modeOptionsEqual(a, b []model.ModeOption) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].Name != b[i].Name || a[i].Description != b[i].Description {
			return false
		}
	}
	return true
}

func modeSwitchSummary(modes []model.ModeOption, from, to string) string {
	name := func(id string) string {
		if id == "" {
			return "（未设）"
		}
		for _, m := range modes {
			if m.ID == id {
				if m.Name != "" {
					return m.Name
				}
				return id
			}
		}
		return id
	}
	return "模式：" + name(from) + " → " + name(to)
}
