package api

import (
	"net/http"
	"strings"

	"pieqi/internal/core"
	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
)

type openIssueReq struct {
	Title        string              `json:"title" binding:"required"`
	Detail       string              `json:"detail"`
	Severity     model.IssueSeverity `json:"severity"`
	CoversAC     []string            `json:"covers_ac"`
	SourceTaskID string              `json:"source_task_id"`
	SourceTurn   int                 `json:"source_turn"`
	CheckID      string              `json:"check_id"`
}

type humanVerifyReq struct {
	Pass   bool   `json:"pass"`
	Detail string `json:"detail"`
}

// openRequirementIssue POST /api/requirements/:id/issues
func (s *Server) openRequirementIssue(c *gin.Context) {
	if s.requirements == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "requirements not wired"})
		return
	}
	var req openIssueReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := s.requirements.OpenIssue(c.Param("id"), model.Issue{
		Title:        req.Title,
		Detail:       req.Detail,
		Severity:     req.Severity,
		CoversAC:     req.CoversAC,
		SourceTaskID: req.SourceTaskID,
		SourceTurn:   req.SourceTurn,
		CheckID:      req.CheckID,
	})
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	if result.FixTask != nil && s.runner != nil {
		s.runner.StartReadyInRequirement(c.Param("id"))
	}
	c.JSON(http.StatusCreated, result)
}

// listRequirementIssues GET /api/requirements/:id/issues
func (s *Server) listRequirementIssues(c *gin.Context) {
	if s.requirements == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "requirements not wired"})
		return
	}
	r, ok := s.requirements.Get(c.Param("id"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"issues": r.Issues})
}

// confirmIssueFix POST /api/issues/:id/confirm-fix
func (s *Server) confirmIssueFix(c *gin.Context) {
	if s.requirements == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "requirements not wired"})
		return
	}
	issueID := c.Param("id")
	reqID, ok := s.requirements.FindRequirementIDForIssue(issueID)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "issue not found"})
		return
	}
	result, err := s.requirements.ConfirmFix(reqID, issueID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if result.FixTask != nil && s.runner != nil {
		s.runner.StartReadyInRequirement(reqID)
	}
	c.JSON(http.StatusOK, result)
}

// resolveIssue POST /api/issues/:id/resolve
func (s *Server) resolveIssue(c *gin.Context) {
	if s.requirements == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "requirements not wired"})
		return
	}
	issueID := c.Param("id")
	reqID, ok := s.requirements.FindRequirementIDForIssue(issueID)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "issue not found"})
		return
	}
	r, err := s.requirements.ResolveIssue(reqID, issueID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, r)
}

// getRequirementOutcome GET /api/requirements/:id/outcome
func (s *Server) getRequirementOutcome(c *gin.Context) {
	if s.requirements == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "requirements not wired"})
		return
	}
	r, ok := s.requirements.Get(c.Param("id"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	var tasks []*model.Task
	if s.store != nil {
		for _, t := range s.store.List() {
			if t.RequirementID == r.ID {
				tasks = append(tasks, t)
			}
		}
	}
	c.JSON(http.StatusOK, core.DeriveRequirementOutcome(r, tasks))
}

// humanVerifyAC POST /api/requirements/:id/acceptance/:acId/verify
func (s *Server) humanVerifyAC(c *gin.Context) {
	if s.requirements == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "requirements not wired"})
		return
	}
	var req humanVerifyReq
	_ = c.ShouldBindJSON(&req)
	result, err := s.requirements.HumanVerify(c.Param("id"), c.Param("acId"), req.Pass, req.Detail)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

// markRequirementDone POST /api/requirements/:id/done
func (s *Server) markRequirementDone(c *gin.Context) {
	if s.requirements == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "requirements not wired"})
		return
	}
	r, err := s.requirements.MarkDone(c.Param("id"))
	if err != nil {
		status := http.StatusConflict
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, r)
}
