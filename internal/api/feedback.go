// feedback.go Feedback API（p0-design.md §5）：
//
//	GET  /api/tasks/:id/feedback          反馈总览（手机端一屏拉齐）
//	GET  /api/tasks/:id/feedback/diff     单文件 Diff（turn 省略 = Baseline 累计）
//	POST /api/tasks/:id/rewind            代码回退（running → 409）
//
// 事实源是 TaskEvent（ADR-0001）：每次请求从 task.Events 现场派生，不存第二份聚合。
package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"pieqi/internal/core"
	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
)

// maxDiffBytes diff 文本截断阈值（大文件只返回首段 + truncated:true）。
const maxDiffBytes = 200 * 1024

// rewindEventPayload rewind TaskEvent 的 Input 结构化载荷（p0-design.md §3.5）。
type rewindEventPayload struct {
	ToTurn        int      `json:"to_turn"`
	Restored      []string `json:"restored"`
	PreviewStopped bool    `json:"preview_stopped"`
}

type rewindReq struct {
	ToTurn int    `json:"to_turn"`
	Scope  string `json:"scope"`  // "" / "code" = 全量；"file" = 单文件（P2 §7）
	Path   string `json:"path"`   // scope=file 时必填：回退的目标文件
	Verify bool   `json:"verify"` // P1：Rewind → Verify（§9）：重跑目标 Turn checks + 需要时重启 preview
}

// getFeedback GET /api/tasks/:id/feedback：总览（turns/changes/summary/cumulative/checkpoints/preview）。
func (s *Server) getFeedback(c *gin.Context) {
	task, ok := s.requireTask(c)
	if !ok {
		return
	}
	if s.feedback == nil {
		c.JSON(http.StatusOK, core.FeedbackBundle{TaskID: task.ID})
		return
	}

	changes := s.deriveChangesBackfilled(task)
	turns := core.BuildTurnInfos(task.Events, changes)

	// 累计统计：Agent-touched 路径集的 baseline diff（用户 dirty 隔离原则 §4.3）
	cumulative := s.cumulativeSummary(task, changes)

	bundle := core.FeedbackBundle{
		TaskID:      task.ID,
		Baseline:    task.Baseline,
		Turns:       turns,
		Cumulative:  cumulative,
		Checkpoints: s.feedback.Checkpoints(task.ID),
	}
	if bundle.Checkpoints == nil {
		bundle.Checkpoints = []int{}
	}
	if s.preview != nil {
		st := s.preview.Status(task)
		bundle.Preview = &core.FeedbackPreview{
			State:     st.State,
			Framework: st.Framework,
			Port:      st.Port,
			URL:       "/api/tasks/" + task.ID + "/preview/",
		}
	}
	c.JSON(http.StatusOK, bundle)
}

// cumulativeSummary 累计统计：tracked 走 git numstat（HEAD 对比），
// untracked（Task 期间新建）按当前文件全增；deleted 由 numstat 给出 -N。
func (s *Server) cumulativeSummary(task *model.Task, changes []core.FileChange) core.ChangeSummary {
	if len(changes) == 0 {
		return core.ChangeSummary{}
	}
	paths := make([]string, 0, len(changes))
	for _, fc := range changes {
		paths = append(paths, fc.Path)
	}
	head := "HEAD"
	if task.Baseline != nil && task.Baseline.HeadSHA != "" {
		head = task.Baseline.HeadSHA
	}
	numstat := core.GitNumstatFiltered(task.WorktreePath, head, paths)

	sum := core.ChangeSummary{Files: len(paths)}
	for _, fc := range changes {
		if e, ok := numstat[fc.Path]; ok {
			sum.Additions += e.Additions
			sum.Deletions += e.Deletions
			continue
		}
		// untracked（Task 期间新建）：当前文件全增；已不存在则 0（异常态）
		if content, exists := core.ReadWorktreeFile(task.WorktreePath, fc.Path); exists {
			sum.Additions += core.CountLines(content)
		}
	}
	return sum
}

