// feedback_p2.go Feedback P2 API（p2-design.md §9）：
//
//	POST /api/tasks/:id/preview/capture           采集 console/network（preview 非 running → 409）
//	GET  /api/tasks/:id/preview/console?since=... Console error/warn 窗口
//	GET  /api/tasks/:id/preview/network?since=... 网络失败窗口
//	POST /api/tasks/:id/push                      Outcome/Evidence 推送（手动）
//	POST /api/tasks/:id/rewind（scope:file）       文件级回退（feedback.go）
//
// 截图相关的三个端点（POST/GET screenshots、GET screenshots/<id>.png）已于
// 2026-10-15 移除 —— 理由见 internal/core/visual.go 的 Capture 说明。
// 采集子路径挂在 preview wildcard 下（与 start/stop 同套分发，见 preview.go），
// 因此继承 ExternalAuthMiddleware 鉴权。
package api

import (
	"net/http"
	"strings"
	"time"

	"pieqi/internal/core"
	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
)

// capturePreview POST /api/tasks/:id/preview/capture：对运行中 preview 做一次采集，
// **只**收集 console error/warn 与网络 4xx/5xx/failed 并入事件窗口。
//
// 历史：本端点原名 /preview/screenshots（POST），语义是"截图 + 顺带采事件"。
// 2026-10-15 起截图移除（理由见 core.Capture），端点随之改名为 /preview/capture ——
// 名字如实反映现在的行为（只采集事件），避免调用方以为还能拿到图。
func (s *Server) capturePreview(c *gin.Context) {
	task, ok := s.requireTask(c)
	if !ok {
		return
	}
	if s.visual == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "visual capture not enabled"})
		return
	}
	port := 0
	if s.preview != nil {
		port = s.preview.RunningPort(task.ID)
	}
	if port == 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "preview not running"})
		return
	}
	if err := s.visual.Capture(c.Request.Context(), task.ID, port); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	// 返回本次并入的窗口摘要，让调用方立刻看到"采到了什么"（而不是一个截图对象）。
	c.JSON(http.StatusCreated, gin.H{
		"ok":      true,
		"console": s.visual.ConsoleSummaryOf(task.ID, time.Time{}),
		"network": s.visual.NetworkSummaryOf(task.ID, time.Time{}),
	})
}

// getConsole GET /api/tasks/:id/preview/console?since=<RFC3339>：console 窗口摘要。
func (s *Server) getConsole(c *gin.Context) {
	task, ok := s.requireTask(c)
	if !ok {
		return
	}
	if s.visual == nil {
		c.JSON(http.StatusOK, &core.ConsoleSummary{})
		return
	}
	c.JSON(http.StatusOK, s.visual.ConsoleSummaryOf(task.ID, sinceQuery(c)))
}

// getNetwork GET /api/tasks/:id/preview/network?since=<RFC3339>：失败请求窗口摘要。
func (s *Server) getNetwork(c *gin.Context) {
	task, ok := s.requireTask(c)
	if !ok {
		return
	}
	if s.visual == nil {
		c.JSON(http.StatusOK, &core.NetworkSummary{})
		return
	}
	c.JSON(http.StatusOK, s.visual.NetworkSummaryOf(task.ID, sinceQuery(c)))
}

// sinceQuery 解析 ?since=RFC3339 增量游标；缺省/空 = 零值（全量窗口）。
func sinceQuery(c *gin.Context) time.Time {
	raw := strings.TrimSpace(c.Query("since"))
	if raw == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return t
}

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
		evidence := s.buildEvidenceWithVisual(task, core.ScopeTask, 0)
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

// --- Visual → Evidence（§6） ---

// buildEvidenceWithVisual 派生 Evidence 并挂视觉证据：
// console/network 窗口摘要（visual 未接线时无视觉字段）。
//
// 截图已于 2026-10-15 移除（见 core.Capture 的说明）。
func (s *Server) buildEvidenceWithVisual(task *model.Task, scope string, turn int) core.Evidence {
	changes := s.deriveChangesBackfilled(task)
	evidence := core.BuildEvidence(task, changes, s.taskChecks(task), s.previewOf(task), scope, turn)
	if turn == 0 {
		// 任务级同上，换成唯一累计口径；Turn 级保留该 Turn 自身的数字（那本来就是单轮）。
		evidence.Changes = s.cumulativeSummary(task, changes).ChangeSummary
	}
	if s.visual != nil {
		s.visual.AttachVisual(&evidence)
	}
	return evidence
}
