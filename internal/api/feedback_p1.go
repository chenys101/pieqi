// feedback_p1.go Feedback P1 API（p1-design.md §11）：
//
//	GET  /api/tasks/:id/approvals/:decisionId/diff  前瞻性 Diff（审批卡进入）
//	GET  /api/tasks/:id/checks                      Check 列表（复用 + 重跑记录）
//	POST /api/tasks/:id/checks/:checkId/rerun       重跑（异步，返回 running 态）
//	GET  /api/tasks/:id/outcome                     Task Outcome（结构化结果）
//	GET  /api/tasks/:id/evidence                    Evidence（随取随派生）
//	POST /api/tasks/:id/continue                    Evidence → Continue（组装续问）
//	POST /api/tasks/:id/rewind（verify:true）        Rewind → Verify（在 feedback.go 扩展）
package api

import (
	"net/http"
	"strconv"

	"pieqi/internal/core"
	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
)

// deriveChangesBackfilled 派生 FileChange 并回填 +/- 行数。
// feedback 总览 / outcome / evidence 共用（事实源 TaskEvent，每次现场派生）。
func (s *Server) deriveChangesBackfilled(task *model.Task) []core.FileChange {
	if s.feedback == nil {
		return core.DeriveFileChanges(task.Events, task.WorktreePath, nil)
	}
	seen := func(path string) bool { return s.feedback.SeenBeforeTask(task, path) }
	changes := core.DeriveFileChanges(task.Events, task.WorktreePath, seen)
	for i := range changes {
		fc := &changes[i]
		before, bOK := s.feedback.AssembleBefore(task, fc.Turn, fc.Path)
		after, aOK := s.feedback.TurnContent(task, fc.Turn, fc.Path)
		if core.IsBinaryContent(before) || core.IsBinaryContent(after) {
			continue // 二进制不计行数
		}
		_, add, del := core.UnifiedDiff(fc.Path, stringOrEmpty(bOK, before), stringOrEmpty(aOK, after), 3)
		fc.Additions, fc.Deletions = add, del
	}
	return changes
}

// taskChecks 合并 agent 派生 + rerun 记录（nil-safe：未接线 CheckRunner 时只有派生）。
func (s *Server) taskChecks(task *model.Task) []core.Check {
	derived := core.DeriveChecks(task.Events)
	for i := range derived {
		derived[i].TaskID = task.ID
	}
	if s.checks == nil {
		return derived
	}
	return core.MergeChecks(derived, s.checks.List(task.ID))
}

// previewOf 当前 preview 状态转 FeedbackPreview（nil = 未接线）。
func (s *Server) previewOf(task *model.Task) *core.FeedbackPreview {
	if s.preview == nil {
		return nil
	}
	st := s.preview.Status(task)
	return &core.FeedbackPreview{
		State:     st.State,
		Framework: st.Framework,
		Port:      st.Port,
		URL:       "/api/tasks/" + task.ID + "/preview/",
	}
}

// --- Approval → Diff（§4） ---

// getApprovalDiff GET /api/tasks/:id/approvals/:decisionId/diff：审批前的前瞻性 Diff。
// decisionId = tool_use id（Decision join 键）；无文件语义的工具（Bash 等）→ 404。
func (s *Server) getApprovalDiff(c *gin.Context) {
	task, ok := s.requireTask(c)
	if !ok {
		return
	}
	res, found := core.ProspectiveDiff(task, c.Param("decisionId"))
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "no prospective diff for this decision"})
		return
	}
	diff := res.Diff
	truncated := false
	if len(diff) > maxDiffBytes {
		diff = diff[:maxDiffBytes]
		truncated = true
	}
	c.JSON(http.StatusOK, gin.H{
		"path":       res.Path,
		"operation":  res.Operation,
		"diff":       diff,
		"additions":  res.Additions,
		"deletions":  res.Deletions,
		"truncated":  truncated,
		"binary":     res.Binary,
		"prospective": true,
	})
}

// --- Checks（§5） ---

// listChecks GET /api/tasks/:id/checks：agent 复用 + rerun 记录，按开始时间升序。
func (s *Server) listChecks(c *gin.Context) {
	task, ok := s.requireTask(c)
	if !ok {
		return
	}
	checks := s.taskChecks(task)
	if checks == nil {
		checks = []core.Check{}
	}
	c.JSON(http.StatusOK, gin.H{"checks": checks})
}

// rerunCheck POST /api/tasks/:id/checks/:checkId/rerun：异步重跑，返回 running 态记录。
func (s *Server) rerunCheck(c *gin.Context) {
	task, ok := s.requireTask(c)
	if !ok {
		return
	}
	if s.checks == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "checks not enabled"})
		return
	}
	checkID := c.Param("checkId")

	// 从合并列表中找到源 Check（拿 Name/Command）
	merged := s.taskChecks(task)
	var source *core.Check
	for i := range merged {
		if merged[i].ID == checkID {
			source = &merged[i]
			break
		}
	}
	if source == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "check not found: " + checkID})
		return
	}
	if source.Status == core.CheckRunning {
		c.JSON(http.StatusConflict, gin.H{"error": "check already running"})
		return
	}
	ck, err := s.checks.Rerun(task, source.ID, source.Name, source.Command)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, ck)
}

