// feedback_p2.go Feedback P2 API（p2-design.md §9）：
//
//	POST /api/tasks/:id/push                Outcome/Evidence 推送（手动）
//	POST /api/tasks/:id/rewind（scope:file） 文件级回退（feedback.go）
//
// ⚠️ **视觉采集（截图 / console / network）已于 2026-10-15 整体移除**（ADR-0012/0013）。
// 原三个 preview 子路径（capture / console / network）与截图三端点均返回 **410 Gone**，
// 分发逻辑保留在 preview.go 里，见那里的注释。
package api

import (
	"net/http"

	"pieqi/internal/core"
	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
)

// --- Evidence Push（§8） ---

// pushReq POST /push 请求体。
type pushReq struct {
	Kind        string `json:"kind"`         // outcome（默认）| evidence
	Instruction string `json:"instruction"`  // evidence 附加说明（并入推送文本）
	Channel     string `json:"channel"`      // 覆盖 OriginChannel（可选）
	ChatID      string `json:"chat_id"`      // 覆盖 OriginChatID（可选）
}

// postPush POST /api/tasks/:id/push：把 Outcome/Evidence 推送到 OriginChannel。
// 终态自动推送走 PushRegistry.WatchBus（core/push.go）；本端点是手动补充入口。
func (s *Server) postPush(c *gin.Context) {
	task, ok := s.requireTask(c)
	if !ok {
		return
	}
	if s.push == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "push not enabled"})
		return
	}
	var req pushReq
	if err := c.ShouldBindJSON(&req); err != nil && err.Error() != "EOF" {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	kind := req.Kind
	if kind == "" {
		kind = "outcome"
	}

	// 目标：默认 task 来源渠道，允许请求覆盖（如转发到 webhook 调试）
	channelName, chatID := task.OriginChannel, task.OriginChatID
	if req.Channel != "" {
		channelName = req.Channel
	}
	if req.ChatID != "" {
		chatID = req.ChatID
	}
	if channelName == "" || chatID == "" {
		c.JSON(http.StatusConflict, gin.H{"error": "task has no origin channel"})
		return
	}

	var content core.EvidencePushContent
	switch kind {
	case "outcome":
		outcome := core.DeriveOutcome(task, s.deriveChangesBackfilled(task), s.taskChecks(task), s.previewOf(task))
		content = core.EvidencePushContent{Kind: kind, Outcome: &outcome, Text: core.OutcomePushText(task)}
	case "evidence":
		evidence := s.buildEvidence(task, core.ScopeTask, 0)
		text := core.EvidencePrompt(req.Instruction, evidence, s.taskChecks(task))
		content = core.EvidencePushContent{Kind: kind, Evidence: &evidence, Text: text}
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported kind: " + kind})
		return
	}

	if err := s.push.Push(c.Request.Context(), channelName, core.PushTarget{ChatID: chatID}, content); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "kind": kind, "channel": channelName})
}

// buildEvidence 派生 Evidence（任务级或 Turn 级）。
//
// 原名为 buildEvidenceWithVisual —— 它曾额外挂 console/network（再早还挂截图）视觉证据。
// 视觉采集层已于 2026-10-15 整体移除（ADR-0012/0013），故更名并去掉挂载动作。
func (s *Server) buildEvidence(task *model.Task, scope string, turn int) core.Evidence {
	changes := s.deriveChangesBackfilled(task)
	evidence := core.BuildEvidence(task, changes, s.taskChecks(task), s.previewOf(task), scope, turn)
	if turn == 0 {
		// 任务级同上，换成唯一累计口径；Turn 级保留该 Turn 自身的数字（那本来就是单轮）。
		evidence.Changes = s.cumulativeSummary(task, changes).ChangeSummary
	}
	return evidence
}

// attachArtifactOpens 给证据卡的文件行补上 kind 与「打开」URL。
//
// 为什么放在 api 层：URL 形态（静态目录内的 html 走 /preview，其余走 /file）
// 是 api 的知识，core 只手产结构性事实。
//
// 为什么复用 collectArtifacts 而不是另算一遍：打开的通道**只允许有一处定义**。
// 概览的文件清单与「变更 → 累计变化」的清单指的是同一批文件，各自拼一份 URL
// 迟早会出现"同一份 html 两个不同的打开地址"，而那正是上一轮把产物并入文件清单
// 时要消灭的漂移。
func (s *Server) attachArtifactOpens(task *model.Task, ev *core.Evidence) {
	if ev == nil || len(ev.Files) == 0 {
		return
	}
	byPath := make(map[string]artifactDTO, len(ev.Files))
	for _, a := range s.collectArtifacts(task) {
		byPath[a.Path] = a
	}
	for i := range ev.Files {
		a, ok := byPath[ev.Files[i].Path]
		if !ok {
			continue // 非产物类型（代码/配置）或文件已不在磁盘：不挂入口
		}
		ev.Files[i].Kind = a.Kind
		ev.Files[i].Open = a.Open
	}
}
