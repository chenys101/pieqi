package api

import (
	"net/http"
	"strings"

	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
)

type proposalReq struct {
	Nodes []model.ProposedTaskNode `json:"nodes" binding:"required"`
}

type confirmReq struct {
	// Nodes 可选：非空则先写入提案再确认；空则确认已暂存的提案。
	Nodes []model.ProposedTaskNode `json:"nodes"`
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
	result, err := s.requirements.Confirm(c.Param("id"), req.Nodes)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	// 根节点（无依赖或依赖已齐）自动开跑；有依赖的等前置 complete。
	if s.runner != nil {
		s.runner.StartReadyInRequirement(c.Param("id"))
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