// --- Task Outcome（§6） ---

// getOutcome GET /api/tasks/:id/outcome：结构化结果（手机端主验收面）。
func (s *Server) getOutcome(c *gin.Context) {
	task, ok := s.requireTask(c)
	if !ok {
		return
	}
	changes := s.deriveChangesBackfilled(task)
	checks := s.taskChecks(task)
	c.JSON(http.StatusOK, core.DeriveOutcome(task, changes, checks, s.previewOf(task)))
}

// --- Evidence（§7） ---

// getEvidence GET /api/tasks/:id/evidence?scope=task|turn&turn=N：随取随派生。
// P2 起挂视觉证据（最新截图 + console/network 摘要，visual 接线时）。
func (s *Server) getEvidence(c *gin.Context) {
	task, ok := s.requireTask(c)
	if !ok {
		return
	}
	scope := c.DefaultQuery("scope", core.ScopeTask)
	turn := 0
	if t := c.Query("turn"); t != "" {
		if n, err := strconv.Atoi(t); err != nil || n < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid turn"})
			return
		} else {
			turn = n
		}
	}
	if scope == core.ScopeTurn && turn == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "scope=turn requires turn"})
		return
	}
	c.JSON(http.StatusOK, s.buildEvidenceWithVisual(task, scope, turn))
}

// --- Evidence → Continue（§8 ⭐ 控制闭环） ---

type continueReq struct {
	Instruction string   `json:"instruction"`
	Screenshots []string `json:"screenshots"` // P2 §6：指定携带的截图 id（省略 = 最新 N 张）
}

// postContinue POST /api/tasks/:id/continue：组装当前证据为续问 prompt，走既有 Resume。
// P2：Evidence 挂视觉证据，prompt 含截图引用（Agent 是否看图由 provider 能力决定）。
// 返回组装文本便于审计与前端回显；task running → 409。
func (s *Server) postContinue(c *gin.Context) {
	task, ok := s.requireTask(c)
	if !ok {
		return
	}
	if task.Status == model.TaskRunning || task.Status == model.TaskPending {
		c.JSON(http.StatusConflict, gin.H{"error": "Agent 执行中，稍后再试"})
		return
	}
	var req continueReq
	if err := c.ShouldBindJSON(&req); err != nil && err.Error() != "EOF" {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	checks := s.taskChecks(task)
	evidence := s.buildEvidenceWithVisual(task, core.ScopeTask, 0)
	// 显式指定截图（覆盖默认最新 N 张）；id 校验防引用不存在的截图
	if urls := s.screenshotURLsFor(task.ID, req.Screenshots); len(urls) > 0 {
		evidence.Screenshots = urls
	}
	prompt := core.EvidencePrompt(req.Instruction, evidence, checks)

	if err := s.runner.Resume(task.ID, prompt); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	// Resume 同步追加 EventUser 后异步起进程；取刚追加的 user 事件 seq 供回显定位
	seq := 0
	if t, ok := s.store.Get(task.ID); ok {
		for _, ev := range t.Events {
			if ev.Type == model.EventUser {
				seq = ev.Seq
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":              true,
		"appended_prompt": prompt,
		"event_seq":       seq,
	})
}

// --- Rewind → Verify（§9） ---

// verifyPayload rewind 响应内的验证摘要。
type verifyPayload struct {
	RestoredFiles int                    `json:"restored_files"`
	Checks        []core.Check          `json:"checks"`
	Preview       map[string]interface{} `json:"preview"`
}

// verifyAfterRewind 回退后重跑目标 Turn 的 checks（异步）+ 需要时重启 preview。
// 目标状态 = Turn N 开始之前 = Turn N-1 结束态 → 重跑 Turn N-1 的 checks；
// 该轮无 check 则退化为全量去重（覆盖「回退到 Turn 1」场景）。
func (s *Server) verifyAfterRewind(task *model.Task, toTurn, restored int, previewWasRunning bool) verifyPayload {
	vp := verifyPayload{RestoredFiles: restored, Checks: []core.Check{}}

	targetTurn := toTurn - 1
	all := core.DeriveChecks(task.Events)
	var selected []core.Check
	for _, ck := range all {
		if ck.Turn == targetTurn {
			selected = append(selected, ck)
		}
	}
	if len(selected) == 0 {
		seen := map[string]bool{}
		for _, ck := range all {
			if !seen[ck.Name] {
				seen[ck.Name] = true
				selected = append(selected, ck)
			}
		}
	}

	if s.checks != nil {
		for _, ck := range selected {
			if rerun, err := s.checks.Rerun(task, ck.ID, ck.Name, ck.Command); err == nil {
				vp.Checks = append(vp.Checks, *rerun)
			}
		}
	}

	// verify 时重启 preview（§10：rewind 停 + verify 重启）
	if previewWasRunning && s.preview != nil {
		if err := s.preview.Restart(task); err == nil {
			vp.Preview = map[string]interface{}{
				"state": core.PreviewStarting,
				"url":   "/api/tasks/" + task.ID + "/preview/",
			}
		}
	}
	if vp.Preview == nil {
		state := core.PreviewStopped
		if s.preview != nil {
			state = s.preview.Status(task).State
		}
		vp.Preview = map[string]interface{}{"state": state}
	}
	return vp
}
