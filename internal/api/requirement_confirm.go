package api

import (
	"net/http"
	"strings"

	"pieqi/internal/core"
	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
)

type proposalReq struct {
	Nodes []model.ProposedTaskNode `json:"nodes" binding:"required"`
}

type confirmReq struct {
	// Nodes 可选：非空则先写入提案再确认；空则确认已暂存的提案。
	Nodes []model.ProposedTaskNode `json:"nodes"`
	// Replace 为 true 时覆盖确认：废弃同需求已有工作任务，再按首次拆分落地（不 expand）。
	Replace bool `json:"replace"`
	// Force 与 Replace 联用：已有离开 planned 的工作票时必须为 true，否则 409。
	Force bool `json:"force"`
}

// putRequirementProposal PUT /api/requirements/:id/proposal
func (s *Server) putRequirementProposal(c *gin.Context) {
	if s.requirements == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "requirements not wired"})
		return
	}
	var req proposalReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	p, err := s.requirements.PutProposal(c.Param("id"), req.Nodes)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, p)
}

// getRequirementProposal GET /api/requirements/:id/proposal
func (s *Server) getRequirementProposal(c *gin.Context) {
	if s.requirements == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "requirements not wired"})
		return
	}
	p, ok := s.requirements.GetProposal(c.Param("id"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "no proposal"})
		return
	}
	c.JSON(http.StatusOK, p)
}

// deleteRequirementProposal DELETE /api/requirements/:id/proposal
func (s *Server) deleteRequirementProposal(c *gin.Context) {
	if s.requirements == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "requirements not wired"})
		return
	}
	s.requirements.DiscardProposal(c.Param("id"))
	c.Status(http.StatusNoContent)
}

// confirmRequirement POST /api/requirements/:id/confirm
func (s *Server) confirmRequirement(c *gin.Context) {
	if s.requirements == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "requirements not wired"})
		return
	}
	var req confirmReq
	_ = c.ShouldBindJSON(&req) // body 可选
	rid := c.Param("id")
	var (
		result *core.ConfirmResult
		err    error
	)
	if req.Replace {
		// 覆盖前先打断仍在跑的旧票，再统一标 cancelled。
		// abandoned 以 Cancel 前的工作票数为准（Cancel 会先置 cancelled，Abandon 再扫会少计）。
		// 有已推进工作且未 force：先 409，避免 Cancel 已执行却确认失败。
		if !req.Force && s.requirements.HasProgressedWork(rid) {
			c.JSON(http.StatusConflict, gin.H{
				"error": "requirement has progressed work tasks; confirm with force=true to abandon and replace",
				"code":  "replace_needs_force",
			})
			return
		}
		oldIDs := s.requirements.WorkTaskIDs(rid)
		if s.runner != nil {
			for _, tid := range oldIDs {
				_ = s.runner.Cancel(tid)
			}
		}
		result, err = s.requirements.ConfirmReplace(rid, req.Nodes, req.Force)
		if result != nil && result.Abandoned < len(oldIDs) {
			result.Abandoned = len(oldIDs)
		}
	} else {
		result, err = s.requirements.Confirm(rid, req.Nodes)
	}
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		if strings.Contains(err.Error(), "force=true") {
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	// 根节点（无依赖或依赖已齐）自动开跑；有依赖的等前置 complete。
	if s.runner != nil {
		s.runner.StartReadyInRequirement(rid)
	}
	c.JSON(http.StatusOK, result)
}

type promoteReq struct {
	TaskID string `json:"task_id" binding:"required"`
}

// promoteRequirement POST /api/requirements/:id/promote
func (s *Server) promoteRequirement(c *gin.Context) {
	if s.requirements == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "requirements not wired"})
		return
	}
	var req promoteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	task, err := s.requirements.Promote(c.Param("id"), req.TaskID)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, task)
}

// suggestRequirementSplit POST /api/requirements/:id/suggest
// 拉起 agent 拆分会话；完成后自动写入暂存提案（GET .../proposal），人确认后再 confirm。
func (s *Server) suggestRequirementSplit(c *gin.Context) {
	if s.requirements == nil || s.runner == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "requirements not wired"})
		return
	}
	var body struct {
		Agent string `json:"agent"`
		Model string `json:"model"`
	}
	_ = c.ShouldBindJSON(&body)
	// 与 createTask 同口径：空串归一默认，未知 agent 直接 400。
	agentName, err := s.resolveAgent(strings.TrimSpace(body.Agent))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	task, err := s.requirements.StartSplitSuggest(c.Param("id"), agentName, strings.TrimSpace(body.Model))
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	// 已在跑则只回任务，不重复 Start。
	if task.Status == model.TaskPending {
		s.runner.Start(c.Request.Context(), task)
	}
	c.JSON(http.StatusAccepted, gin.H{
		"task":    task,
		"message": "split suggest running; poll GET /proposal when task completes",
	})
}

// expandRequirement POST /api/requirements/:id/expand — 提案体同 confirm，落地为追加 planned。
func (s *Server) expandRequirement(c *gin.Context) {
	if s.requirements == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "requirements not wired"})
		return
	}
	var req confirmReq
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Nodes) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "nodes required"})
		return
	}
	result, err := s.requirements.Expand(c.Param("id"), req.Nodes)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	if s.runner != nil {
		s.runner.StartReadyInRequirement(c.Param("id"))
	}
	c.JSON(http.StatusOK, result)
}
