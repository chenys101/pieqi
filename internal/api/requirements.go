package api

import (
	"net/http"
	"strings"

	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
)

type createRequirementReq struct {
	Title       string                 `json:"title"`
	Body        string                 `json:"body"`
	ProjectPath string                 `json:"project_path" binding:"required"`
	ProjectID   string                 `json:"project_id"`
	Acceptance  []model.AcceptanceItem `json:"acceptance"`
}

type patchRequirementReq struct {
	Title      *string                 `json:"title"`
	Body       *string                 `json:"body"`
	Status     *model.RequirementStatus `json:"status"`
	Acceptance *[]model.AcceptanceItem `json:"acceptance"`
}

// listRequirements GET /api/requirements
func (s *Server) listRequirements(c *gin.Context) {
	if s.requirements == nil {
		c.JSON(http.StatusOK, gin.H{"requirements": []any{}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"requirements": s.requirements.List()})
}

// createRequirement POST /api/requirements
func (s *Server) createRequirement(c *gin.Context) {
	if s.requirements == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "requirements not wired"})
		return
	}
	var req createRequirementReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	r, err := s.requirements.Create(&model.Requirement{
		Title:       strings.TrimSpace(req.Title),
		Body:        req.Body,
		ProjectPath: strings.TrimSpace(req.ProjectPath),
		ProjectID:   req.ProjectID,
		Acceptance:  req.Acceptance,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, r)
}

// getRequirement GET /api/requirements/:id
func (s *Server) getRequirement(c *gin.Context) {
	if s.requirements == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "requirements not wired"})
		return
	}
	r, ok := s.requirements.Get(c.Param("id"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, r)
}

// patchRequirement PATCH /api/requirements/:id
func (s *Server) patchRequirement(c *gin.Context) {
	if s.requirements == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "requirements not wired"})
		return
	}
	var req patchRequirementReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	updated, err := s.requirements.Update(c.Param("id"), func(r *model.Requirement) bool {
		changed := false
		if req.Title != nil {
			r.Title = strings.TrimSpace(*req.Title)
			changed = true
		}
		if req.Body != nil {
			r.Body = *req.Body
			changed = true
		}
		if req.Status != nil {
			r.Status = *req.Status
			changed = true
		}
		if req.Acceptance != nil {
			r.Acceptance = *req.Acceptance
			changed = true
		}
		return changed
	})
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, updated)
}

// deleteRequirement DELETE /api/requirements/:id
func (s *Server) deleteRequirement(c *gin.Context) {
	if s.requirements == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "requirements not wired"})
		return
	}
	if err := s.requirements.Delete(c.Param("id")); err != nil {
		if strings.Contains(err.Error(), "not found") {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
