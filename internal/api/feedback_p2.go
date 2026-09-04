// feedback_p2.go Feedback P2 API（p2-design.md §9）：
//
//	POST /api/tasks/:id/preview/screenshots          截图（preview 非 running → 409）
//	GET  /api/tasks/:id/preview/screenshots          截图列表（时间倒序）
//	GET  /api/tasks/:id/preview/screenshots/:id.png  截图文件（继承 preview wildcard 鉴权）
//	GET  /api/tasks/:id/preview/console?since=...    Console error/warn 窗口
//	GET  /api/tasks/:id/preview/network?since=...    网络失败窗口
//	POST /api/tasks/:id/push                         Outcome/Evidence 推送（手动）
//	POST /api/tasks/:id/rewind（scope:file）          文件级回退（feedback.go）
//
// 截图子路径挂在 preview wildcard 下（与 start/stop 同套分发，见 preview.go），
// 因此继承 ExternalAuthMiddleware 鉴权；PNG 由 Go 直出（不代理给 dev server）。
package api

import (
	"net/http"
	"strings"
	"time"

	"pieqi/internal/core"
	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
)

// visualEvidenceLimit Evidence 默认挂载的最新截图数（§6：只传引用，N 张防 prompt 膨胀）。
const visualEvidenceLimit = 3

// screenshotReq POST /preview/screenshots 请求体。
type screenshotReq struct {
	FullPage bool `json:"full_page"` // 视口（默认）/全页
}

// postScreenshot POST /api/tasks/:id/preview/screenshots：对运行中 preview 截图。
// 采集会话同时把 console/network 事件并入该 task 的内存窗口（§4/§5）。
func (s *Server) postScreenshot(c *gin.Context) {
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
	var req screenshotReq
	if err := c.ShouldBindJSON(&req); err != nil && err.Error() != "EOF" {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	shot, err := s.visual.Capture(c.Request.Context(), task.ID, port, req.FullPage)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, shot)
}

// listScreenshots GET /api/tasks/:id/preview/screenshots：该 task 的截图列表。
func (s *Server) listScreenshots(c *gin.Context) {
	task, ok := s.requireTask(c)
	if !ok {
		return
	}
	shots := []core.Screenshot{}
	if s.visual != nil {
		if list := s.visual.ListScreenshots(task.ID); list != nil {
			shots = list
		}
	}
	c.JSON(http.StatusOK, gin.H{"screenshots": shots})
}

// getScreenshotPNG GET /api/tasks/:id/preview/screenshots/<id>.png：直出 PNG。
// c.Param("path") 形如 "/screenshots/<id>.png"；id 经 shotIDPattern 校验（防路径逃逸）。
func (s *Server) getScreenshotPNG(c *gin.Context) {
	task, ok := s.requireTask(c)
	if !ok {
		return
	}
	if s.visual == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "visual capture not enabled"})
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(c.Param("path"), "/screenshots/"), ".png")
	path, found := s.visual.ScreenshotPath(task.ID, id)
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "screenshot not found"})
		return
	}
	// id 唯一 → 内容不可变，允许长缓存
	c.Header("Cache-Control", "public, max-age=86400, immutable")
	c.File(path)
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

// --- Screenshot → Evidence（§6） ---

// buildEvidenceWithVisual 派生 Evidence 并挂视觉证据：
// 最新 N 张截图 URL + console/network 窗口摘要（visual 未接线时无视觉字段）。
func (s *Server) buildEvidenceWithVisual(task *model.Task, scope string, turn int) core.Evidence {
	evidence := core.BuildEvidence(task, s.deriveChangesBackfilled(task), s.taskChecks(task), s.previewOf(task), scope, turn)
	if s.visual != nil {
		s.visual.AttachVisual(&evidence, visualEvidenceLimit)
	}
	return evidence
}

// screenshotURLsFor Continue 指定截图 id → URL 列表（校验存在；§6 Continue 携带引用）。
func (s *Server) screenshotURLsFor(taskID string, ids []string) []string {
	if s.visual == nil || len(ids) == 0 {
		return nil
	}
	var urls []string
	for _, id := range ids {
		if _, ok := s.visual.ScreenshotPath(taskID, id); ok {
			urls = append(urls, "/api/tasks/"+taskID+"/preview/screenshots/"+id+".png")
		}
	}
	return urls
}
