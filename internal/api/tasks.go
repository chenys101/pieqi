package api

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"pieqi/internal/core"
	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
)

type createTaskReq struct {
	// ProjectPath 任意本地路径（绝对路径优先），用于直接在原路径运行，不建 worktree
	ProjectPath string `json:"project_path" binding:"required"`
	Prompt      string `json:"prompt" binding:"required"`
}

func (s *Server) createTask(c *gin.Context) {
	var req createTaskReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Trim 首尾空白：PWA 文本框常带末尾换行，存进 task 的 prompt 也会带 \n（见 createTask 注释）。
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "prompt is empty"})
		return
	}

	// 项目不再预注册：一律用 project_path（绝对路径），deriveProjectID 派生稳定 id。
	abs, err := filepath.Abs(req.ProjectPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid path: " + err.Error()})
		return
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path not found or not a directory: " + abs})
		return
	}
	projectID := deriveProjectID(abs)
	projectPath := abs
	worktreePath := abs // 直接用原路径，run 里跳过 worktree 创建

	// 预置首条 user 事件（seq=1）：POST 返回即带该事件，前端立刻渲染用户气泡，
	// 且已持久化，WS 推送不会丢。续问事件由 Resume 以 EventUser 追加，风格一致。
	task, err := s.store.Create(&model.Task{
		Source:       model.SourceHTTP,
		ProjectID:    projectID,
		ProjectPath:  projectPath,
		WorktreePath: worktreePath,
		Prompt:       req.Prompt,
		Events: []model.TaskEvent{{
			Type: model.EventUser, Text: req.Prompt, Seq: 1, At: time.Now(),
		}},
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	s.bus.Publish(core.Event{Type: "task_created", TaskID: task.ID, Task: task})
	s.runner.Start(c.Request.Context(), task)
	// 异步生成一句话标题（大模型摘要）：不阻塞创建，生成后经 WS 推送替换前端截断标题
	s.runner.GenerateTitleAsync(task.ID)
	c.JSON(http.StatusCreated, task)
}

// deriveProjectID 从绝对路径派生一个稳定的 project ID（用于分组与并发限流）。
// 取最后一段目录名，空则用 "custom"。
func deriveProjectID(absPath string) string {
	base := filepath.Base(absPath)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "custom"
	}
	return base
}

// normalizeProjectKey 统一 listTasks 的分组键。
// 历史数据可能混用 "/" 与 "\"（如 G:/... vs G:\...）且 Windows 路径大小写不敏感，
// 归一后避免同一项目被分成两组（对应前端侧栏的重复分组问题）。
func normalizeProjectKey(p string) string {
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return filepath.Clean(p)
}

func (s *Server) listTasks(c *gin.Context) {
	tasks := s.store.List()
	// 按 project_path 分组（REQ-01）
	groups := map[string]*taskGroup{}
	order := []string{}
	for _, t := range tasks {
		key := normalizeProjectKey(t.ProjectPath)
		g, ok := groups[key]
		if !ok {
			g = &taskGroup{ProjectPath: t.ProjectPath}
			if t.ProjectID != "" {
				g.ProjectID = t.ProjectID
			}
			groups[key] = g
			order = append(order, key)
		}
		g.Tasks = append(g.Tasks, t)
		g.count(t.Status)
	}
	out := make([]*taskGroup, 0, len(order))
	for _, k := range order {
		out = append(out, groups[k])
	}
	c.JSON(http.StatusOK, gin.H{"projects": out})
}

type taskGroup struct {
	ProjectID   string         `json:"project_id"`
	ProjectPath string         `json:"project_path"`
	Counts      map[string]int `json:"counts"`
	Tasks       []*model.Task  `json:"tasks"`
}

func (g *taskGroup) count(st model.TaskStatus) {
	if g.Counts == nil {
		g.Counts = map[string]int{}
	}
	g.Counts[string(st)]++
}

func (s *Server) getTask(c *gin.Context) {
	t, ok := s.store.Get(c.Param("id"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}
	c.JSON(http.StatusOK, t)
}

type interveneReq struct {
	Kind       string `json:"kind" binding:"required"` // "decision" | "append_prompt"
	DecisionID string `json:"decision_id"`
	Choice     string `json:"choice"` // approve | deny
	Text       string `json:"text"`
}

func (s *Server) intervene(c *gin.Context) {
	id := c.Param("id")
	t, ok := s.store.Get(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}
	// 终态任务（completed/failed/cancelled）允许 append_prompt 续问：走 Resume 复用 session 重跑一轮。
	// decision 仅对 waiting_input 生效；running/waiting_input 的 append_prompt 走 stdin 注入。
	isTerminal := t.Status == model.TaskCompleted || t.Status == model.TaskFailed || t.Status == model.TaskCancelled
	if t.Status != model.TaskWaitingInput && t.Status != model.TaskRunning && !isTerminal {
		c.JSON(http.StatusConflict, gin.H{"error": "task not interventionable: " + string(t.Status)})
		return
	}
	var req interveneReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// 终态任务只接受 append_prompt（续问）；decision 对终态无意义
	if isTerminal && req.Kind != "append_prompt" {
		c.JSON(http.StatusConflict, gin.H{"error": "terminal task only supports append_prompt"})
		return
	}
	// 路径 B（choice waiting_input）：claude 进程已 end_turn 退出，恢复必须走 Resume。
	// 只接受 append_prompt（选项文本）；decision 对 choice 无意义。
	if t.Status == model.TaskWaitingInput && t.CurrentDecision != nil &&
		t.CurrentDecision.Kind == model.DecisionKindChoice {
		if req.Kind != "append_prompt" {
			c.JSON(http.StatusConflict, gin.H{"error": "choice decision requires option text (append_prompt)"})
			return
		}
		if err := s.runner.Resume(id, req.Text); err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"ok": true, "resumed": true})
		return
	}
	in := model.Intervention{
		TaskID: id, Kind: req.Kind, DecisionID: req.DecisionID,
		Choice: req.Choice, Text: req.Text, Source: model.SourceHTTP,
	}
	if isTerminal {
		// 同步检查 Resume 前置条件（worktree/session 存在），异步启动
		if err := s.runner.Resume(id, req.Text); err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"ok": true, "resumed": true})
		return
	}
	if err := s.runner.Intervene(id, in); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) cancelTask(c *gin.Context) {
	id := c.Param("id")
	if err := s.runner.Cancel(id); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) deleteTask(c *gin.Context) {
	id := c.Param("id")
	// 若仍在跑先取消
	_ = s.runner.Cancel(id)
	// 保活的 ACP 会话跨轮存活：删除任务时显式关闭，避免会话/子进程残留为孤儿
	s.runner.CloseAgentSession(id)
	// Feedback P0：任务删除时一并清掉 checkpoint 快照（preview 由 WatchBus 收）
	if s.feedback != nil {
		s.feedback.Cleanup(id)
	}
	if err := s.store.Delete(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	s.bus.Publish(core.Event{Type: "task_deleted", TaskID: id})
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// hookCallback PreToolUse hook 子进程回连：阻塞等决策。
func (s *Server) hookCallback(c *gin.Context) {
	var p core.HookPayload
	if err := c.ShouldBindJSON(&p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if strings.TrimSpace(p.TaskID) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "task_id required"})
		return
	}
	res := s.hooks.RegisterPending(p)
	c.JSON(http.StatusOK, res)
}