// diffResponse GET /feedback/diff 响应体。
type diffResponse struct {
	Path      string `json:"path"`
	Turn      int    `json:"turn,omitempty"`
	Operation string `json:"operation"`
	Diff      string `json:"diff"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Truncated bool   `json:"truncated"`
	Binary    bool   `json:"binary"`
}

// getFeedbackDiff GET /api/tasks/:id/feedback/diff?path=...&turn=...
// turn 省略 → Baseline 累计 diff；给定 → 该 Turn 的单文件 diff。懒加载：仅请求时计算。
func (s *Server) getFeedbackDiff(c *gin.Context) {
	task, ok := s.requireTask(c)
	if !ok {
		return
	}
	path := strings.TrimPrefix(c.Query("path"), "./")
	if path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}
	turn := 0
	if t := c.Query("turn"); t != "" {
		n, err := strconv.Atoi(t)
		if err != nil || n < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid turn"})
			return
		}
		turn = n
	}
	if s.feedback == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "feedback not enabled"})
		return
	}

	resp := diffResponse{Path: path, Turn: turn}
	if turn > 0 {
		resp.Operation, resp.Diff, resp.Additions, resp.Deletions, resp.Binary = s.turnDiff(task, turn, path)
	} else {
		resp.Operation, resp.Diff, resp.Additions, resp.Deletions, resp.Binary = s.baselineDiff(task, path)
	}
	if len(resp.Diff) > maxDiffBytes {
		resp.Diff = resp.Diff[:maxDiffBytes]
		resp.Truncated = true
	}
	c.JSON(http.StatusOK, resp)
}

// turnDiff 单文件 · 单 Turn：before = 组装 Turn 之前状态，after = Turn 结束态。
func (s *Server) turnDiff(task *model.Task, turn int, path string) (op, diff string, add, del int, binary bool) {
	before, bOK := s.feedback.AssembleBefore(task, turn, path)
	after, aOK := s.feedback.TurnContent(task, turn, path)
	if core.IsBinaryContent(before) || core.IsBinaryContent(after) {
		return "modify", "", 0, 0, true
	}
	op = "modify"
	if !bOK {
		op = "create"
	} else if !aOK {
		op = "delete"
	}
	diff, add, del = core.UnifiedDiff(path, stringOrEmpty(bOK, before), stringOrEmpty(aOK, after), 3)
	return
}

// baselineDiff 单文件 · Baseline 累计：tracked 走 git diff（真实），untracked 全增。
func (s *Server) baselineDiff(task *model.Task, path string) (op, diff string, add, del int, binary bool) {
	head := "HEAD"
	if task.Baseline != nil && task.Baseline.HeadSHA != "" {
		head = task.Baseline.HeadSHA
	}
	if content, ok := core.GitCatFileAtHead(task.WorktreePath, head, path); ok {
		// tracked：git diff（含用户起始 dirty 也一并展示——该路径已被 Agent 触碰）
		after, aOK := core.ReadWorktreeFile(task.WorktreePath, path)
		if core.IsBinaryContent(content) || core.IsBinaryContent(after) {
			return "modify", "", 0, 0, true
		}
		op = "modify"
		if !aOK {
			op = "delete"
		}
		if gitDiff, err := core.GitDiffFiltered(task.WorktreePath, head, []string{path}); err == nil && strings.TrimSpace(gitDiff) != "" {
			// 从 git diff 文本统计增删行数
			add, del = countDiffLines(gitDiff)
			return op, gitDiff, add, del, false
		}
		// git diff 空（内容与 HEAD 一致，如改后又被 Rewind 回去）→ 纯函数兜底
		diff, add, del = core.UnifiedDiff(path, string(content), stringOrEmpty(aOK, after), 3)
		return op, diff, add, del, false
	}
	// untracked（Task 期间新建 / 用户起始 dirty）：before 为空，after 全增
	after, aOK := core.ReadWorktreeFile(task.WorktreePath, path)
	if core.IsBinaryContent(after) {
		return "create", "", 0, 0, true
	}
	diff, add, del = core.UnifiedDiff(path, "", stringOrEmpty(aOK, after), 3)
	return "create", diff, add, del, false
}

// countDiffLines 从 unified diff 文本统计 + / - 行数（排除 +++/--- 头）。
func countDiffLines(diff string) (add, del int) {
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++"):
		case strings.HasPrefix(line, "---"):
		case strings.HasPrefix(line, "+"):
			add++
		case strings.HasPrefix(line, "-"):
			del++
		}
	}
	return
}

// postRewind POST /api/tasks/:id/rewind：恢复代码到 Turn N 之前（p0-design.md §5.3）。
//   - running → 409（Agent 执行中不可回退，静止边界原则）
//   - 成功：覆盖/删除/恢复文件 → 停 preview → 追加 rewind TaskEvent（Timeline 永不删除）
func (s *Server) postRewind(c *gin.Context) {
	task, ok := s.requireTask(c)
	if !ok {
		return
	}
	if task.Status == model.TaskRunning {
		c.JSON(http.StatusConflict, gin.H{"error": "Agent 执行中，暂不可回退"})
		return
	}
	if task.Status != model.TaskWaitingInput && task.Status != model.TaskCompleted &&
		task.Status != model.TaskFailed && task.Status != model.TaskCancelled {
		c.JSON(http.StatusConflict, gin.H{"error": "task not rewindable: " + string(task.Status)})
		return
	}
	var req rewindReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Scope != "" && req.Scope != "code" && req.Scope != "file" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported scope: " + req.Scope})
		return
	}
	if req.ToTurn < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "to_turn must be >= 1"})
		return
	}
	if s.feedback == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "feedback not enabled"})
		return
	}

	// scope 分派：file = 单文件回退（P2 §7）；空/code = 全量（P0）
	var res *core.RewindResult
	var err error
	if req.Scope == "file" {
		if req.Path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "scope=file requires path"})
			return
		}
		res, err = s.feedback.RewindFileToTurn(task, req.ToTurn, req.Path)
	} else {
		res, err = s.feedback.RewindToTurn(task, req.ToTurn)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Rewind 后文件已变，运行中的 preview 内容过期 → 停止
	previewStopped := false
	if s.preview != nil && s.preview.RunningPort(task.ID) > 0 {
		s.preview.Stop(task.ID)
		previewStopped = true
	}

	// P1 Rewind → Verify（§9）：重跑目标 Turn 的 checks（异步）+ 停了的 preview 重启
	var verification *verifyPayload
	if req.Verify {
		vp := s.verifyAfterRewind(task, req.ToTurn, len(res.Restored), previewStopped)
		verification = &vp
	}

	// 追加 rewind TaskEvent（持久化 + task_updated 全量推送；Timeline 永不删除）
	payload, _ := json.Marshal(rewindEventPayload{
		ToTurn: req.ToTurn, Restored: res.Restored, PreviewStopped: previewStopped,
	})
	summary := rewindSummary(req.ToTurn, res.Restored)
	updated := s.appendTaskEvent(task.ID, model.TaskEvent{
		Type: model.EventRewind, Input: payload, Text: summary,
	})

	resp := gin.H{
		"ok":               true,
		"rewind_event_seq": rewindEventSeq(updated),
		"to_turn":          req.ToTurn,
		"restored":         res.Restored,
		"preview_stopped":  previewStopped,
	}
	if verification != nil {
		resp["verification"] = verification
	}
	c.JSON(http.StatusOK, resp)
}

// rewindSummary rewind 事件的人读摘要。
func rewindSummary(toTurn int, restored []string) string {
	if len(restored) == 0 {
		return "已回退到 Turn #" + strconv.Itoa(toTurn) + " 之前（无文件需要恢复）"
	}
	return "已回退到 Turn #" + strconv.Itoa(toTurn) + " 之前，恢复 " + strconv.Itoa(len(restored)) + " 个文件"
}

// requireTask 取 task 或写 404。
func (s *Server) requireTask(c *gin.Context) (*model.Task, bool) {
	t, ok := s.store.Get(c.Param("id"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return nil, false
	}
	return t, true
}

// appendTaskEvent 追加 TaskEvent 并广播（rewind 事件入 Timeline 的通道）。
func (s *Server) appendTaskEvent(taskID string, ev model.TaskEvent) *model.Task {
	ev.At = time.Now()
	updated, err := s.store.Update(taskID, func(t *model.Task) bool {
		ev.Seq = len(t.Events) + 1
		t.Events = append(t.Events, ev)
		return true
	})
	if err != nil || updated == nil {
		return nil
	}
	s.bus.Publish(core.Event{Type: "task_updated", TaskID: taskID, Task: updated})
	return updated
}

// stringOrEmpty 把 (content, exists) 折叠为字符串（不存在 → 空串）。
func stringOrEmpty(exists bool, content []byte) string {
	if !exists {
		return ""
	}
	return string(content)
}

func rewindEventSeq(t *model.Task) int {
	if t == nil || len(t.Events) == 0 {
		return 0
	}
	return t.Events[len(t.Events)-1].Seq
}
