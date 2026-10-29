// mode_switch.go：会话内切换 ACP 运行模式（POST /api/tasks/:id/mode）。
//
// 与 model_switch 对称：
//   Task.Mode 更新 + EventModeSwitch 时间线留痕
//   + 若该任务有活着的 ACP 会话，立刻 session/set_mode（协议允许随时切）
//
// 清单来自 Task.Modes（session/new 协商写入），不是全局 agent 探测。
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"pieqi/internal/core"
	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
)

type setTaskModeReq struct {
	Mode string `json:"mode"`
}

// setTaskMode POST /api/tasks/:id/mode。
//
// 运行中拒绝（与切模型同判据）：这一轮的模式边界已定，改 Task.Mode 会造成
// 「界面已切、本轮没切」的错觉。waiting_input / idle 可切；活会话则立刻 set_mode。
func (s *Server) setTaskMode(c *gin.Context) {
	id := c.Param("id")
	t, ok := s.store.Get(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}
	var req setTaskModeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// pending：会话还没建起来，set_mode 无处可落。
	// running：ACP 协议允许随时切（Cursor 的 agent/plan/ask 也如此），
	// 与切模型不同——模式边界是会话级能力开关，不是「这一轮 prompt 已发出」的硬边界。
	if t.Status == model.TaskPending {
		c.JSON(http.StatusConflict, gin.H{
			"error": "task is pending: wait for the session to start before switching mode",
		})
		return
	}
	target := req.Mode
	if target == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "mode is required"})
		return
	}
	if len(t.Modes) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "this task has no available modes"})
		return
	}
	if err := verifyModeChoice(t.Modes, target); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	from := t.Mode
	if from == target {
		c.JSON(http.StatusOK, gin.H{"ok": true, "mode": target, "changed": false})
		return
	}

	// 活会话先 set_mode：失败则不改落库（避免「库里已切、agent 没切」）。
	if err := s.runner.ApplyLiveMode(c.Request.Context(), id, target); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	summary := modeSwitchSummaryAPI(t.Modes, from, target)
	payload, err := json.Marshal(model.ModeSwitchPayload{From: from, To: target})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	now := time.Now()
	updated, err := s.store.Update(id, func(tk *model.Task) bool {
		tk.Mode = target
		s.store.AppendEvent(tk, model.TaskEvent{
			Type: model.EventModeSwitch, Input: payload, Text: summary, At: now,
		})
		return true
	})
	if err != nil || updated == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to switch mode"})
		return
	}
	s.bus.Publish(core.Event{Type: "task_updated", TaskID: id, Task: updated})
	c.JSON(http.StatusOK, gin.H{"ok": true, "mode": target, "changed": true})
}

func verifyModeChoice(modes []model.ModeOption, target string) error {
	for _, m := range modes {
		if m.ID == target {
			return nil
		}
	}
	return fmt.Errorf("unknown mode: %s", target)
}

func modeSwitchSummaryAPI(modes []model.ModeOption, from, to string) string {
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
